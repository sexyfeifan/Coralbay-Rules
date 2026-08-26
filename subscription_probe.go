package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// A diagnostic config contains one node, not an untrusted full profile. No
// scripts, remote providers, TUN, controller, plugins or user-selected targets.
func probeNode(ctx context.Context, node map[string]any) (map[string]any, error) {
	allowed := strings.Fields("name type server port uuid password cipher alterId tls servername sni network flow encryption client-fingerprint fingerprint reality-opts ws-opts grpc-opts h2-opts http-opts skip-cert-verify udp alpn packet-encoding ip-version")
	fields := map[string]bool{}
	for _, k := range allowed {
		fields[k] = true
	}
	for k := range node {
		if !fields[k] {
			return nil, fmt.Errorf("诊断暂不支持字段 %s；未修改转换输出", k)
		}
	}
	switch node["type"] {
	case "vless", "vmess", "ss", "trojan":
	default:
		return nil, fmt.Errorf("诊断仅支持 VLESS/VMess/SS/Trojan")
	}
	host, ok := node["server"].(string)
	if !ok || host == "" {
		return nil, fmt.Errorf("缺少服务器地址")
	}
	ips, e := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if e != nil || len(ips) == 0 {
		return nil, fmt.Errorf("节点地址解析失败")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, fmt.Errorf("诊断禁止非公网节点")
		}
	}
	result := map[string]any{}
	for k, v := range node {
		result[k] = v
	}
	// Preserve TLS name when pinning the actual connection destination.
	if net.ParseIP(host) == nil {
		if _, ok := result["servername"]; !ok {
			result["servername"] = host
		}
		if _, ok := result["sni"]; !ok {
			result["sni"] = host
		}
	}
	result["server"] = ips[0].String()
	result["name"] = "probe"
	return result, nil
}
func runNodeProbe(ctx context.Context, node map[string]any) (int64, error) {
	safe, e := probeNode(ctx, node)
	if e != nil {
		return 0, e
	}
	dir, e := os.MkdirTemp("", "coralbay-probe-")
	if e != nil {
		return 0, fmt.Errorf("无法创建诊断目录")
	}
	defer os.RemoveAll(dir)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return 0, e
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	cfg := map[string]any{"mixed-port": port, "bind-address": "127.0.0.1", "allow-lan": false, "mode": "rule", "log-level": "silent", "ipv6": true, "proxies": []any{safe}, "rules": []string{"MATCH,probe"}, "dns": map[string]any{"enable": false}, "profile": map[string]any{"store-selected": false}}
	b, e := yaml.Marshal(cfg)
	if e != nil {
		return 0, e
	}
	file := filepath.Join(dir, "config.yaml")
	if e = os.WriteFile(file, b, 0600); e != nil {
		return 0, e
	}
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/usr/local/bin/coralbay-probe-core", "-d", dir, "-f", file)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		return 0, fmt.Errorf("诊断内核不可用，请升级完整镜像")
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ready := false
	for i := 0; i < 30; i++ {
		c, e := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if e == nil {
			c.Close()
			ready = true
			break
		}
		select {
		case <-runCtx.Done():
			return 0, fmt.Errorf("诊断超时")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ready {
		return 0, fmt.Errorf("诊断内核未就绪，节点参数可能不兼容")
	}
	proxy, _ := url.Parse("http://" + addr)
	client := &http.Client{Timeout: 12 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(runCtx, "GET", "https://www.gstatic.com/generate_204", nil)
	start := time.Now()
	resp, e := client.Do(req)
	if e != nil {
		return 0, fmt.Errorf("HTTPS 经节点请求失败或超时（详情未包含节点凭据）")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return 0, fmt.Errorf("HTTPS 测试返回 %d，预期 204", resp.StatusCode)
	}
	return time.Since(start).Milliseconds(), nil
}
func (s *server) probeSubscription(w http.ResponseWriter, r *http.Request) {
	if !s.probeMu.TryLock() {
		writeJSON(w, 429, map[string]string{"error": "已有诊断正在进行，请稍后"})
		return
	}
	defer s.probeMu.Unlock()
	var body struct {
		URL string `json:"url"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&body) != nil {
		writeJSON(w, 400, map[string]string{"error": "请求无效"})
		return
	}
	p, e := s.validateSignedLink(body.URL)
	if e != nil {
		writeJSON(w, 400, map[string]string{"error": "需要本站有效的订阅链接"})
		return
	}
	target := p.Get("target")
	if target != "stash" && target != "clash" {
		writeJSON(w, 400, map[string]string{"error": "服务器诊断目前支持 Stash / Clash YAML"})
		return
	}
	s.mu.Lock()
	db, e := s.openUsageDB()
	s.mu.Unlock()
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "数据库不可用"})
		return
	}
	var disabled bool
	e = db.QueryRow("SELECT disabled FROM links WHERE id=?", usageID(body.URL)).Scan(&disabled)
	if e != nil && e != sql.ErrNoRows {
		writeJSON(w, 503, map[string]string{"error": "链接状态无法读取"})
		return
	}
	if disabled {
		writeJSON(w, 410, map[string]string{"error": "链接已停用"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	data, _, e := s.convertSubscription(ctx, p)
	if e != nil {
		writeJSON(w, 422, map[string]string{"error": e.Error()})
		return
	}
	var cfg struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if yaml.Unmarshal(data, &cfg) != nil || len(cfg.Proxies) == 0 {
		writeJSON(w, 422, map[string]string{"error": "没有可诊断的内联节点"})
		return
	}
	results := []map[string]any{}
	for i, node := range cfg.Proxies {
		if i >= 3 {
			break
		}
		delay, e := runNodeProbe(ctx, node)
		item := map[string]any{"name": node["name"], "ok": e == nil, "delay_ms": delay}
		if e != nil {
			item["error"] = e.Error()
		}
		results = append(results, item)
	}
	writeJSON(w, 200, map[string]any{"results": results, "scope": "服务器经 Mihomo 内核抽测前 3 个节点至 Google HTTPS 204；不是手机 Stash 实机认证，诊断不修改订阅且不计入拉取统计"})
}
