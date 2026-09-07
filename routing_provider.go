package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The delivery URLs must never be fetched to validate a candidate. Substitute
// the exact already-published raw bytes in a private validation directory.
// Only this disposable configuration uses type:file; delivered URLs stay intact.
func routingLocalValidationConfig(output string, resources map[string]routingRuleResource, dir string) ([]byte, map[string]any, error) {
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(output), &cfg); err != nil {
		return nil, nil, fmt.Errorf("无法读取待校验配置")
	}
	raw, exists := cfg["rule-providers"]
	if !exists {
		return []byte(output), nil, nil
	}
	providers, ok := raw.(map[string]any)
	if !ok || len(providers) == 0 {
		return nil, nil, fmt.Errorf("规则集合结构无效")
	}
	for name, item := range providers {
		id := strings.TrimPrefix(name, "cb-")
		resource, exists := resources[id]
		provider, ok := item.(map[string]any)
		if !exists || !ok || name != routingProviderName(id) || provider["behavior"] != resource.Behavior || len(resource.Content) == 0 || routingSHA256(resource.Content) != resource.SHA256 {
			return nil, nil, fmt.Errorf("规则集合 %s 缺少一致的原始文件", id)
		}
		if !routingHashPattern.MatchString(resource.SHA256) {
			return nil, nil, fmt.Errorf("规则集合摘要无效")
		}
		// The digest, not a user-controlled path, names the temporary file.
		filename := filepath.Join(dir, resource.SHA256+".yaml")
		if err := os.WriteFile(filename, resource.Content, 0600); err != nil {
			return nil, nil, fmt.Errorf("无法写入规则集合校验文件")
		}
		providers[name] = map[string]any{"type": "file", "behavior": resource.Behavior, "format": "yaml", "path": filename}
	}
	cfg["rule-providers"] = providers
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("无法编码规则集合校验配置")
	}
	return encoded, providers, nil
}

// A successful -t can still silently drop malformed classical entries. Start
// a second, deliberately node-free core instance and inspect its provider API.
// It has no proxy listener, DNS, remote resource URLs, or real node credentials.
// The controller is a private Unix socket, so this phase performs no networking.
func routingVerifyLoadedProviders(ctx context.Context, binary, dir string, providers map[string]any, resources map[string]routingRuleResource) error {
	secret, err := routingRandom(24)
	if err != nil {
		return fmt.Errorf("无法创建规则集合校验令牌")
	}
	socket := filepath.Join(dir, "controller.sock")
	rules := []string{}
	expected := map[string]int{}
	for name := range providers {
		id := strings.TrimPrefix(name, "cb-")
		var raw struct {
			Payload []string `yaml:"payload"`
		}
		if yaml.Unmarshal(resources[id].Content, &raw) != nil || len(raw.Payload) == 0 {
			return fmt.Errorf("规则集合 %s 的原始内容无效", id)
		}
		expected[name] = len(raw.Payload)
		rules = append(rules, "RULE-SET,"+name+",DIRECT,no-resolve")
	}
	rules = append(rules, "MATCH,DIRECT")
	cfg := map[string]any{"mode": "rule", "log-level": "silent", "external-controller-unix": socket, "secret": secret, "proxies": []any{}, "proxy-groups": []any{}, "rules": rules, "rule-providers": providers, "dns": map[string]any{"enable": false}, "profile": map[string]any{"store-selected": false}}
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("无法编码集合加载校验配置")
	}
	file := filepath.Join(dir, "provider-check.yaml")
	if err = os.WriteFile(file, encoded, 0600); err != nil {
		return fmt.Errorf("无法写入集合加载校验配置")
	}
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, binary, "-d", dir, "-f", file)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("规则集合加载校验内核无法启动")
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	lastFailure := "集合尚未完成加载"
	for {
		req, _ := http.NewRequestWithContext(runCtx, http.MethodGet, "http://localhost/providers/rules", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		resp, requestErr := client.Do(req)
		if requestErr == nil {
			var loaded struct {
				Providers map[string]struct {
					RuleCount int    `json:"ruleCount"`
					Behavior  string `json:"behavior"`
				} `json:"providers"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&loaded)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decodeErr == nil {
				complete := true
				for name, count := range expected {
					actual, ok := loaded.Providers[name]
					behavior := resources[strings.TrimPrefix(name, "cb-")].Behavior
					if !ok || actual.RuleCount != count || !strings.EqualFold(actual.Behavior, behavior) {
						complete = false
						lastFailure = fmt.Sprintf("%s 实际加载 %d 条，期望 %d 条", name, actual.RuleCount, count)
						break
					}
				}
				if complete {
					return nil
				}
			}
		}
		select {
		case <-runCtx.Done():
			return fmt.Errorf("规则集合加载校验失败：%s；未发布配置", lastFailure)
		case <-ticker.C:
		}
	}
}
