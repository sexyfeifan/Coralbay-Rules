package main

import (
	"crypto/tls"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const version = "3.2.1"

//go:embed web/*
var webFS embed.FS

type server struct {
	dataDir       string
	domain        string
	authDisabled  bool
	updaterURL    string
	updaterToken  string
	interval      time.Duration
	scheduleReset chan time.Duration
	mu            sync.RWMutex
	syncing       bool
	lastError     string
	logs          []string
}

type persistedSettings struct {
	IntervalSeconds int `json:"interval_seconds"`
}

type clientTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Extension   string `json:"extension"`
	Enhanced    bool   `json:"enhanced"`
	Description string `json:"description"`
}

var clientTemplates = []clientTemplate{
	{"clash", "Clash / Mihomo / OpenClash", "yaml", true, "666OS Pro_cn：33 个本地 MRS 规则与本地图标"},
	{"stash", "Stash", "yaml", true, "666OS Pro_cn：33 个本地 MRS 规则，适配 Stash domain/ipcidr"},
	{"surge", "Surge", "conf", false, "Perfect Panel 官方节点与基础分流模板"},
	{"loon", "Loon", "conf", false, "Perfect Panel 官方节点与基础分流模板"},
	{"shadowrocket", "Shadowrocket", "conf", false, "Perfect Panel 官方节点与基础分流模板"},
	{"quantumult-x", "Quantumult X", "conf", false, "Perfect Panel 官方节点与基础分流模板"},
	{"quantumult", "Quantumult", "conf", false, "Perfect Panel 官方节点与基础分流模板"},
	{"surfboard", "Surfboard", "conf", false, "Perfect Panel 官方节点与基础分流模板"},
	{"egern", "Egern", "yaml", false, "Perfect Panel 官方节点与基础分流模板"},
	{"hiddify", "Hiddify", "json", false, "Perfect Panel 官方节点与基础分流模板"},
	{"sing-box-1.11", "sing-box 1.11", "json", false, "Perfect Panel 官方对应版本模板"},
	{"sing-box-1.12", "sing-box 1.12", "json", false, "Perfect Panel 官方对应版本模板"},
	{"sing-box-1.13", "sing-box 1.13", "json", false, "Perfect Panel 官方对应版本模板"},
	{"sing-box-1.14", "sing-box 1.14", "json", false, "Perfect Panel 官方对应版本模板"},
	{"default", "通用订阅", "txt", false, "Perfect Panel 官方通用节点订阅模板"},
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
	dataDir := env("DATA_DIR", "/data")
	if content, err := os.ReadFile(filepath.Join(dataDir, "settings.json")); err == nil {
		var settings persistedSettings
		if json.Unmarshal(content, &settings) == nil && settings.IntervalSeconds >= 3600 && settings.IntervalSeconds <= 604800 {
			intervalSeconds = settings.IntervalSeconds
		}
	}
	s := &server{
		dataDir:       dataDir,
		domain:        env("MIRROR_DOMAIN", "rules.coralbay.top"),
		authDisabled:  strings.EqualFold(env("ADMIN_AUTH_DISABLED", "true"), "true"),
		updaterURL:    env("UPDATER_URL", "http://updater:8080/v1/update"),
		updaterToken:  os.Getenv("UPDATER_TOKEN"),
		interval:      time.Duration(intervalSeconds) * time.Second,
		scheduleReset: make(chan time.Duration, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/public/status", s.publicStatus)
	mux.HandleFunc("GET /api/public/rules", s.ruleCatalog)
	mux.HandleFunc("GET /api/public/rule-details", s.ruleDetails)
	mux.HandleFunc("GET /api/public/templates", s.templateCatalog)
	mux.HandleFunc("GET /api/admin/status", s.auth(s.adminStatus))
	mux.HandleFunc("POST /api/admin/sync", s.auth(s.syncNow))
	mux.HandleFunc("GET /api/admin/logs", s.auth(s.getLogs))
	mux.HandleFunc("GET /api/admin/releases", s.auth(s.releases))
	mux.HandleFunc("POST /api/admin/rollback", s.auth(s.rollback))
	mux.HandleFunc("PUT /api/admin/settings", s.auth(s.updateSettings))
	mux.HandleFunc("POST /api/admin/update", s.auth(s.updateContainer))
	mux.HandleFunc("GET /downloads/CoralBay_OpenClash_PPanel_Template.yaml", s.downloadTemplate)
	mux.HandleFunc("GET /downloads/templates/{client}", s.downloadClientTemplate)
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

func (s *server) ruleCatalog(w http.ResponseWriter, _ *http.Request) {
	content, err := os.ReadFile("/app/expected-files.txt")
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "规则清单读取失败"})
		return
	}
	items := make([]map[string]any, 0, 33)
	for _, relative := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		relative = strings.TrimSpace(relative)
		if relative == "" {
			continue
		}
		info, statErr := os.Stat(filepath.Join(s.dataDir, "current", filepath.FromSlash(relative)))
		behavior := "domain"
		if strings.Contains(relative, "/ip/") {
			behavior = "ipcidr"
		}
		item := map[string]any{
			"name": strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative)),
			"path": relative, "behavior": behavior, "format": "mrs",
			"original_url": "https://github.com/666OS/rules/raw/release/" + relative,
			"mirror_url":   "https://" + s.domain + "/" + relative,
			"cached":       statErr == nil, "readable": false,
			"detail":   "MRS 是 Mihomo 编译后二进制规则集，支持下载和元数据检查，不能直接作为文本展开。",
			"icon_url": "/_assets/icons/" + ruleIcon(relative),
		}
		if source := readableSource(relative); source != "" {
			item["readable"] = true
			item["source_url"] = "https://github.com/666OS/rules/blob/geo/" + source
			item["detail"] = "存在 666OS geo 可读源，可展开查看全部规则条目。"
		}
		if statErr == nil {
			item["bytes"] = info.Size()
			item["modified"] = info.ModTime()
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"rules": items, "count": len(items)})
}

func (s *server) templateCatalog(w http.ResponseWriter, _ *http.Request) {
	items := make([]map[string]any, 0, len(clientTemplates))
	for _, client := range clientTemplates {
		items = append(items, map[string]any{
			"id": client.ID, "name": client.Name, "extension": client.Extension,
			"enhanced": client.Enhanced, "description": client.Description,
			"online_url":   "https://" + s.domain + "/_templates/clients/" + client.ID + ".gotmpl",
			"download_url": "/downloads/templates/" + client.ID,
		})
	}
	writeJSON(w, 200, map[string]any{"templates": items, "count": len(items)})
}

func (s *server) ruleDetails(w http.ResponseWriter, r *http.Request) {
	relative := r.URL.Query().Get("path")
	source := readableSource(relative)
	if source == "" {
		writeJSON(w, 404, map[string]string{"error": "该 MRS 暂无对应的公开可读源"})
		return
	}
	content, err := os.ReadFile(filepath.Join(s.dataDir, "current", "_sources", "geo", filepath.FromSlash(source)))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "可读源尚未同步，请先执行规则同步"})
		return
	}
	entries := make([]string, 0)
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			entries = append(entries, line)
		}
	}
	writeJSON(w, 200, map[string]any{
		"path": relative, "source_path": source,
		"source_url": "https://github.com/666OS/rules/blob/geo/" + source,
		"count":      len(entries), "entries": entries,
	})
}

func readableSource(relative string) string {
	base := strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative))
	isIP := strings.Contains(relative, "/ip/")
	if isIP {
		name := strings.ToLower(base)
		switch name {
		case "private", "telegram", "netflix", "google", "facebook":
			return "ip/" + name + ".txt"
		}
		return ""
	}
	files := map[string]string{
		"Tracking": "site/category-public-tracker.txt", "Private": "site/private.txt",
		"Apple": "site/apple.txt", "Telegram": "site/telegram.txt", "TM": "site/category-tm.txt",
		"SocialMedia": "site/category-social-media-!cn.txt", "AI": "site/category-ai-!cn.txt",
		"Dev": "site/category-dev.txt", "YouTube": "site/youtube.txt", "Netflix": "site/netflix.txt",
		"Spotify": "site/spotify.txt", "Disney": "site/disney.txt", "Streaming": "site/category-media.txt",
		"Proxy": "site/geolocation-!cn.txt", "China": "site/cn.txt", "Speedtest": "site/connectivity-check.txt",
		"Games": "site/category-games.txt", "Crypto": "site/category-cryptocurrency.txt", "Google": "site/google.txt",
		"Microsoft": "site/microsoft.txt", "Facebook": "site/facebook.txt",
	}
	return files[base]
}

func ruleIcon(relative string) string {
	base := strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative))
	icons := map[string]string{
		"Tracking": "Reject.png", "Advertising": "Reject.png", "Private": "China.png",
		"Apple": "Apple_1.png", "Telegram": "Telegram_X.png", "TM": "Twitter.png",
		"SocialMedia": "Twitter.png", "AI": "AI.png", "Dev": "GitHub.png",
		"YouTube": "Streaming.png", "Netflix": "Streaming.png", "Spotify": "Streaming.png",
		"Disney": "Streaming.png", "Emby": "Emby.png", "Streaming": "Streaming.png",
		"Proxy": "Global.png", "China": "China.png", "Speedtest": "Speedtest.png",
		"Games": "Game.png", "Crypto": "Cryptocurrency_3.png", "Google": "Google_Search.png",
		"Microsoft": "Microsoft.png", "Facebook": "Facebook.png",
	}
	if icon := icons[base]; icon != "" {
		return icon
	}
	return "Global.png"
}

func (s *server) adminStatus(w http.ResponseWriter, _ *http.Request) {
	status, _ := s.readStatus()
	type releaseResult struct{ version, url string }
	releaseChannel := make(chan releaseResult, 1)
	certificateChannel := make(chan map[string]any, 1)
	go func() { latest, url := latestVersion(); releaseChannel <- releaseResult{latest, url} }()
	go func() { certificateChannel <- certificateStatus(s.domain) }()
	icons := s.iconStatus()
	var disk syscall.Statfs_t
	_ = syscall.Statfs(s.dataDir, &disk)
	release := <-releaseChannel
	cert := <-certificateChannel
	s.mu.RLock()
	response := map[string]any{
		"version": version, "domain": s.domain, "syncing": s.syncing,
		"last_error": s.lastError, "interval_seconds": int(s.interval.Seconds()),
		"status": status, "latest_version": release.version, "update_available": newerVersion(release.version, "v"+version),
		"release_url": release.url, "certificate": cert, "icons": icons,
		"disk_free_bytes": disk.Bavail * uint64(disk.Bsize),
		"template_url":    "https://" + s.domain + "/_templates/ppanel_openclash_pro_cn.gotmpl",
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

func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authDisabled {
			if r.Method != http.MethodGet && r.Header.Get("X-CoralBay-Action") != "console" {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "操作请求缺少控制台标记"})
				return
			}
			next(w, r)
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "当前安装未启用免登录控制台"})
	}
}

func (s *server) scheduler() {
	if _, err := s.readStatus(); err != nil {
		s.startSync()
	}
	timer := time.NewTimer(s.interval)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			s.startSync()
			s.mu.RLock()
			interval := s.interval
			s.mu.RUnlock()
			timer.Reset(interval)
		case interval := <-s.scheduleReset:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(interval)
		}
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

func (s *server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var body persistedSettings
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.IntervalSeconds < 3600 || body.IntervalSeconds > 604800 {
		writeJSON(w, 400, map[string]string{"error": "同步周期必须在 1 小时到 7 天之间"})
		return
	}
	content, _ := json.MarshalIndent(body, "", "  ")
	temp := filepath.Join(s.dataDir, ".settings.json.next")
	if err := os.WriteFile(temp, content, 0600); err != nil || os.Rename(temp, filepath.Join(s.dataDir, "settings.json")) != nil {
		os.Remove(temp)
		writeJSON(w, 500, map[string]string{"error": "设置保存失败"})
		return
	}
	interval := time.Duration(body.IntervalSeconds) * time.Second
	s.mu.Lock()
	s.interval = interval
	s.mu.Unlock()
	select {
	case s.scheduleReset <- interval:
	default:
	}
	s.addLog("同步周期已调整为 " + interval.String())
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) updateContainer(w http.ResponseWriter, _ *http.Request) {
	if s.updaterToken == "" {
		writeJSON(w, 503, map[string]string{"error": "专用更新器尚未配置，请先重新运行安装脚本"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	go func() {
		time.Sleep(750 * time.Millisecond)
		req, _ := http.NewRequest(http.MethodPost, s.updaterURL, nil)
		req.Header.Set("Authorization", "Bearer "+s.updaterToken)
		client := &http.Client{Timeout: 10 * time.Minute}
		resp, err := client.Do(req)
		if err != nil {
			s.addLog("容器更新请求失败: " + err.Error())
			return
		}
		resp.Body.Close()
		s.addLog("容器更新器返回: " + resp.Status)
	}()
}

func latestVersion() (string, string) {
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/sexyfeifan/Coralbay-Rules/releases/latest", nil)
	req.Header.Set("User-Agent", "CoralBay-Rules/"+version)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	var result struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&result) != nil {
		return "", ""
	}
	return result.TagName, result.HTMLURL
}

func newerVersion(candidate, current string) bool {
	parse := func(value string) [3]int {
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		parts := strings.Split(value, ".")
		var result [3]int
		for index := 0; index < len(parts) && index < 3; index++ {
			result[index], _ = strconv.Atoi(parts[index])
		}
		return result
	}
	a, b := parse(candidate), parse(current)
	for index := 0; index < 3; index++ {
		if a[index] != b[index] {
			return a[index] > b[index]
		}
	}
	return false
}

func certificateStatus(domain string) map[string]any {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(domain, "443"), &tls.Config{ServerName: domain, MinVersion: tls.VersionTLS12})
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	defer conn.Close()
	cert := conn.ConnectionState().PeerCertificates[0]
	return map[string]any{"ok": true, "not_after": cert.NotAfter, "days_remaining": int(time.Until(cert.NotAfter).Hours() / 24), "issuer": cert.Issuer.CommonName}
}

func (s *server) iconStatus() map[string]any {
	entries, err := os.ReadDir(filepath.Join(s.dataDir, "current", "_assets", "icons"))
	count, bytes := 0, int64(0)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".png") {
				count++
				if info, e := entry.Info(); e == nil {
					bytes += info.Size()
				}
			}
		}
	}
	return map[string]any{"ok": count == 27, "cached": count, "expected": 27, "bytes": bytes}
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
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
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

func (s *server) downloadClientTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("client")
	var selected *clientTemplate
	for index := range clientTemplates {
		if clientTemplates[index].ID == id {
			selected = &clientTemplates[index]
			break
		}
	}
	if selected == nil {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.dataDir, "current", "_templates", "clients", selected.ID+".gotmpl")
	content, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := "text/plain; charset=utf-8"
	if selected.Extension == "yaml" {
		contentType = "application/yaml; charset=utf-8"
	}
	if selected.Extension == "json" {
		contentType = "application/json; charset=utf-8"
	}
	filename := "CoralBay_PPanel_" + strings.ReplaceAll(selected.ID, "-", "_") + "." + selected.Extension
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
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
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("CDN-Cache-Control", "no-store")
		w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
		w.Write(content)
		return
	}
	if r.URL.Path == "/" {
		content, _ := fs.ReadFile(webFS, "web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("CDN-Cache-Control", "no-store")
		w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
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
