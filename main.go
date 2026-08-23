package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const version = "2.1.0"

//go:embed web/*
var webFS embed.FS

type server struct {
	dataDir      string
	domain       string
	passwordHash string
	interval     time.Duration
	mu           sync.RWMutex
	syncing      bool
	lastError    string
	logs         []string
	sessions     map[string]time.Time
}

type mirrorStatus struct {
	OK             bool   `json:"ok"`
	Repository     string `json:"repository"`
	Branch         string `json:"branch"`
	Commit         string `json:"commit"`
	SyncedAt       string `json:"synced_at"`
	ValidatedFiles int    `json:"validated_files"`
}

func main() {
	intervalSeconds, _ := strconv.Atoi(env("SYNC_INTERVAL", "21600"))
	if intervalSeconds < 3600 {
		intervalSeconds = 3600
	}
	s := &server{
		dataDir:      env("DATA_DIR", "/data"),
		domain:       env("MIRROR_DOMAIN", "rules.coralbay.top"),
		passwordHash: strings.ToLower(os.Getenv("ADMIN_PASSWORD_SHA256")),
		interval:     time.Duration(intervalSeconds) * time.Second,
		sessions:     make(map[string]time.Time),
	}
	if len(s.passwordHash) != 64 {
		log.Fatal("ADMIN_PASSWORD_SHA256 must be a SHA-256 hex digest")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/public/status", s.publicStatus)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.auth(s.logout))
	mux.HandleFunc("GET /api/admin/status", s.auth(s.adminStatus))
	mux.HandleFunc("POST /api/admin/sync", s.auth(s.syncNow))
	mux.HandleFunc("GET /api/admin/logs", s.auth(s.getLogs))
	mux.HandleFunc("GET /api/admin/releases", s.auth(s.releases))
	mux.HandleFunc("POST /api/admin/rollback", s.auth(s.rollback))
	mux.HandleFunc("GET /downloads/CoralBay_OpenClash_PPanel_Template.yaml", s.downloadTemplate)
	mux.HandleFunc("GET /admin/", s.adminPage)
	mux.HandleFunc("GET /", s.publicFiles)

	go s.scheduler()
	addr := env("LISTEN_ADDR", ":8080")
	log.Printf("CoralBay Rules v%s listening on %s", version, addr)
	log.Fatal(http.ListenAndServe(addr, securityHeaders(mux)))
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }

func (s *server) publicStatus(w http.ResponseWriter, _ *http.Request) {
	status, _ := s.readStatus()
	s.mu.RLock()
	response := map[string]any{"version": version, "domain": s.domain, "syncing": s.syncing, "status": status}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, response)
}

func (s *server) adminStatus(w http.ResponseWriter, _ *http.Request) {
	status, _ := s.readStatus()
	s.mu.RLock()
	response := map[string]any{
		"version": version, "domain": s.domain, "syncing": s.syncing,
		"last_error": s.lastError, "interval_seconds": int(s.interval.Seconds()),
		"status":       status,
		"template_url": "https://" + s.domain + "/_templates/ppanel_openclash_pro_cn.gotmpl",
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, response)
}

func (s *server) readStatus() (mirrorStatus, error) {
	var status mirrorStatus
	content, err := os.ReadFile(filepath.Join(s.dataDir, "current", "_mirror", "status.json"))
	if err != nil {
		return status, err
	}
	err = json.Unmarshal(content, &status)
	return status, err
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	sum := sha256.Sum256([]byte(body.Password))
	provided := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(provided), []byte(s.passwordHash)) != 1 {
		time.Sleep(600 * time.Millisecond)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "密码错误"})
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		http.Error(w, "session error", 500)
		return
	}
	token := hex.EncodeToString(tokenBytes)
	expires := time.Now().Add(12 * time.Hour)
	s.mu.Lock()
	s.sessions[token] = expires
	s.mu.Unlock()
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{Name: "coralbay_session", Value: token, Path: "/", Expires: expires, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("coralbay_session")
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "请先登录"})
			return
		}
		s.mu.Lock()
		expires, ok := s.sessions[cookie.Value]
		if ok && time.Now().After(expires) {
			delete(s.sessions, cookie.Value)
			ok = false
		}
		s.mu.Unlock()
		if !ok {
			writeJSON(w, 401, map[string]string{"error": "登录已失效"})
			return
		}
		next(w, r)
	}
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("coralbay_session"); err == nil {
		s.mu.Lock()
		delete(s.sessions, cookie.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "coralbay_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) scheduler() {
	if _, err := s.readStatus(); err != nil {
		s.startSync()
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for range ticker.C {
		s.startSync()
	}
}

func (s *server) syncNow(w http.ResponseWriter, _ *http.Request) {
	if !s.startSync() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "同步正在进行"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *server) startSync() bool {
	s.mu.Lock()
	if s.syncing {
		s.mu.Unlock()
		return false
	}
	s.syncing = true
	s.lastError = ""
	s.mu.Unlock()
	go func() {
		cmd := exec.Command("/usr/local/bin/coralbay-rules-sync", "once")
		cmd.Env = os.Environ()
		output, err := cmd.CombinedOutput()
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			if line != "" {
				s.addLog(line)
			}
		}
		s.mu.Lock()
		s.syncing = false
		if err != nil {
			s.lastError = err.Error()
			s.addLogLocked("同步失败: " + err.Error())
		}
		s.mu.Unlock()
	}()
	return true
}

func (s *server) addLog(line string) { s.mu.Lock(); defer s.mu.Unlock(); s.addLogLocked(line) }
func (s *server) addLogLocked(line string) {
	s.logs = append(s.logs, time.Now().Format("2006-01-02 15:04:05")+" "+line)
	if len(s.logs) > 300 {
		s.logs = s.logs[len(s.logs)-300:]
	}
}

func (s *server) getLogs(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	logs := append([]string(nil), s.logs...)
	s.mu.RUnlock()
	writeJSON(w, 200, map[string]any{"logs": logs})
}

var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func (s *server) releases(w http.ResponseWriter, _ *http.Request) {
	entries, _ := os.ReadDir(filepath.Join(s.dataDir, "releases"))
	current, _ := filepath.EvalSymlinks(filepath.Join(s.dataDir, "current"))
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !commitPattern.MatchString(entry.Name()) {
			continue
		}
		info, _ := entry.Info()
		path := filepath.Join(s.dataDir, "releases", entry.Name())
		items = append(items, map[string]any{"commit": entry.Name(), "active": path == current, "modified": info.ModTime()})
	}
	writeJSON(w, 200, map[string]any{"releases": items})
}

func (s *server) rollback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Commit string `json:"commit"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || !commitPattern.MatchString(body.Commit) {
		writeJSON(w, 400, map[string]string{"error": "版本号无效"})
		return
	}
	target := filepath.Join(s.dataDir, "releases", body.Commit)
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		writeJSON(w, 404, map[string]string{"error": "版本不存在"})
		return
	}
	temp := filepath.Join(s.dataDir, ".current.web")
	os.Remove(temp)
	if err := os.Symlink(filepath.Join("releases", body.Commit), temp); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := os.Rename(temp, filepath.Join(s.dataDir, "current")); err != nil {
		os.Remove(temp)
		http.Error(w, err.Error(), 500)
		return
	}
	s.addLog("已回滚到 " + body.Commit)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) adminPage(w http.ResponseWriter, r *http.Request) {
	content, _ := fs.ReadFile(webFS, "web/admin.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

func (s *server) downloadTemplate(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.dataDir, "current", "_templates", "ppanel_openclash_pro_cn.gotmpl")
	content, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="CoralBay_OpenClash_PPanel_Template.yaml"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(content)
}

func (s *server) publicFiles(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/assets/style.css" || r.URL.Path == "/assets/app.js" {
		name := "web/" + strings.TrimPrefix(r.URL.Path, "/assets/")
		content, err := fs.ReadFile(webFS, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(name, ".css") {
			w.Header().Set("Content-Type", "text/css")
		} else {
			w.Header().Set("Content-Type", "application/javascript")
		}
		w.Write(content)
		return
	}
	if r.URL.Path == "/" {
		content, _ := fs.ReadFile(webFS, "web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(content)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/mihomo/") || strings.HasPrefix(r.URL.Path, "/_templates/") || strings.HasPrefix(r.URL.Path, "/_assets/") || r.URL.Path == "/_mirror/status.json" {
		http.StripPrefix("/", http.FileServer(http.Dir(filepath.Join(s.dataDir, "current")))).ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
