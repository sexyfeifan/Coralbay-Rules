package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNewerVersion(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"v3.0.1", "v3.0.0", true},
		{"v3.1.0", "v3.0.9", true},
		{"v4.0.0", "v3.9.9", true},
		{"v3.0.0", "v3.0.0", false},
		{"v2.1.0", "v3.0.0", false},
	}
	for _, test := range tests {
		if got := newerVersion(test.candidate, test.current); got != test.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", test.candidate, test.current, got, test.want)
		}
	}
}

func TestConvertedNodeCount(t *testing.T) {
	tests := []struct {
		target, content string
		want            int
	}{
		{"surge", "[General]\nloglevel=notify\n[Proxy]\nA = ss, host, 443\nB = vmess, host, 443\n[Rule]\nFINAL,DIRECT\n", 2},
		{"surge", "[General]\nloglevel=notify\n", 0},
		{"loon", "[Proxy]\nA = VLESS,host,443,id\n[Remote Rule]\nhttps://rules.example/a, policy = DIRECT\n", 1},
		{"quanx", "[server_local]\nvless=host:443, password=id, tag=A\n", 1},
		{"clash", "proxies:\n  - name: A\n    type: vless\nproxy-groups:\n  - name: Proxy\n", 1},
		{"singbox", `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"vless","tag":"A"}]}`, 1},
		{"v2ray", base64.StdEncoding.EncodeToString([]byte("vless://a\nvmess://b\n")), 2},
	}
	for _, test := range tests {
		if got := convertedNodeCount(test.target, []byte(test.content)); got != test.want {
			t.Errorf("convertedNodeCount(%s) = %d, want %d", test.target, got, test.want)
		}
	}
}

func TestSecureEqual(t *testing.T) {
	if !secureEqual("secret", "secret") || secureEqual("secret", "wrong") || secureEqual("a", "aa") {
		t.Fatal("constant-time token comparison returned an unexpected result")
	}
}

func TestAdminSession(t *testing.T) {
	s := &server{actionToken: "test-token", adminPassword: "secret", actionTimes: make(map[string]time.Time)}
	handler := s.auth(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	expires := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	valid := expires + "." + s.sign(expires)
	for _, test := range []struct {
		cookie string
		want   int
	}{{"", 401}, {"bad", 401}, {valid, 204}} {
		req := httptest.NewRequest(http.MethodPost, "http://example.test/action", nil)
		if test.cookie != "" {
			req.AddCookie(&http.Cookie{Name: "coralbay_session", Value: test.cookie})
		}
		res := httptest.NewRecorder()
		handler(res, req)
		if res.Code != test.want {
			t.Fatalf("cookie %q: got %d, want %d", test.cookie, res.Code, test.want)
		}
	}
}

func TestRuleDetailsPaginationAndSearch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "current", "_sources", "geo", "site")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "google.txt"), []byte("a.google.com\nb.example.com\nc.google.com\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s := &server{dataDir: dir}
	req := httptest.NewRequest(http.MethodGet, "/api/public/rule-details?path=mihomo/domain/Google.mrs&q=google&page_size=1&page=2", nil)
	res := httptest.NewRecorder()
	s.ruleDetails(res, req)
	if res.Code != 200 || !strings.Contains(res.Body.String(), "c.google.com") || !strings.Contains(res.Body.String(), `"count":2`) {
		t.Fatalf("unexpected response: %d %s", res.Code, res.Body.String())
	}
}

func TestCompositeReleaseID(t *testing.T) {
	valid := strings.Repeat("a", 40) + "-" + strings.Repeat("b", 12) + "-v3.6.0-" + strings.Repeat("c", 12)
	if !commitPattern.MatchString(valid) || commitPattern.MatchString("../../etc/passwd") {
		t.Fatal("release id validation failed")
	}
}

func TestPPanelTemplateStructure(t *testing.T) {
	files, err := filepath.Glob("templates/clients/perfect-panel/*.gotmpl")
	if err != nil || len(files) != 15 {
		t.Fatalf("template catalog: %v (%d files)", err, len(files))
	}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		if len(content) < 100 || !strings.Contains(text, "{{") || !strings.Contains(text, "}}") {
			t.Errorf("template %s is empty or does not contain template actions", file)
		}
		if !strings.Contains(text, ".Proxies") {
			t.Errorf("template %s does not render PPanel proxies", file)
		}
	}
}

func TestMihomoTemplatesCoverRegionalAndUnmatchedNodes(t *testing.T) {
	for _, file := range []string{"templates/ppanel_openclash_pro_cn.gotmpl", "templates/openclash/Pro_cn.upstream.yaml"} {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		for _, marker := range []string{
			"x-filter-eu:", "x-filter-sea:", "x-filter-other:",
			"欧洲策略", "欧洲自动", "欧洲均衡",
			"东南亚策略", "东南亚自动", "东南亚均衡",
			"其他地区策略", "其他地区自动", "其他地区均衡",
		} {
			if !strings.Contains(text, marker) {
				t.Errorf("%s does not cover %q", file, marker)
			}
		}
	}
}

func TestNativeAdaptersUseLocalPlaceholder(t *testing.T) {
	for _, file := range []string{"surge-tail.conf", "surfboard-tail.conf", "egern-tail.yaml"} {
		content, err := os.ReadFile(filepath.Join("templates", "clients", "native", file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "__NATIVE_LIST_BASE_URL__") {
			t.Errorf("%s bypasses the local native rule mirror", file)
		}
	}
}

func TestLoonAdapterUsesNativeRulesAndRegionalFilters(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("templates", "clients", "native", "loon-tail.conf"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{"[Remote Filter]", "香港自动 = url-test", "广告拦截", "img-url=__ASSETS_BASE_URL__", "Hong_Kong.png", "AI.png", "surge/Advertising.txt", "policy = 广告拦截", "GEOIP,CN,国内流量", "FINAL,漏网之鱼"} {
		if !strings.Contains(text, expected) {
			t.Errorf("Loon adapter missing %q", expected)
		}
	}
	if strings.Contains(text, "{{ $proxyNames }}") {
		t.Error("Loon groups should use Remote Filter instead of expanding every node name")
	}
}

func TestLoonUsesRemoteConfigImportScheme(t *testing.T) {
	for _, client := range clientTemplates {
		if client.ID != "loon" {
			continue
		}
		if client.URLScheme != "loon://import?sub=${encodeURIComponent(url)}" {
			t.Fatalf("Loon must import the generated document as a remote config, got %q", client.URLScheme)
		}
		if strings.Contains(client.URLScheme, "nodelist=") {
			t.Fatal("Loon full configuration must not be imported as a node subscription")
		}
		return
	}
	t.Fatal("Loon client metadata is missing")
}

func TestLoonTemplateUsesCurrentRealitySyntax(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("templates", "clients", "perfect-panel", "loon.gotmpl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{"transport=tcp", "public-key=", "short-id=", "over-tls=true", "sni=", "[URL Rewrite]"} {
		if !strings.Contains(text, expected) {
			t.Errorf("Loon template missing current syntax %q", expected)
		}
	}
	for _, obsolete := range []string{"reality-public-key=", "reality-short-id=", "tls-name=", "[Rewrite]"} {
		if strings.Contains(text, obsolete) {
			t.Errorf("Loon template still contains obsolete syntax %q", obsolete)
		}
	}
}

func TestReadableSource(t *testing.T) {
	tests := map[string]string{
		"mihomo/domain/Google.mrs": "site/google.txt",
		"mihomo/ip/Google.mrs":     "ip/google.txt",
		"mihomo/domain/Emby.mrs":   "",
		"../../etc/passwd":         "",
	}
	for path, want := range tests {
		if got := readableSource(path); got != want {
			t.Errorf("readableSource(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestClientTemplateMetadata(t *testing.T) {
	seen := make(map[string]bool, len(clientTemplates))
	for _, client := range clientTemplates {
		if client.ID == "" || client.PPanelName == "" || client.UserAgent == "" || client.OutputFormat == "" {
			t.Fatalf("client metadata is incomplete: %+v", client)
		}
		if seen[client.ID] {
			t.Fatalf("duplicate client id: %s", client.ID)
		}
		seen[client.ID] = true
		if client.Enhanced != (client.Capability == "adapted") {
			t.Fatalf("enhanced flag and capability disagree for %s", client.ID)
		}
	}
	if len(clientTemplates) != 17 {
		t.Fatalf("got %d client templates, want 17", len(clientTemplates))
	}
}
