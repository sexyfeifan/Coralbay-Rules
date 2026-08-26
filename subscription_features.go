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
	"regexp"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

type subscriptionRequest struct {
	Target      string `json:"target"`
	URL         string `json:"url"`
	Config      string `json:"config"`
	Filename    string `json:"filename"`
	Include     string `json:"include"`
	Exclude     string `json:"exclude"`
	Rename      string `json:"rename"`
	DeviceID    string `json:"dev_id"`
	SurgeVer    int    `json:"surge_version"`
	Emoji       bool   `json:"emoji"`
	Sort        bool   `json:"sort"`
	Dedup       bool   `json:"dedup"`
	UDP         bool   `json:"udp"`
	XUDP        bool   `json:"xudp"`
	TFO         bool   `json:"tfo"`
	SCV         bool   `json:"scv"`
	TLS13       bool   `json:"tls13"`
	AppendType  bool   `json:"append_type"`
	ListOnly    bool   `json:"list"`
	Insert      bool   `json:"insert"`
	Expand      bool   `json:"expand"`
	NewName     bool   `json:"new_name"`
	FilterNodes bool   `json:"fdn"`
	ClashDoH    bool   `json:"clash_doh"`
	SurgeDoH    bool   `json:"surge_doh"`
	SingboxIPv6 bool   `json:"singbox_ipv6"`
	Interval    int    `json:"interval"`
}

type subscriptionHistoryItem struct {
	ID          string               `json:"id"`
	CreatedAt   time.Time            `json:"created_at"`
	Target      string               `json:"target"`
	Filename    string               `json:"filename"`
	NodeCount   int                  `json:"node_count"`
	SourceCount int                  `json:"source_count"`
	URL         string               `json:"url"`
	Config      string               `json:"config,omitempty"`
	Settings    *subscriptionRequest `json:"settings,omitempty"`
}

func (s *server) subscriptionCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"self_hosted": true,
		"targets": []map[string]any{
			{"id": "clash", "name": "Clash Meta / Mihomo", "modern": true, "vless_reality": true},
			{"id": "stash", "name": "Stash 3.1.1+", "modern": true, "vless_reality": true, "warning": "Reality/Vision 需要 Stash 3.1.1 或更高版本"},
			{"id": "clashr", "name": "ClashR", "modern": false, "vless_reality": false},
			{"id": "singbox", "name": "sing-box", "modern": true, "vless_reality": true},
			{"id": "loon", "name": "Loon", "modern": true, "vless_reality": true},
			{"id": "surge", "name": "Surge", "modern": true, "vless_reality": false, "warning": "Surge 不支持 VLESS Reality"},
			{"id": "surfboard", "name": "Surfboard", "modern": false, "vless_reality": false},
			{"id": "shadowrocket", "name": "Shadowrocket", "modern": true, "vless_reality": true},
			{"id": "quanx", "name": "Quantumult X", "modern": true, "vless_reality": true},
			{"id": "quan", "name": "Quantumult", "modern": false, "vless_reality": false},
			{"id": "v2ray", "name": "V2Ray / Base64", "modern": true, "vless_reality": true},
			{"id": "ss", "name": "Shadowsocks SIP002", "modern": false, "vless_reality": false},
			{"id": "ssr", "name": "ShadowsocksR", "modern": false, "vless_reality": false},
			{"id": "trojan", "name": "Trojan URI", "modern": false, "vless_reality": false},
			{"id": "mixed", "name": "混合 URI", "modern": true, "vless_reality": true},
		},
		"features": []string{"merge", "include", "exclude", "rename", "list", "emoji", "sort", "dedup", "udp", "xudp", "tfo", "scv", "tls13", "fdn", "remote_config", "reverse_parse", "history", "qr"},
		"excluded": []string{"public_backend", "public_short_url", "arbitrary_javascript"},
	})
}

func validateRegexOption(label, value string) error {
	if len(value) > 500 {
		return fmt.Errorf("%s正则过长", label)
	}
	if value != "" {
		if _, err := regexp.Compile(value); err != nil {
			return fmt.Errorf("%s正则无效：%v", label, err)
		}
	}
	return nil
}

func subscriptionParams(body subscriptionRequest) (url.Values, error) {
	if !allowedTargets[body.Target] {
		return nil, fmt.Errorf("转换目标无效")
	}
	if err := validateSubscriptionURLs(body.URL); err != nil {
		return nil, err
	}
	if body.Config != "" && (strings.Contains(body.Config, "|") || validateSubscriptionURLs(body.Config) != nil) {
		return nil, fmt.Errorf("远程配置必须是单个可访问的 HTTP/HTTPS 地址")
	}
	if err := validateRegexOption("包含", body.Include); err != nil {
		return nil, err
	}
	if err := validateRegexOption("排除", body.Exclude); err != nil {
		return nil, err
	}
	if len(body.Rename) > 1000 || strings.ContainsAny(body.Rename, "\r\n") {
		return nil, fmt.Errorf("重命名规则无效")
	}
	if len(body.DeviceID) > 80 || strings.ContainsAny(body.DeviceID, "\r\n&") {
		return nil, fmt.Errorf("设备 ID 无效")
	}
	if body.Interval == 0 {
		body.Interval = 24
	}
	if body.Interval < 1 || body.Interval > 720 {
		return nil, fmt.Errorf("更新间隔必须为 1 到 720 小时")
	}
	if len(body.Filename) > 80 || strings.ContainsAny(body.Filename, "/\\\r\n") {
		return nil, fmt.Errorf("订阅名称无效")
	}
	if body.Target == "surge" && body.SurgeVer != 0 && body.SurgeVer != 2 && body.SurgeVer != 3 && body.SurgeVer != 4 {
		return nil, fmt.Errorf("Surge 版本无效")
	}
	p := url.Values{"target": {body.Target}, "url": {body.URL}}
	bools := map[string]bool{"emoji": body.Emoji, "sort": body.Sort, "dedup": body.Dedup, "udp": body.UDP, "xudp": body.XUDP, "tfo": body.TFO, "scv": body.SCV, "tls13": body.TLS13, "append_type": body.AppendType, "list": body.ListOnly, "insert": body.Insert, "expand": body.Expand, "new_name": body.NewName, "fdn": body.FilterNodes}
	for key, value := range bools {
		p.Set(key, strconv.FormatBool(value))
	}
	p.Set("interval", strconv.Itoa(body.Interval*3600))
	for key, value := range map[string]string{"config": body.Config, "filename": body.Filename, "include": body.Include, "exclude": body.Exclude, "rename": body.Rename, "dev_id": body.DeviceID} {
		if value != "" {
			p.Set(key, value)
		}
	}
	if body.Target == "surge" {
		ver := body.SurgeVer
		if ver == 0 {
			ver = 4
		}
		p.Set("ver", strconv.Itoa(ver))
		if body.SurgeDoH {
			p.Set("surge.doh", "true")
		}
	}
	if (body.Target == "clash" || body.Target == "stash") && body.ClashDoH {
		p.Set("clash.doh", "true")
	}
	if body.Target == "singbox" && body.SingboxIPv6 {
		p.Set("singbox.ipv6", "1")
	}
	return p, nil
}

func (s *server) createSubscriptionLinkV2(w http.ResponseWriter, r *http.Request) {
	var body subscriptionRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求内容无效"})
		return
	}
	params, err := subscriptionParams(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	content, headers, err := s.convertSubscription(r.Context(), params)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	nodes := convertedNodeCount(body.Target, content)
	if nodes == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "转换结果没有可用节点：目标客户端不支持当前订阅中的节点协议。格式转换不能为客户端增加它本身不支持的协议。"})
		return
	}
	canonical := params.Encode()
	link := "https://" + s.domain + "/sub?" + canonical + "&sig=" + url.QueryEscape(s.sign(canonical))
	item := subscriptionHistoryItem{CreatedAt: time.Now().UTC(), Target: body.Target, Filename: body.Filename, NodeCount: nodes, SourceCount: len(strings.Split(body.URL, "|")), URL: link, Config: body.Config, Settings: &body}
	sum := sha256.Sum256([]byte(link + item.CreatedAt.String()))
	item.ID = hex.EncodeToString(sum[:6])
	s.appendSubscriptionHistory(item)
	s.audit("subscription-link", "completed", fmt.Sprintf("%s nodes=%d", body.Target, nodes))
	writeJSON(w, http.StatusOK, map[string]any{"url": link, "node_count": nodes, "content_type": headers.Get("Content-Type"), "validated": true, "history_id": item.ID})
}

func (s *server) historyPath() string {
	return filepath.Join(s.dataDir, "settings", "subscription-history.json")
}

func (s *server) readSubscriptionHistory() []subscriptionHistoryItem {
	content, err := os.ReadFile(s.historyPath())
	if err != nil {
		return []subscriptionHistoryItem{}
	}
	var items []subscriptionHistoryItem
	if json.Unmarshal(content, &items) != nil {
		return []subscriptionHistoryItem{}
	}
	return items
}

func (s *server) appendSubscriptionHistory(item subscriptionHistoryItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.readSubscriptionHistory()
	items = append([]subscriptionHistoryItem{item}, items...)
	if len(items) > 100 {
		items = items[:100]
	}
	_ = os.MkdirAll(filepath.Dir(s.historyPath()), 0700)
	content, _ := json.MarshalIndent(items, "", "  ")
	tmp := s.historyPath() + ".tmp"
	if os.WriteFile(tmp, content, 0600) == nil {
		_ = os.Rename(tmp, s.historyPath())
	}
}

func (s *server) subscriptionHistory(w http.ResponseWriter, _ *http.Request) {
	items := s.readSubscriptionHistory()
	writeJSON(w, http.StatusOK, map[string]any{"count": len(items), "history": items})
}

func (s *server) clearSubscriptionHistory(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	if err := s.preserveHistoryUsage(); err != nil {
		s.mu.Unlock()
		writeJSON(w, 503, map[string]string{"error": "无法保存链接管理信息，未清除历史"})
		return
	}
	err := os.Remove(s.historyPath())
	s.mu.Unlock()
	if err != nil && !os.IsNotExist(err) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "清空历史失败"})
		return
	}
	s.audit("subscription-history", "cleared", "all")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) deleteSubscriptionHistory(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || len(id) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "历史记录 ID 无效"})
		return
	}
	s.mu.Lock()
	if err := s.preserveHistoryUsage(); err != nil {
		s.mu.Unlock()
		writeJSON(w, 503, map[string]string{"error": "无法保存链接管理信息，未删除历史"})
		return
	}
	items := s.readSubscriptionHistory()
	kept := make([]subscriptionHistoryItem, 0, len(items))
	found := false
	for _, item := range items {
		if item.ID == id {
			found = true
			continue
		}
		kept = append(kept, item)
	}
	if found {
		content, _ := json.MarshalIndent(kept, "", "  ")
		tmp := s.historyPath() + ".tmp"
		err := os.WriteFile(tmp, content, 0600)
		if err == nil {
			err = os.Rename(tmp, s.historyPath())
		}
		if err != nil {
			s.mu.Unlock()
			writeJSON(w, 500, map[string]string{"error": "历史记录保存失败，删除未完成"})
			return
		}
	}
	s.mu.Unlock()
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "历史记录不存在"})
		return
	}
	s.audit("subscription-history", "deleted", id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) validateSignedLink(raw string) (url.Values, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() != s.domain || u.Path != "/sub" {
		return nil, fmt.Errorf("只支持本服务生成的订阅链接")
	}
	p := u.Query()
	sig := p.Get("sig")
	p.Del("sig")
	if !secureEqual(sig, s.sign(p.Encode())) || !allowedTargets[p.Get("target")] {
		return nil, fmt.Errorf("订阅签名无效")
	}
	return p, nil
}

func (s *server) parseSubscriptionLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "链接无效"})
		return
	}
	p, err := s.validateSignedLink(body.URL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"params": p, "source_count": len(strings.Split(p.Get("url"), "|"))})
}

func (s *server) subscriptionQRCode(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("url")
	if _, err := s.validateSignedLink(raw); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	png, err := qrcode.Encode(raw, qrcode.Medium, 360)
	if err != nil {
		http.Error(w, "二维码生成失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `inline; filename="CoralBay_Subscription_QR.png"`)
	_, _ = w.Write(png)
}
