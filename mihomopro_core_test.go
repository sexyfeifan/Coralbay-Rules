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

// Run the compiled Linux test in a network:none container with a read-only
// complete 666OS release mounted at MIHOMOPRO_TEST_RELEASE. All actual rule
// providers are local MRS, node providers are synthetic files, and the only
// control connection is a private Unix socket. No real proxy is contacted.
func TestMihomoProNativeCoreLoads33AndInjectedNodes(t *testing.T) {
	root := os.Getenv("MIHOMOPRO_TEST_RELEASE")
	if root == "" {
		t.Skip("set MIHOMOPRO_TEST_RELEASE inside the isolated Linux core test environment")
	}
	const binary = "/usr/local/bin/coralbay-probe-core"
	if _, err := os.Stat(binary); err != nil {
		t.Fatal(err)
	}
	input, err := os.ReadFile("templates/openclash/Pro_cn.upstream.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{domain: "rules.example.com"}
	resources := legacyResourceManifest{Status: mirrorStatus{Commit: strings.Repeat("a", 40), ReleaseID: strings.Repeat("a", 40) + "-v414", MirrorDomain: s.domain}}
	for _, source := range []string{"local", "upstream"} {
		t.Run(source, func(t *testing.T) {
			candidate, count, err := s.mihomoProConfig(input, resources, source)
			if err != nil || count != 33 {
				t.Fatalf("variant: %v %d", err, count)
			}
			var cfg map[string]any
			if err = yaml.Unmarshal(candidate, &cfg); err != nil {
				t.Fatal(err)
			}
			providers := cfg["rule-providers"].(map[string]any)
			dir := t.TempDir()
			for _, raw := range providers {
				p := raw.(map[string]any)
				address := p["url"].(string)
				i := strings.Index(address, "/mihomo/")
				if i < 0 {
					t.Fatal("unmapped rule URL")
				}
				path := filepath.Join(root, address[i+1:])
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				path = filepath.Join(dir, strings.ReplaceAll(address[i+1:], "/", "-"))
				if err = os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				p["type"] = "file"
				p["path"] = path
				delete(p, "url")
			}
			nodeFile := filepath.Join(dir, "nodes.yaml")
			if err = os.WriteFile(nodeFile, []byte("proxies:\n - {name: 'Fixture Japan', type: ss, server: 192.0.2.1, port: 443, cipher: aes-128-gcm, password: synthetic}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg["proxy-providers"] = map[string]any{"优质服务商": map[string]any{"type": "file", "path": nodeFile, "health-check": map[string]any{"enable": false}}}
			for _, raw := range cfg["proxy-groups"].([]any) {
				g := raw.(map[string]any)
				g["type"] = "select"
				delete(g, "url")
				delete(g, "interval")
				delete(g, "strategy")
			}
			for _, key := range []string{"port", "socks-port", "redir-port", "mixed-port", "tproxy-port", "external-ui", "external-ui-name", "external-ui-url", "external-controller"} {
				delete(cfg, key)
			}
			cfg["tun"] = map[string]any{"enable": false}
			cfg["dns"] = map[string]any{"enable": false}
			cfg["sniffer"] = map[string]any{"enable": false}
			socket := filepath.Join(dir, "controller.sock")
			cfg["external-controller-unix"] = socket
			cfg["secret"] = "coralbay-isolated-test"
			cfg["log-level"] = "silent"
			candidate, err = yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "config.yaml")
			if err = os.WriteFile(file, candidate, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if out, checkErr := exec.CommandContext(ctx, binary, "-t", "-d", dir, "-f", file).CombinedOutput(); checkErr != nil {
				t.Fatalf("native syntax: %v %s", checkErr, out)
			}
			cmd := exec.CommandContext(ctx, binary, "-d", dir, "-f", file)
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: time.Second}
			read := func(path string, value any) bool {
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost"+path, nil)
				req.Header.Set("Authorization", "Bearer coralbay-isolated-test")
				resp, err := client.Do(req)
				if err != nil {
					return false
				}
				defer resp.Body.Close()
				return resp.StatusCode == 200 && json.NewDecoder(resp.Body).Decode(value) == nil
			}
			total := 0
			ready := false
			for ctx.Err() == nil {
				var loaded struct {
					Providers map[string]struct {
						RuleCount int `json:"ruleCount"`
					} `json:"providers"`
				}
				var group struct {
					All []string `json:"all"`
				}
				if read("/providers/rules", &loaded) && len(loaded.Providers) == 33 && read("/proxies/"+url.PathEscape("全球手动"), &group) {
					ready = true
					total = 0
					for _, p := range loaded.Providers {
						if p.RuleCount < 1 {
							ready = false
						}
						total += p.RuleCount
					}
					hasNode := false
					for _, n := range group.All {
						if n == "Fixture Japan" {
							hasNode = true
						}
					}
					ready = ready && hasNode
					if ready {
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !ready {
				t.Fatal("core did not load all 33 non-empty MRS providers and include injected node in 全球手动")
			}
			t.Logf("%s: actual core loaded 33 MRS providers / %d rules; injected provider node visible in 全球手动", source, total)
		})
	}
}
