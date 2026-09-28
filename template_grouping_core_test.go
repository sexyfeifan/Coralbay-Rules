package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// Opt-in real-core check. Run inside the release image with --network none and
// a read-only rule snapshot. Synthetic nodes are never contacted successfully;
// this verifies actual filters/provider membership, not proxy connectivity.
func TestTemplateGroupingNativeCoreMembership(t *testing.T) {
	root := os.Getenv("MIHOMOPRO_TEST_RELEASE")
	if root == "" {
		t.Skip("requires isolated Linux release image and MIHOMOPRO_TEST_RELEASE")
	}
	input, err := os.ReadFile("templates/openclash/Pro_cn.upstream.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err = yaml.Unmarshal(input, &base); err != nil {
		t.Fatal(err)
	}
	names := []string{"香港01", "日本01", "美国01", "德国01", "越南01", "加拿大01", "巴西01", "南非01", "澳大利亚01", "未标注01"}
	profile := defaultTemplateGroupingProfile()
	profile.Media = []string{"YouTube", "Netflix", "Disney", "Spotify"}
	for _, strategy := range []string{"consistent-hashing", "round-robin", "sticky-sessions"} {
		t.Run(strategy, func(t *testing.T) {
			profile.Strategy = strategy
			cfg, err := applyTemplateGrouping(base, profile, "mihomo", "rules.example.com")
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			for name, raw := range cfg["rule-providers"].(map[string]any) {
				p := raw.(map[string]any)
				address := p["url"].(string)
				i := strings.Index(address, "/mihomo/")
				if i < 0 {
					t.Fatalf("unmapped provider %s", name)
				}
				data, err := os.ReadFile(filepath.Join(root, address[i+1:]))
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, name+".mrs")
				if err = os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				p["type"], p["path"] = "file", path
				delete(p, "url")
			}
			nodes := []any{}
			for _, name := range names {
				nodes = append(nodes, map[string]any{"name": name, "type": "ss", "server": "192.0.2.1", "port": 443, "cipher": "aes-128-gcm", "password": "synthetic-only"})
			}
			nodeFile := filepath.Join(dir, "nodes.yaml")
			data, _ := yaml.Marshal(map[string]any{"proxies": nodes})
			if err = os.WriteFile(nodeFile, data, 0600); err != nil {
				t.Fatal(err)
			}
			delete(cfg, "proxies")
			cfg["proxy-providers"] = map[string]any{"fixture": map[string]any{"type": "file", "path": nodeFile, "health-check": map[string]any{"enable": false}}}
			for _, key := range []string{"port", "socks-port", "redir-port", "mixed-port", "tproxy-port", "external-controller", "external-ui", "external-ui-url", "external-ui-name", "geox-url"} {
				delete(cfg, key)
			}
			for _, key := range []string{"tun", "dns", "sniffer"} {
				cfg[key] = map[string]any{"enable": false}
			}
			cfg["log-level"] = "silent"
			socket := filepath.Join(dir, "controller.sock")
			cfg["external-controller-unix"], cfg["secret"] = socket, "isolated-test"
			data, err = yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "config.yaml")
			if err = os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			const binary = "/usr/local/bin/coralbay-probe-core"
			if output, err := exec.CommandContext(ctx, binary, "-t", "-d", dir, "-f", file).CombinedOutput(); err != nil {
				t.Fatalf("native config rejected: %v %s", err, output)
			}
			cmd := exec.CommandContext(ctx, binary, "-d", dir, "-f", file)
			cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: time.Second}
			read := func(path string, value any) bool {
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost"+path, nil)
				req.Header.Set("Authorization", "Bearer isolated-test")
				resp, err := client.Do(req)
				if err != nil {
					return false
				}
				defer resp.Body.Close()
				return resp.StatusCode == 200 && json.NewDecoder(resp.Body).Decode(value) == nil
			}
			var all struct {
				Proxies map[string]struct {
					All  []string `json:"all"`
					Type string   `json:"type"`
				} `json:"proxies"`
			}
			ready := false
			for ctx.Err() == nil {
				if read("/proxies", &all) && len(all.Proxies["全球手动"].All) >= len(names) {
					ready = true
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !ready {
				t.Fatal("core did not load provider nodes")
			}
			membership := map[string]int{}
			for group, info := range all.Proxies {
				if strings.HasSuffix(group, "手动") && group != "全球手动" {
					for _, node := range info.All {
						if templateGroupingHas(names, node) {
							membership[node]++
						}
					}
				}
			}
			for _, name := range names {
				if membership[name] != 1 {
					t.Errorf("%s belongs to %d regional manual groups", name, membership[name])
				}
				for _, category := range []string{"谷歌服务", "国际媒体", "YouTube", "Netflix", "Disney", "Spotify"} {
					if !templateGroupingHas(all.Proxies[category].All, name) {
						t.Errorf("%s missing direct node %s", category, name)
					}
				}
			}
			for _, name := range []string{"日本自动", "日本均衡", "日本手动"} {
				if !templateGroupingHas(all.Proxies[name].All, "日本01") {
					t.Errorf("real native group %s missing Japanese node", name)
				}
			}
			for _, name := range []string{"台湾自动", "台湾均衡", "台湾手动"} {
				members := all.Proxies[name].All
				if len(members) != 1 || members[0] != "REJECT" {
					t.Errorf("empty region %s must fail closed, got %v", name, members)
				}
			}
			var rules struct {
				Providers map[string]struct {
					RuleCount int `json:"ruleCount"`
				} `json:"providers"`
			}
			if !read("/providers/rules", &rules) || len(rules.Providers) != 33 {
				t.Fatal("core did not load all 33 providers")
			}
			for name, info := range rules.Providers {
				if info.RuleCount < 1 {
					t.Errorf("empty provider %s", name)
				}
			}
			var group struct {
				Type string `json:"type"`
			}
			if !read("/proxies/"+url.PathEscape("日本均衡"), &group) || group.Type != "LoadBalance" {
				t.Fatalf("native balance group type: %s", group.Type)
			}
			t.Logf("real core: %s, %d groups/proxies, 33 loaded providers, 10 nodes each in exactly one regional manual group", strategy, len(all.Proxies))
		})
	}
}
