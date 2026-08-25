package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var version = "4.8.4"

//go:embed web/*
var webFS embed.FS

//go:embed remote-configs.json
var remoteConfigCatalog []byte

type server struct {
	dataDir         string
	domain          string
	updaterURL      string
	updaterToken    string
	actionToken     string
	adminPassword   string
	subconverterURL string
	interval        time.Duration
	scheduleReset   chan time.Duration
	mu              sync.RWMutex
	syncing         bool
	lastError       string
	logs            []string
	job             syncJob
	latest          releaseInfo
	latestChecked   time.Time
	actionTimes     map[string]time.Time
}

type syncJob struct {
	ID         string    `json:"id"`
	State      string    `json:"state"`
	Stage      string    `json:"stage"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type releaseInfo struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

type persistedSettings struct {
	IntervalSeconds int `json:"interval_seconds"`
}

type clientTemplate struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Extension    string `json:"extension"`
	Enhanced     bool   `json:"enhanced"`
	Capability   string `json:"capability"`
	Description  string `json:"description"`
	PPanelName   string `json:"ppanel_name"`
	UserAgent    string `json:"user_agent"`
	OutputFormat string `json:"output_format"`
	URLScheme    string `json:"url_scheme"`
}

var clientTemplates = []clientTemplate{
	{"clash", "Clash", "yaml", true, "adapted", "Clash YAML：666OS Pro_cn 分组、33 个本地 MRS 规则与本地图标", "Clash", "Clash", "yaml", "clash://install-config?url=${encodeURIComponent(url)}&name=${encodeURIComponent(name)}"},
	{"mihomo", "Mihomo", "yaml", true, "adapted", "Mihomo 原生配置：666OS Pro_cn 分组、MRS 规则与本地图标", "Mihomo", "Mihomo", "yaml", "clash://install-config?url=${encodeURIComponent(url)}&name=${encodeURIComponent(name)}"},
	{"openclash", "OpenClash", "yaml", true, "adapted", "OpenClash 配置：666OS Pro_cn 分组、MRS 规则与本地图标", "OpenClash", "Clash", "yaml", "clash://install-config?url=${encodeURIComponent(url)}&name=${encodeURIComponent(name)}"},
	{"stash", "Stash", "yaml", true, "adapted", "完整改造：666OS Pro_cn 策略分组、地区测速/均衡与 33 个本地 MRS 规则", "Stash", "Stash", "yaml", "stash://install-config?url=${encodeURIComponent(url)}"},
	{"surge", "Surge", "conf", true, "adapted", "已生成 666OS Geo 原生 RULE-SET，并映射 Pro_cn 策略分组", "Surge", "Surge", "conf", "surge:///install-config?url=${encodeURIComponent(url)}"},
	{"loon", "Loon", "conf", true, "adapted", "完整配置：必须作为远程配置导入（不是节点订阅）；包含官方节点语法、Pro_cn 地区筛选/测速组与 666OS 远程规则", "Loon", "Loon", "conf", "loon://import?sub=${encodeURIComponent(url)}"},
	{"shadowrocket", "Shadowrocket", "txt", false, "nodes-only", "官方模板输出节点 URI，不包含策略组和规则路由", "Shadowrocket", "Shadowrocket", "base64", "shadowrocket://add/sub://${window.btoa(url)}?remark=${encodeURIComponent(name)}"},
	{"quantumult-x", "Quantumult X", "conf", false, "nodes-only", "官方模板输出节点资源，不是完整分流配置", "Quantumult X", "Quantumult X", "conf", ""},
	{"quantumult", "Quantumult", "conf", false, "nodes-only", "官方模板仅输出节点，不包含策略组和规则路由", "Quantumult", "Quantumult", "conf", ""},
	{"surfboard", "Surfboard", "conf", true, "adapted", "已生成 Surfboard 原生文本 RULE-SET 与 Pro_cn 策略分组", "Surfboard", "Surfboard", "conf", ""},
	{"egern", "Egern", "yaml", true, "adapted", "已生成 Egern 原生 rule-set 与 Pro_cn 策略分组", "Egern", "Egern", "yaml", "egern:/profiles/new?name=${encodeURIComponent(name)}&url=${encodeURIComponent(url)}"},
	{"hiddify", "Hiddify", "json", false, "converted", "已切换到本地 666OS Source JSON；完整 Pro_cn 分组仍在适配", "Hiddify", "Hiddify", "json", ""},
	{"sing-box-1.11", "sing-box 1.11", "json", false, "converted", "已接入本地 Source JSON，并保留 1.11 节点语法", "sing-box 1.11", "sing-box 1.11", "json", "sing-box://import-remote-profile?url=${encodeURIComponent(url)}#${name}"},
	{"sing-box-1.12", "sing-box 1.12", "json", false, "converted", "已接入本地 Source JSON，并保留 1.12 节点语法", "sing-box 1.12", "sing-box 1.12", "json", "sing-box://import-remote-profile?url=${encodeURIComponent(url)}#${name}"},
	{"sing-box-1.13", "sing-box 1.13", "json", false, "converted", "已接入本地 Source JSON，并保留 1.13 节点语法", "sing-box 1.13", "sing-box 1.13", "json", "sing-box://import-remote-profile?url=${encodeURIComponent(url)}#${name}"},
	{"sing-box-1.14", "sing-box 1.14", "json", false, "converted", "已接入本地 Source JSON，并保留 1.14 节点语法", "sing-box 1.14", "sing-box 1.14", "json", "sing-box://import-remote-profile?url=${encodeURIComponent(url)}#${name}"},
	{"default", "通用订阅", "txt", false, "nodes-only", "通用节点 URI 订阅没有策略组或规则路由，分流改造不适用", "Default", "default", "base64", ""},
}

type mirrorStatus struct {
	OK               bool   `json:"ok"`
	Repository       string `json:"repository"`
	Branch           string `json:"branch"`
	Commit           string `json:"commit"`
	GeoCommit        string `json:"geo_commit"`
	ReleaseID        string `json:"release_id"`
	GeneratorVersion string `json:"generator_version"`
	SyncedAt         string `json:"synced_at"`
	ValidatedFiles   int    `json:"validated_files"`
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
		dataDir:         dataDir,
		domain:          env("MIRROR_DOMAIN", "rules.coralbay.top"),
		updaterURL:      env("UPDATER_URL", "http://updater:8080/v1/update"),
		updaterToken:    os.Getenv("UPDATER_TOKEN"),
		actionToken:     os.Getenv("ADMIN_ACTION_TOKEN"),
		adminPassword:   env("ADMIN_PASSWORD", "sexyfeifan"),
		subconverterURL: env("SUBCONVERTER_URL", "http://subconverter:25500"),
		interval:        time.Duration(intervalSeconds) * time.Second,
		scheduleReset:   make(chan time.Duration, 1),
		actionTimes:     make(map[string]time.Time),
	}
	if content, err := os.ReadFile(filepath.Join(dataDir, "activity.log")); err == nil {
		lines := strings.Split(strings.TrimSpace(string(content)), "\n")
		if len(lines) > 300 {
			lines = lines[len(lines)-300:]
		}
		s.logs = lines
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/session", s.sessionStatus)
	mux.HandleFunc("GET /api/public/status", s.auth(s.publicStatus))
	mux.HandleFunc("GET /api/public/rules", s.auth(s.ruleCatalog))
	mux.HandleFunc("GET /api/public/rule-details", s.auth(s.ruleDetails))
	mux.HandleFunc("GET /api/public/templates", s.auth(s.templateCatalog))
	mux.HandleFunc("GET /api/public/conversions", s.auth(s.conversionCatalog))
	mux.HandleFunc("GET /api/public/native-rules", s.auth(s.nativeRuleCatalog))
	mux.HandleFunc("GET /api/public/diagnostics", s.auth(s.publicDiagnostics))
	mux.HandleFunc("GET /api/admin/status", s.auth(s.adminStatus))
	mux.HandleFunc("GET /api/admin/job", s.auth(s.currentJob))
	mux.HandleFunc("POST /api/admin/sync", s.auth(s.syncNow))
	mux.HandleFunc("GET /api/admin/logs", s.auth(s.getLogs))
	mux.HandleFunc("GET /api/admin/releases", s.auth(s.releases))
	mux.HandleFunc("POST /api/admin/rollback", s.auth(s.rollback))
	mux.HandleFunc("PUT /api/admin/settings", s.auth(s.updateSettings))
	mux.HandleFunc("POST /api/admin/update", s.auth(s.updateContainer))
	mux.HandleFunc("GET /api/admin/audit", s.auth(s.getAudit))
	mux.HandleFunc("GET /api/admin/subconverter/status", s.auth(s.subconverterStatus))
	mux.HandleFunc("POST /api/admin/subscription-link", s.auth(s.createSubscriptionLinkV2))
	mux.HandleFunc("GET /api/admin/subscription-presets", s.auth(s.subscriptionPresets))
	mux.HandleFunc("POST /api/admin/subscription-presets/sync", s.auth(s.syncSubscriptionPresets))
	mux.HandleFunc("GET /api/admin/subscription-capabilities", s.auth(s.subscriptionCapabilities))
	mux.HandleFunc("GET /api/admin/subscription-history", s.auth(s.subscriptionHistory))
	mux.HandleFunc("DELETE /api/admin/subscription-history", s.auth(s.clearSubscriptionHistory))
	mux.HandleFunc("DELETE /api/admin/subscription-history/{id}", s.auth(s.deleteSubscriptionHistory))
	mux.HandleFunc("POST /api/admin/subscription-parse", s.auth(s.parseSubscriptionLink))
	mux.HandleFunc("GET /api/admin/subscription-qr", s.auth(s.subscriptionQRCode))
	mux.HandleFunc("GET /sub", s.signedSubscription)
	mux.HandleFunc("GET /_configs/{file}", s.remoteConfigFile)
	mux.HandleFunc("GET /downloads/CoralBay_OpenClash_PPanel_Template.yaml", s.downloadTemplate)
	mux.HandleFunc("GET /downloads/templates/{client}", s.downloadClientTemplate)
	mux.HandleFunc("GET /admin/", s.redirectRoot)
	mux.HandleFunc("GET /", s.publicFiles)

	go s.scheduler()
	go s.refreshRemoteConfigs(context.Background())
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
		policy, rules, validation := "无", "无", "基础检查"
		switch client.Capability {
		case "adapted":
			policy, rules, validation = "Pro_cn 已映射", "本地规则镜像", "结构校验通过"
		case "converted":
			policy, rules, validation = "基础分组", "本地 Source JSON", "JSON 源校验通过"
		}
		items = append(items, map[string]any{
			"id": client.ID, "name": client.Name, "extension": client.Extension,
			"enhanced": client.Enhanced, "capability": client.Capability, "description": client.Description,
			"ppanel_name": client.PPanelName, "user_agent": client.UserAgent,
			"output_format": client.OutputFormat, "url_scheme": client.URLScheme,
			"online_url":            "https://" + s.domain + "/_templates/clients/" + client.ID + ".gotmpl",
			"download_url":          "/downloads/templates/" + client.ID,
			"original_url":          "https://" + s.domain + "/_templates/clients/original/" + client.ID + ".gotmpl",
			"original_download_url": "/downloads/templates/" + client.ID + "?variant=original",
			"node_rendering":        true, "policy_groups": policy, "rule_sources": rules,
			"validation": validation,
		})
	}
	writeJSON(w, 200, map[string]any{"templates": items, "count": len(items)})
}

func (s *server) conversionCatalog(w http.ResponseWriter, _ *http.Request) {
	content, err := os.ReadFile(filepath.Join(s.dataDir, "current", "_converted", "manifest.json"))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "转换产物尚未生成，请先同步规则"})
		return
	}
	var manifest struct {
		Count int `json:"count"`
		Sets  []struct {
			ID            string `json:"id"`
			Kind          string `json:"kind"`
			Source        string `json:"source"`
			Entries       int    `json:"entries"`
			List          string `json:"list"`
			SingBox       string `json:"sing_box"`
			ListSHA256    string `json:"list_sha256"`
			SingBoxSHA256 string `json:"sing_box_sha256"`
		} `json:"sets"`
	}
	if json.Unmarshal(content, &manifest) != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "转换清单格式无效"})
		return
	}
	items := make([]map[string]any, 0, len(manifest.Sets))
	for _, item := range manifest.Sets {
		items = append(items, map[string]any{
			"id": item.ID, "kind": item.Kind, "source": item.Source, "entries": item.Entries,
			"list_url":    "https://" + s.domain + "/_converted/" + item.List,
			"singbox_url": "https://" + s.domain + "/_converted/" + item.SingBox,
			"list_sha256": item.ListSHA256, "singbox_sha256": item.SingBoxSHA256,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(items), "sets": items})
}

func (s *server) nativeRuleCatalog(w http.ResponseWriter, _ *http.Request) {
	type item struct {
		Platform string `json:"platform"`
		Path     string `json:"path"`
		Format   string `json:"format"`
		URL      string `json:"url"`
		Bytes    int64  `json:"bytes"`
	}
	items := make([]item, 0, 256)
	for _, platform := range []string{"mihomo", "singbox", "surge"} {
		root := filepath.Join(s.dataDir, "current", platform)
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(filepath.Join(s.dataDir, "current"), path)
			if relErr != nil {
				return nil
			}
			info, statErr := entry.Info()
			if statErr != nil {
				return nil
			}
			ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
			items = append(items, item{Platform: platform, Path: filepath.ToSlash(rel), Format: ext, URL: "https://" + s.domain + "/" + filepath.ToSlash(rel), Bytes: info.Size()})
			return nil
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(items), "rules": items})
}

var allowedTargets = map[string]bool{"clash": true, "stash": true, "clashr": true, "singbox": true, "surge": true, "surfboard": true, "shadowrocket": true, "quan": true, "quanx": true, "loon": true, "v2ray": true, "ss": true, "ssr": true, "trojan": true, "mixed": true}

func validateSubscriptionURLs(raw string) error {
	parts := strings.Split(raw, "|")
	if len(parts) == 0 || len(parts) > 5 {
		return fmt.Errorf("订阅地址数量必须为 1 到 5 个")
	}
	for _, value := range parts {
		u, err := url.Parse(strings.TrimSpace(value))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
			return fmt.Errorf("仅支持 HTTP/HTTPS 订阅地址")
		}
		ips, err := net.LookupIP(u.Hostname())
		if err != nil || len(ips) == 0 {
			return fmt.Errorf("订阅域名无法解析")
		}
		for _, ip := range ips {
			if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
				return fmt.Errorf("不允许访问内网订阅地址")
			}
		}
	}
	return nil
}

func (s *server) createSubscriptionLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target     string `json:"target"`
		URL        string `json:"url"`
		Config     string `json:"config"`
		Filename   string `json:"filename"`
		Emoji      bool   `json:"emoji"`
		Sort       bool   `json:"sort"`
		Dedup      bool   `json:"dedup"`
		UDP        bool   `json:"udp"`
		TFO        bool   `json:"tfo"`
		SCV        bool   `json:"scv"`
		AppendType bool   `json:"append_type"`
		Interval   int    `json:"interval"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&body) != nil || !allowedTargets[body.Target] {
		writeJSON(w, 400, map[string]string{"error": "转换目标无效"})
		return
	}
	if err := validateSubscriptionURLs(body.URL); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if body.Config != "" {
		if strings.Contains(body.Config, "|") || validateSubscriptionURLs(body.Config) != nil {
			writeJSON(w, 400, map[string]string{"error": "远程配置必须是单个可访问的 HTTP/HTTPS 地址"})
			return
		}
	}
	if body.Interval == 0 {
		body.Interval = 24
	}
	if body.Interval < 1 || body.Interval > 720 {
		writeJSON(w, 400, map[string]string{"error": "更新间隔必须为 1 到 720 小时"})
		return
	}
	if len(body.Filename) > 80 || strings.ContainsAny(body.Filename, "/\\\r\n") {
		writeJSON(w, 400, map[string]string{"error": "订阅名称无效"})
		return
	}
	params := url.Values{"target": {body.Target}, "url": {body.URL}}
	params.Set("emoji", strconv.FormatBool(body.Emoji))
	params.Set("sort", strconv.FormatBool(body.Sort))
	params.Set("dedup", strconv.FormatBool(body.Dedup))
	params.Set("udp", strconv.FormatBool(body.UDP))
	params.Set("tfo", strconv.FormatBool(body.TFO))
	params.Set("scv", strconv.FormatBool(body.SCV))
	params.Set("append_type", strconv.FormatBool(body.AppendType))
	params.Set("interval", strconv.Itoa(body.Interval))
	if body.Config != "" {
		params.Set("config", body.Config)
	}
	if body.Filename != "" {
		params.Set("filename", body.Filename)
	}
	content, headers, err := s.convertSubscription(r.Context(), params)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	nodes := convertedNodeCount(body.Target, content)
	if nodes == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "转换结果没有可用节点：目标客户端不支持当前订阅中的节点协议。VLESS Reality 不能输出到 Surge，请改用 Clash/Mihomo、sing-box、Shadowrocket、Quantumult X、Loon 或 V2Ray。"})
		return
	}
	canonical := params.Encode()
	link := "https://" + s.domain + "/sub?" + canonical + "&sig=" + url.QueryEscape(s.sign(canonical))
	s.audit("subscription-link", "completed", fmt.Sprintf("%s nodes=%d", body.Target, nodes))
	writeJSON(w, http.StatusOK, map[string]any{"url": link, "node_count": nodes, "content_type": headers.Get("Content-Type"), "validated": true})
}

func (s *server) convertSubscription(ctx context.Context, params url.Values) ([]byte, http.Header, error) {
	upstreamParams := cloneURLValues(params)
	if upstreamParams.Get("target") == "stash" {
		// subconverter has no Stash target. Stash consumes Clash.Meta YAML;
		// retain "stash" in the signed public URL while using clash upstream.
		upstreamParams.Set("target", "clash")
	}
	upstream := strings.TrimRight(s.subconverterURL, "/") + "/sub?" + upstreamParams.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("无法创建转换请求")
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("转换后端不可用：%v", err)
	}
	defer resp.Body.Close()
	content, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if readErr != nil {
		return nil, nil, fmt.Errorf("读取转换结果失败")
	}
	if resp.StatusCode != http.StatusOK {
		detail := strings.TrimSpace(string(content))
		if len(detail) > 240 {
			detail = detail[:240]
		}
		if detail == "" {
			detail = resp.Status
		}
		return nil, nil, fmt.Errorf("转换后端返回错误：%s", detail)
	}
	if params.Get("target") == "stash" {
		content = transformStashSubscription(content, "https://"+s.domain+"/_assets/icons/")
	}
	return content, resp.Header.Clone(), nil
}

func cloneURLValues(source url.Values) url.Values {
	cloned := make(url.Values, len(source))
	for key, values := range source {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func (s *server) subconverterStatus(w http.ResponseWriter, r *http.Request) {
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(s.subconverterURL, "/")+"/version", nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	content, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	writeJSON(w, http.StatusOK, map[string]any{"ok": resp.StatusCode == http.StatusOK, "version": strings.TrimSpace(string(content)), "self_hosted": true, "supports_modern_protocols": true})
}

func convertedNodeCount(target string, content []byte) int {
	text := string(content)
	section := ""
	wanted := map[string]string{"surge": "Proxy", "surfboard": "Proxy", "shadowrocket": "Proxy", "loon": "Proxy", "quan": "server_local", "quanx": "server_local"}[target]
	if wanted != "" {
		count := 0
		for _, raw := range strings.Split(text, "\n") {
			line := strings.TrimSpace(raw)
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				section = strings.Trim(line, "[]")
				continue
			}
			if section == wanted && line != "" && !strings.HasPrefix(line, "#") && strings.Contains(line, "=") {
				count++
			}
		}
		return count
	}
	if target == "clash" || target == "stash" || target == "clashr" {
		inProxies, count := false, 0
		for _, line := range strings.Split(text, "\n") {
			if line == "proxies:" {
				inProxies = true
				continue
			}
			if inProxies && len(line) > 0 && line[0] != ' ' {
				break
			}
			if inProxies && strings.HasPrefix(strings.TrimSpace(line), "type:") {
				count++
			}
		}
		return count
	}
	if target == "singbox" {
		var doc struct {
			Outbounds []struct {
				Type string `json:"type"`
				Tag  string `json:"tag"`
			} `json:"outbounds"`
		}
		if json.Unmarshal(content, &doc) != nil {
			return 0
		}
		count := 0
		for _, outbound := range doc.Outbounds {
			if !map[string]bool{"direct": true, "block": true, "dns": true, "selector": true, "urltest": true}[outbound.Type] {
				count++
			}
		}
		return count
	}
	if target == "v2ray" || target == "ss" || target == "ssr" || target == "trojan" || target == "mixed" {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
		if err != nil {
			return 0
		}
		count := 0
		for _, line := range strings.Split(string(decoded), "\n") {
			if strings.Contains(line, "://") {
				count++
			}
		}
		return count
	}
	return 0
}

func (s *server) signedSubscription(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	signature := params.Get("sig")
	params.Del("sig")
	target, source := params.Get("target"), params.Get("url")
	if !allowedTargets[target] || !secureEqual(signature, s.sign(params.Encode())) || validateSubscriptionURLs(source) != nil {
		http.Error(w, "invalid subscription link", http.StatusForbidden)
		return
	}
	content, headers, err := s.convertSubscription(r.Context(), params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if convertedNodeCount(target, content) == 0 {
		http.Error(w, "转换结果没有目标客户端可用的节点", http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", headers.Get("Content-Type"))
	for _, header := range []string{"Subscription-Userinfo", "Profile-Update-Interval"} {
		if value := headers.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	extension := "txt"
	if target == "clash" || target == "stash" || target == "clashr" {
		extension = "yaml"
	}
	if target == "surge" || target == "surfboard" || target == "shadowrocket" || target == "quan" || target == "quanx" || target == "loon" {
		extension = "conf"
	}
	if target == "singbox" {
		extension = "json"
	}
	w.Header().Set("Content-Disposition", `attachment; filename="CoralBay_Subscription_`+target+`.`+extension+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(content)
}

func (s *server) publicDiagnostics(w http.ResponseWriter, _ *http.Request) {
	status, statusErr := s.readStatus()
	manifestPath := filepath.Join(s.dataDir, "current", "_converted", "manifest.json")
	content, manifestErr := os.ReadFile(manifestPath)
	var manifest struct {
		Sets []struct {
			Entries int `json:"entries"`
		} `json:"sets"`
	}
	if manifestErr == nil {
		manifestErr = json.Unmarshal(content, &manifest)
	}
	real, empty := 0, 0
	for _, item := range manifest.Sets {
		if item.Entries == 0 {
			empty++
		} else {
			real++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      statusErr == nil && status.OK && manifestErr == nil,
		"release": map[string]any{"id": status.ReleaseID, "commit": status.Commit, "geo_commit": status.GeoCommit, "generator_version": status.GeneratorVersion},
		"rules":   map[string]any{"validated_mrs": status.ValidatedFiles, "converted_real": real, "safe_empty": empty, "total": real + empty},
		"icons":   s.iconStatus(), "status_error": errorText(statusErr), "manifest_error": errorText(manifestErr),
	})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 || pageSize > 500 {
		pageSize = 200
	}
	entries := make([]string, 0)
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && (query == "" || strings.Contains(strings.ToLower(line), query)) {
			entries = append(entries, line)
		}
	}
	total := len(entries)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	writeJSON(w, 200, map[string]any{
		"path": relative, "source_path": source,
		"source_url": "https://github.com/666OS/rules/blob/geo/" + source,
		"count":      total, "page": page, "page_size": pageSize, "entries": entries[start:end],
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
	certificateChannel := make(chan map[string]any, 1)
	go func() { certificateChannel <- certificateStatus(s.domain) }()
	icons := s.iconStatus()
	release := s.cachedLatestVersion()
	cert := <-certificateChannel
	s.mu.RLock()
	response := map[string]any{
		"version": version, "domain": s.domain, "syncing": s.syncing,
		"last_error": s.lastError, "interval_seconds": int(s.interval.Seconds()),
		"status": status, "latest_version": release.Version, "update_available": newerVersion(release.Version, "v"+version),
		"release_url": release.URL, "certificate": cert, "icons": icons,
		"template_url": "https://" + s.domain + "/_templates/ppanel_openclash_pro_cn.gotmpl",
		"job":          s.job, "next_sync_at": time.Now().Add(s.interval),
		"security": map[string]any{"password_login": true, "session_hours": 12, "public_console": false},
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
		if !s.validSession(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "登录已失效，请重新登录"})
			return
		}
		if r.Method != http.MethodGet {
			if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+s.domain && origin != "http://"+r.Host {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "请求来源不受信任"})
				return
			}
			if !s.allowAction(r.RemoteAddr) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "操作过于频繁，请稍后再试"})
				return
			}
		}
		next(w, r)
	}
}

func (s *server) sessionKey() []byte { return []byte(s.actionToken + "\x00" + s.adminPassword) }

func (s *server) sign(value string) string {
	mac := hmac.New(sha256.New, s.sessionKey())
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *server) validSession(r *http.Request) bool {
	cookie, err := r.Cookie("coralbay_session")
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 || !secureEqual(parts[1], s.sign(parts[0])) {
		return false
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	return err == nil && time.Now().Unix() < expires
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if !s.allowAction("login:" + r.RemoteAddr) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "请稍后再试"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&body) != nil || !secureEqual(body.Password, s.adminPassword) {
		s.audit("login", "failed", r.RemoteAddr)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "密码错误"})
		return
	}
	expires := strconv.FormatInt(time.Now().Add(12*time.Hour).Unix(), 10)
	http.SetCookie(w, &http.Cookie{Name: "coralbay_session", Value: expires + "." + s.sign(expires), Path: "/", MaxAge: 43200, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	s.audit("login", "completed", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "coralbay_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) sessionStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": s.validSession(r)})
}

func (s *server) allowAction(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.actionTimes == nil {
		s.actionTimes = make(map[string]time.Time)
	}
	if previous := s.actionTimes[host]; !previous.IsZero() && now.Sub(previous) < time.Second {
		return false
	}
	s.actionTimes[host] = now
	if len(s.actionTimes) > 1024 {
		for key, seen := range s.actionTimes {
			if now.Sub(seen) > time.Hour {
				delete(s.actionTimes, key)
			}
		}
	}
	return true
}

func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *server) currentJob(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	job := s.job
	s.mu.RUnlock()
	writeJSON(w, 200, job)
}

func (s *server) audit(action, result, detail string) {
	entry := map[string]any{"time": time.Now().UTC(), "action": action, "result": result, "detail": detail}
	content, _ := json.Marshal(entry)
	path := filepath.Join(s.dataDir, "audit.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		defer file.Close()
		_, _ = file.Write(append(content, '\n'))
	}
}

func (s *server) getAudit(w http.ResponseWriter, _ *http.Request) {
	content, _ := os.ReadFile(filepath.Join(s.dataDir, "audit.jsonl"))
	lines := make([]string, 0)
	if trimmed := strings.TrimSpace(string(content)); trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	writeJSON(w, 200, map[string]any{"entries": lines})
}

func (s *server) cachedLatestVersion() releaseInfo {
	s.mu.RLock()
	cached, checked := s.latest, s.latestChecked
	s.mu.RUnlock()
	if cached.Version != "" && time.Since(checked) < 30*time.Minute {
		return cached
	}
	latest, url := latestVersion()
	if latest == "" {
		return cached
	}
	result := releaseInfo{Version: latest, URL: url}
	s.mu.Lock()
	s.latest, s.latestChecked = result, time.Now()
	s.mu.Unlock()
	return result
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
	s.job = syncJob{ID: fmt.Sprintf("sync-%d", time.Now().Unix()), State: "running", Stage: "准备同步", StartedAt: time.Now().UTC()}
	s.mu.Unlock()
	s.audit("sync", "started", s.job.ID)
	go func() {
		cmd := exec.Command("/usr/local/bin/coralbay-rules-sync", "once")
		cmd.Env = os.Environ()
		reader, writer := io.Pipe()
		cmd.Stdout, cmd.Stderr = writer, writer
		startErr := cmd.Start()
		var err error
		if startErr == nil {
			wait := make(chan error, 1)
			go func() { wait <- cmd.Wait(); _ = writer.Close() }()
			scanner := bufio.NewScanner(reader)
			for scanner.Scan() {
				line := scanner.Text()
				s.addLog(line)
				s.mu.Lock()
				s.job.Stage = syncStage(line)
				s.mu.Unlock()
			}
			err = <-wait
		} else {
			err = startErr
		}
		_ = writer.Close()
		_ = reader.Close()
		s.mu.Lock()
		s.syncing = false
		s.job.FinishedAt = time.Now().UTC()
		if err != nil {
			s.lastError = err.Error()
			s.job.State, s.job.Error = "failed", err.Error()
			s.addLogLocked("同步失败: " + err.Error())
		} else {
			s.job.State, s.job.Stage = "completed", "发布完成"
		}
		s.mu.Unlock()
		if err != nil {
			s.audit("sync", "failed", err.Error())
		} else {
			s.audit("sync", "completed", "")
		}
	}()
	return true
}

func syncStage(line string) string {
	stages := []string{"同步完成", "发布", "校验", "可读规则源", "生成跨客户端", "生成客户端模板", "开始同步"}
	for _, stage := range stages {
		if strings.Contains(line, stage) {
			return stage
		}
	}
	return "处理中"
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
	s.audit("settings", "completed", interval.String())
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) updateContainer(w http.ResponseWriter, _ *http.Request) {
	if s.updaterToken == "" {
		writeJSON(w, 503, map[string]string{"error": "专用更新器尚未配置，请先重新运行安装脚本"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	s.audit("update", "started", "container")
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
	formatted := time.Now().Format("2006-01-02 15:04:05") + " " + line
	s.logs = append(s.logs, formatted)
	if len(s.logs) > 300 {
		s.logs = s.logs[len(s.logs)-300:]
	}
	file, err := os.OpenFile(filepath.Join(s.dataDir, "activity.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		_, _ = file.WriteString(formatted + "\n")
		_ = file.Close()
	}
}

func (s *server) getLogs(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	logs := append([]string(nil), s.logs...)
	s.mu.RUnlock()
	writeJSON(w, 200, map[string]any{"logs": logs})
}

var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}(?:-[a-f0-9]{12}-v[0-9]+\.[0-9]+\.[0-9]+-[a-f0-9]{12})?$`)

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
		items = append(items, map[string]any{"commit": entry.Name(), "release_id": entry.Name(), "active": path == current, "modified": info.ModTime()})
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
	s.audit("rollback", "completed", body.Commit)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) adminPage(w http.ResponseWriter, r *http.Request) {
	if !s.validSession(r) {
		content, _ := fs.ReadFile(webFS, "web/login.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(content)
		return
	}
	content, _ := fs.ReadFile(webFS, "web/admin.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
	w.Write(content)
}

func (s *server) redirectRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusMovedPermanently)
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
	variant := "adapted"
	pathParts := []string{s.dataDir, "current", "_templates", "clients", selected.ID + ".gotmpl"}
	if r.URL.Query().Get("variant") == "original" {
		variant = "original"
		pathParts = []string{s.dataDir, "current", "_templates", "clients", "original", selected.ID + ".gotmpl"}
	}
	path := filepath.Join(pathParts...)
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
	filename := "CoralBay_PPanel_" + strings.ReplaceAll(selected.ID, "-", "_") + "_" + variant + "." + selected.Extension
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(content)
}

func (s *server) publicFiles(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/assets/style.css" || r.URL.Path == "/assets/app.js" || r.URL.Path == "/assets/login.js" || r.URL.Path == "/assets/icon.svg" {
		name := "web/" + strings.TrimPrefix(r.URL.Path, "/assets/")
		content, err := fs.ReadFile(webFS, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(name, ".css") {
			w.Header().Set("Content-Type", "text/css")
		} else if strings.HasSuffix(name, ".svg") {
			w.Header().Set("Content-Type", "image/svg+xml")
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
		s.adminPage(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/mihomo/") || strings.HasPrefix(r.URL.Path, "/singbox/") || strings.HasPrefix(r.URL.Path, "/surge/") || strings.HasPrefix(r.URL.Path, "/_templates/") || strings.HasPrefix(r.URL.Path, "/_assets/") || strings.HasPrefix(r.URL.Path, "/_converted/") || r.URL.Path == "/_mirror/status.json" {
		switch {
		case r.URL.Path == "/_mirror/status.json":
			w.Header().Set("Cache-Control", "no-store")
		case strings.HasPrefix(r.URL.Path, "/_templates/"):
			w.Header().Set("Cache-Control", "public, max-age=300")
		default:
			w.Header().Set("Cache-Control", "public, max-age=3600, stale-if-error=86400")
		}
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
