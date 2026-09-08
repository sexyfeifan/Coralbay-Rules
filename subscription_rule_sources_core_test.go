package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// This opt-in test needs an isolated Docker network, a real subconverter whose
// SUBNG_PROXY and HTTP(S)_PROXY point to http://coralbay:<token>@fixture:18080,
// and this Linux test binary in a container named fixture with the probe core.
// Mount a complete release read-only at ORDINARY_SOURCE_TEST_RELEASE and the
// converter's default DNS GeoIP database at ORDINARY_SOURCE_TEST_MMDB. Only
// synthetic nodes are supplied; all upstream rule requests are pinned fixtures.
func TestOrdinarySourcesRealBackendAndCore(t *testing.T) {
	release, backend := os.Getenv("ORDINARY_SOURCE_TEST_RELEASE"), os.Getenv("ORDINARY_SOURCE_TEST_BACKEND")
	if release == "" || backend == "" {
		t.Skip("set ORDINARY_SOURCE_TEST_RELEASE and ORDINARY_SOURCE_TEST_BACKEND in the isolated Linux integration environment")
	}
	token := os.Getenv("ORDINARY_SOURCE_TEST_TOKEN")
	if token == "" {
		t.Fatal("ORDINARY_SOURCE_TEST_TOKEN is required")
	}
	dataDir := t.TempDir()
	current := filepath.Join(dataDir, "release")
	paths := append(append([]string{}, legacyResourcePaths()...), "_mirror/status.json", "_templates/MihomoPro.yaml")
	for _, path := range paths {
		body, err := os.ReadFile(filepath.Join(release, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		to := filepath.Join(current, filepath.FromSlash(path))
		if err = os.MkdirAll(filepath.Dir(to), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(to, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(current, filepath.Join(dataDir, "current")); err != nil {
		t.Fatal(err)
	}
	s := &server{dataDir: dataDir, domain: "rules.example.com", actionToken: token, subconverterURL: backend}
	_, status, err := s.legacyCurrent()
	if err != nil {
		t.Fatal(err)
	}
	var upstreamFetches, nodeFetches, configFetches, denied atomic.Int32
	s.resourceHTTPClient = &http.Client{Transport: ruleSourceTransport(func(r *http.Request) (*http.Response, error) {
		prefix := "https://raw.githubusercontent.com/666OS/rules/" + status.Commit + "/"
		path := strings.TrimPrefix(r.URL.String(), prefix)
		if !strings.HasPrefix(r.URL.String(), prefix) || !legacyKnownPath(path) {
			return nil, fmt.Errorf("unverified upstream resource: %s", r.URL)
		}
		body, err := os.ReadFile(filepath.Join(current, filepath.FromSlash(path)))
		if err != nil {
			return nil, err
		}
		upstreamFetches.Add(1)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	const nodeURL = "http://subscription.example.test/nodes"
	var fixture strings.Builder
	for i, name := range []string{"香港 HK Fixture", "台湾 TW Fixture", "日本 JP Fixture", "新加坡 SG Fixture", "韩国 KR Fixture", "美国 US Fixture", "德国 DE Fixture", "泰国 TH Fixture"} {
		fmt.Fprintf(&fixture, "ss://%s@192.0.2.%d:443#%s\n", base64.RawStdEncoding.EncodeToString([]byte("aes-128-gcm:synthetic")), i+1, url.PathEscape(name))
	}
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("coralbay:"+token))
	listener, err := net.Listen("tcp", ":18080")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host == "coralbay-rules.internal" {
			if r.URL.String() == conversionGroupsURL && r.Header.Get("Proxy-Authorization") == auth {
				configFetches.Add(1)
			}
			s.egressProxy(w, r)
			return
		}
		if r.URL.String() == nodeURL && r.Header.Get("Proxy-Authorization") == auth && r.Method == "GET" {
			nodeFetches.Add(1)
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, fixture.String())
			return
		}
		denied.Add(1)
		http.Error(w, "isolated fixture rejects external traffic", http.StatusForbidden)
	})}
	go func() { _ = proxy.Serve(listener) }()
	t.Cleanup(func() { _ = proxy.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for _, target := range []string{"clash", "stash"} {
		var local []byte
		for _, source := range []string{"local", "upstream"} {
			t.Run(target+"/"+source, func(t *testing.T) {
				before := upstreamFetches.Load()
				params := url.Values{"target": {target}, "url": {nodeURL}, "config": {"https://" + s.domain + "/_configs/coralbay-mihomopro.ini"}, "rule_source": {source}}
				content, headers, meta, err := s.convertSubscriptionWithMetadata(ctx, params)
				if err != nil {
					t.Fatalf("real backend generation: %v (node fetches=%d, INI fetches=%d, denied=%d)", err, nodeFetches.Load(), configFetches.Load(), denied.Load())
				}
				wantFetches := int32(0)
				if source == "upstream" {
					wantFetches = 33
				}
				if upstreamFetches.Load()-before != wantFetches || headers.Get("X-CoralBay-Rule-Source") != source || meta.ActualSource != source || meta.RuleRevision != status.Commit || meta.Uncovered != 0 || len(meta.Resources) != 33 {
					t.Fatalf("invalid provenance: %+v fetches=%d", meta, upstreamFetches.Load()-before)
				}
				var doc struct {
					Rules   []string         `yaml:"rules"`
					Proxies []map[string]any `yaml:"proxies"`
					Groups  []struct {
						Name    string   `yaml:"name"`
						Proxies []string `yaml:"proxies"`
					} `yaml:"proxy-groups"`
					Providers map[string]any `yaml:"rule-providers"`
				}
				if err = yaml.Unmarshal(content, &doc); err != nil {
					t.Fatal(err)
				}
				if len(doc.Rules) != meta.Count || len(doc.Rules) < 100000 || len(doc.Proxies) != 8 || len(doc.Providers) != 0 || strings.Contains(string(content), "_converted/") || doc.Rules[len(doc.Rules)-1] != "MATCH,漏网之鱼" {
					t.Fatalf("incomplete output: rules=%d metadata=%d nodes=%d providers=%d", len(doc.Rules), meta.Count, len(doc.Proxies), len(doc.Providers))
				}
				groups := map[string][]string{}
				for _, g := range doc.Groups {
					groups[g.Name] = g.Proxies
				}
				if len(groups["全球手动"]) != 8 || len(groups["日本自动"]) != 1 || !strings.Contains(groups["日本自动"][0], "JP Fixture") || len(groups["EMBY"]) == 0 {
					t.Fatalf("real backend did not apply INI groups: global=%v japan=%v EMBY=%v", groups["全球手动"], groups["日本自动"], groups["EMBY"])
				}
				advertising, emby := 0, 0
				for _, rule := range doc.Rules {
					if strings.HasSuffix(rule, ",广告拦截") {
						advertising++
					}
					if strings.Contains(rule, ",EMBY") {
						emby++
					}
				}
				if advertising < 900 || emby < 1 {
					t.Fatalf("advertising/EMBY were lost: %d/%d", advertising, emby)
				}
				if source == "local" {
					local = append([]byte(nil), content...)
				} else if !reflect.DeepEqual(local, content) {
					t.Fatal("local and fixed-upstream output differ")
				}
				ordinarySourceCoreAssert(t, content, len(doc.Rules))
				t.Logf("%s/%s: real converter returned 8 nodes / %d groups; core loaded %d inline rules; advertising=%d EMBY=%d; 33 MRS verified; YAML=%d bytes", target, source, len(doc.Groups), len(doc.Rules), advertising, emby, len(content))
			})
		}
	}
	if nodeFetches.Load() < 1 || configFetches.Load() < 1 {
		t.Fatal("backend bypassed authenticated fixture proxy")
	}
	t.Logf("real proxy fetches: synthetic nodes=%d authenticated embedded INI=%d rejected other requests=%d", nodeFetches.Load(), configFetches.Load(), denied.Load())
}

func ordinarySourceCoreAssert(t *testing.T, candidate []byte, expected int) {
	t.Helper()
	const binary = "/usr/local/bin/coralbay-probe-core"
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if database := os.Getenv("ORDINARY_SOURCE_TEST_MMDB"); database != "" {
		body, err := os.ReadFile(database)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "Country.mmdb"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(file, candidate, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, binary, "-t", "-d", dir, "-f", file).CombinedOutput(); err != nil {
		t.Fatalf("native syntax: %v %s", err, out)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(candidate, &cfg); err != nil {
		t.Fatal(err)
	}
	probes := map[string]string{}
	for _, raw := range cfg["rules"].([]any) {
		parts := strings.Split(raw.(string), ",")
		if len(parts) < 3 {
			continue
		}
		category := ""
		if parts[2] == "广告拦截" || parts[2] == "人工智能" {
			category = parts[2]
		} else if parts[0] == "IP-CIDR" {
			category = "IP"
		}
		if category != "" && probes[category] == "" {
			probes[category] = parts[1] + "," + parts[2]
		}
	}
	if len(probes) != 3 {
		t.Fatal("fixture lacks advertising, AI or IP rule probes")
	}
	// Runtime uses the exact rules and node/group membership, but disables all
	// listeners, DNS, and periodic group probes so no proxy traffic is attempted.
	for _, key := range []string{"port", "socks-port", "redir-port", "mixed-port", "tproxy-port", "external-ui", "external-ui-name", "external-ui-url", "external-controller", "listeners"} {
		delete(cfg, key)
	}
	for _, raw := range cfg["proxy-groups"].([]any) {
		g := raw.(map[string]any)
		g["type"] = "select"
		for _, key := range []string{"url", "interval", "strategy", "lazy", "tolerance"} {
			delete(g, key)
		}
	}
	cfg["dns"] = map[string]any{"enable": false}
	cfg["tun"] = map[string]any{"enable": false}
	cfg["sniffer"] = map[string]any{"enable": false}
	cfg["log-level"] = "silent"
	cfg["secret"] = "isolated-ordinary-test"
	socket := filepath.Join(dir, "controller.sock")
	cfg["external-controller-unix"] = socket
	safe, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, safe, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, binary, "-d", dir, "-f", file)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost/rules", nil)
		req.Header.Set("Authorization", "Bearer isolated-ordinary-test")
		resp, err := client.Do(req)
		if err == nil {
			var loaded struct {
				Rules []struct{ Type, Payload, Proxy string } `json:"rules"`
			}
			decodeErr := json.NewDecoder(resp.Body).Decode(&loaded)
			_ = resp.Body.Close()
			if resp.StatusCode == 200 && decodeErr == nil {
				if len(loaded.Rules) != expected || loaded.Rules[len(loaded.Rules)-1].Type != "Match" || loaded.Rules[len(loaded.Rules)-1].Proxy != "漏网之鱼" {
					t.Fatalf("core loaded wrong rule count/final: %d expected %d", len(loaded.Rules), expected)
				}
				seen := map[string]bool{}
				for _, rule := range loaded.Rules {
					seen[rule.Payload+","+rule.Proxy] = true
				}
				for category, payload := range probes {
					if !seen[payload] {
						t.Fatalf("core lost %s rule payload/policy: %s", category, payload)
					}
					t.Logf("native loaded representative %s rule: %s", category, payload)
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("native core did not expose its actual loaded rules")
}
