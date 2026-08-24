package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type remoteConfigPreset struct {
	ID          string    `json:"id"`
	Group       string    `json:"group"`
	Name        string    `json:"name"`
	OriginalURL string    `json:"original_url"`
	LocalURL    string    `json:"local_url"`
	Cached      bool      `json:"cached"`
	Bytes       int64     `json:"bytes,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	Error       string    `json:"error,omitempty"`
	BuiltIn     bool      `json:"built_in,omitempty"`
}

type remoteConfigSource struct {
	Group string `json:"group"`
	Name  string `json:"name"`
	URL   string `json:"url"`
}

type remoteConfigSyncResult struct {
	Total   int `json:"total"`
	Updated int `json:"updated"`
	Cached  int `json:"cached"`
	Failed  int `json:"failed"`
}

func remoteConfigID(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "rc-" + hex.EncodeToString(sum[:6])
}

func remoteConfigSources() []remoteConfigSource {
	var sources []remoteConfigSource
	if err := json.Unmarshal(remoteConfigCatalog, &sources); err != nil {
		return nil
	}
	return sources
}

func (s *server) remoteConfigDir() string {
	return filepath.Join(s.dataDir, "settings", "remote-configs")
}

func (s *server) remoteConfigPath(id string) string {
	return filepath.Join(s.remoteConfigDir(), id+".ini")
}

func (s *server) remoteConfigErrorPath(id string) string {
	return filepath.Join(s.remoteConfigDir(), id+".error")
}

func (s *server) subscriptionPresetItems() []remoteConfigPreset {
	sources := remoteConfigSources()
	items := make([]remoteConfigPreset, 0, len(sources)+2)
	items = append(items, remoteConfigPreset{ID: "coralbay-mihomopro", Group: "MihomoPro（置顶）", Name: "CoralBay MihomoPro · 666OS Pro_cn", LocalURL: "https://" + s.domain + "/_configs/coralbay-mihomopro.ini", Cached: true, BuiltIn: true})
	items = append(items, remoteConfigPreset{ID: "none", Group: "CoralBay", Name: "不使用远程配置"})
	for _, source := range sources {
		id := remoteConfigID(source.URL)
		item := remoteConfigPreset{ID: id, Group: source.Group, Name: source.Name, OriginalURL: source.URL, LocalURL: "https://" + s.domain + "/_configs/" + id + ".ini"}
		if info, err := os.Stat(s.remoteConfigPath(id)); err == nil && info.Size() > 0 {
			item.Cached, item.Bytes, item.UpdatedAt = true, info.Size(), info.ModTime().UTC()
		}
		if content, err := os.ReadFile(s.remoteConfigErrorPath(id)); err == nil {
			item.Error = strings.TrimSpace(string(content))
		}
		items = append(items, item)
	}
	return items
}

func (s *server) subscriptionPresets(w http.ResponseWriter, _ *http.Request) {
	items := s.subscriptionPresetItems()
	cached := 0
	for _, item := range items {
		if item.Cached && !item.BuiltIn {
			cached++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"presets": items, "total": len(remoteConfigSources()), "cached": cached, "note": "MihomoPro 是 CoralBay 内置本机预设；其他配置可在原链接与本机镜像之间切换。"})
}

func (s *server) fetchRemoteConfig(ctx context.Context, source remoteConfigSource) error {
	id := remoteConfigID(source.URL)
	if err := validateSubscriptionURLs(source.URL); err != nil {
		return fmt.Errorf("来源地址不安全：%w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "CoralBay-Rules/"+version)
	client := &http.Client{Timeout: 18 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("重定向过多")
		}
		return validateSubscriptionURLs(req.URL.String())
	}}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("上游返回 HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	if len(content) < 32 {
		return fmt.Errorf("配置内容为空或过短")
	}
	if err := os.MkdirAll(s.remoteConfigDir(), 0700); err != nil {
		return err
	}
	tmp := s.remoteConfigPath(id) + ".tmp"
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.remoteConfigPath(id)); err != nil {
		return err
	}
	_ = os.Remove(s.remoteConfigErrorPath(id))
	return nil
}

func (s *server) refreshRemoteConfigs(ctx context.Context) remoteConfigSyncResult {
	sources := remoteConfigSources()
	result := remoteConfigSyncResult{Total: len(sources)}
	jobs := make(chan remoteConfigSource)
	var mu sync.Mutex
	var workers sync.WaitGroup
	for i := 0; i < 24; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for source := range jobs {
				err := s.fetchRemoteConfig(ctx, source)
				id := remoteConfigID(source.URL)
				mu.Lock()
				if err == nil {
					result.Updated++
				} else {
					result.Failed++
					_ = os.MkdirAll(s.remoteConfigDir(), 0700)
					_ = os.WriteFile(s.remoteConfigErrorPath(id), []byte(err.Error()), 0600)
				}
				mu.Unlock()
			}
		}()
	}
	for _, source := range sources {
		jobs <- source
	}
	close(jobs)
	workers.Wait()
	for _, item := range s.subscriptionPresetItems() {
		if item.Cached && !item.BuiltIn {
			result.Cached++
		}
	}
	s.audit("remote-config-sync", "completed", fmt.Sprintf("updated=%d cached=%d failed=%d", result.Updated, result.Cached, result.Failed))
	return result
}

func (s *server) syncSubscriptionPresets(w http.ResponseWriter, r *http.Request) {
	result := s.refreshRemoteConfigs(r.Context())
	writeJSON(w, http.StatusOK, result)
}

func (s *server) remoteConfigFile(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	if file == "coralbay-mihomopro.ini" {
		content, err := os.ReadFile("/app/templates/subconverter/mihomopro.ini")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		content = []byte(strings.ReplaceAll(string(content), "__RULES_BASE_URL__", "https://"+s.domain+"/"))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(content)
		return
	}
	if !strings.HasSuffix(file, ".ini") {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSuffix(file, ".ini")
	if len(id) != 15 || !strings.HasPrefix(id, "rc-") {
		http.NotFound(w, r)
		return
	}
	valid := false
	for _, source := range remoteConfigSources() {
		if remoteConfigID(source.URL) == id {
			valid = true
			break
		}
	}
	if !valid {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300, stale-if-error=86400")
	http.ServeFile(w, r, s.remoteConfigPath(id))
}
