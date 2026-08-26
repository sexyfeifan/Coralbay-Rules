package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Stored separately from history: deleting history must never revoke or
// reactivate a link. No IP addresses or unbounded User-Agent values are stored.
type subscriptionUsage struct {
	ID       string     `json:"id"`
	URL      string     `json:"url"`
	Target   string     `json:"target"`
	Disabled bool       `json:"disabled"`
	Success  uint64     `json:"success"`
	Failure  uint64     `json:"failure"`
	Blocked  uint64     `json:"blocked"`
	First    *time.Time `json:"first,omitempty"`
	Last     *time.Time `json:"last,omitempty"`
	Client   string     `json:"client"`
}

func usageID(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := u.Query()
	p.Del("sig")
	hash := sha256.Sum256([]byte(p.Encode()))
	return hex.EncodeToString(hash[:])
}

func (s *server) usagePath() string {
	return filepath.Join(s.dataDir, "settings", "subscription-usage.json")
}
func (s *server) preserveHistoryUsage() error {
	items, err := s.readUsage()
	if err != nil {
		return err
	}
	for _, h := range s.readSubscriptionHistory() {
		id := usageID(h.URL)
		if items[id] == nil {
			items[id] = &subscriptionUsage{ID: id, URL: h.URL, Target: h.Target}
		}
	}
	return s.saveUsage(items)
}

// Caller holds s.mu. Corrupt state is an error, never an empty denylist.
func (s *server) readUsage() (map[string]*subscriptionUsage, error) {
	items := map[string]*subscriptionUsage{}
	data, err := os.ReadFile(s.usagePath())
	if os.IsNotExist(err) {
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	if items == nil {
		return nil, fmt.Errorf("invalid usage state")
	}
	for _, item := range items {
		if item == nil {
			return nil, fmt.Errorf("invalid usage entry")
		}
	}
	return items, nil
}
func (s *server) saveUsage(items map[string]*subscriptionUsage) error {
	if err := os.MkdirAll(filepath.Dir(s.usagePath()), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	tmp := s.usagePath() + ".tmp"
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.usagePath())
}
func subscriptionClient(ua string) string {
	ua = strings.ToLower(ua)
	for _, name := range []string{"stash", "loon", "surge", "shadowrocket", "mihomo", "openclash", "clash", "sing-box", "quantumult"} {
		if strings.Contains(ua, name) {
			return name
		}
	}
	if strings.Contains(ua, "mozilla") {
		return "浏览器"
	}
	return "其他 / 未知"
}
func (s *server) startSubscriptionAccess(raw, ua string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.readUsage()
	if err != nil {
		return "", false, err
	}
	id := usageID(raw)
	item := items[id]
	if item == nil {
		u, _ := url.Parse(raw)
		item = &subscriptionUsage{ID: id, URL: raw, Target: u.Query().Get("target")}
		items[id] = item
	}
	now := time.Now().UTC()
	if item.First == nil {
		item.First = &now
	}
	item.Last = &now
	item.Client = subscriptionClient(ua)
	if item.Disabled {
		item.Blocked++
	}
	return id, item.Disabled, s.saveUsage(items)
}
func (s *server) finishSubscriptionAccess(id string, success bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.readUsage()
	if err != nil {
		return err
	}
	item := items[id]
	if item == nil {
		return fmt.Errorf("missing link")
	}
	if success {
		item.Success++
	} else {
		item.Failure++
	}
	return s.saveUsage(items)
}
func (s *server) subscriptionUsageCatalog(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	items, err := s.readUsage()
	if err == nil {
		for _, h := range s.readSubscriptionHistory() {
			id := usageID(h.URL)
			if items[id] == nil {
				items[id] = &subscriptionUsage{ID: id, URL: h.URL, Target: h.Target}
			}
		}
		err = s.saveUsage(items)
	}
	s.mu.Unlock()
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "链接统计状态读取或保存失败"})
		return
	}
	list := make([]*subscriptionUsage, 0, len(items))
	for _, item := range items {
		list = append(list, item)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Last == nil {
			return false
		}
		if list[j].Last == nil {
			return true
		}
		return list[i].Last.After(*list[j].Last)
	})
	writeJSON(w, 200, map[string]any{"links": list, "count": len(list)})
}
func (s *server) setSubscriptionDisabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Disabled *bool `json:"disabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil || body.Disabled == nil {
		writeJSON(w, 400, map[string]string{"error": "需要 disabled 布尔值"})
		return
	}
	s.mu.Lock()
	items, err := s.readUsage()
	found := false
	if err == nil {
		if item := items[r.PathValue("id")]; item != nil {
			found = true
			item.Disabled = *body.Disabled
			err = s.saveUsage(items)
		}
	}
	s.mu.Unlock()
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "保存链接状态失败"})
		return
	}
	if !found {
		writeJSON(w, 404, map[string]string{"error": "链接不存在"})
		return
	}
	s.audit("subscription-link-state", fmt.Sprintf("disabled=%t", *body.Disabled), r.PathValue("id"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}
