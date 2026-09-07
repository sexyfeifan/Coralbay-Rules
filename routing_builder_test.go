package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type routingBuilderTransport func(*http.Request) (*http.Response, error)

func (f routingBuilderTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const routingBuilderNodeFixture = `proxies:
  - {name: '美国, A | premium', type: vless, server: us.example.com, port: 443, uuid: 6d345895-691c-4f74-9b50-ab638c735687, tls: true, network: tcp, flow: xtls-rprx-vision, encryption: none, servername: www.example.com, client-fingerprint: chrome, reality-opts: {public-key: XFRkJRx4ZI58JWglCWFTcSLwcmCWuZfYXRY_DdpiNUo, short-id: '0123456789abcdef', spider-x: '/hello'}, udp: true}
  - {name: '日本 A', type: ss, server: jp.example.com, port: 8443, cipher: aes-128-gcm, password: fixture-password}
  - {name: '日本 A', type: trojan, server: jp2.example.com, port: 443, password: fixture-password, sni: jp2.example.com}
  - {name: '日本 duplicate', type: ss, server: jp.example.com, port: 8443, cipher: aes-128-gcm, password: fixture-password}
  - {name: '剩余流量：100 GB', type: ss, server: info.example.com, port: 443, cipher: aes-128-gcm, password: fixture-password}
rules:
  - RULE-SET,old-666OS,DIRECT
script:
  code: this-must-never-be-executed
dns:
  nameserver: [untrusted-source-dns]
`

func routingBuilderFixtureServer(t *testing.T, subscription string) *server {
	t.Helper()
	s := &server{dataDir: t.TempDir()}
	s.routingHTTPClient = &http.Client{Transport: routingBuilderTransport(func(r *http.Request) (*http.Response, error) {
		content := ""
		status := http.StatusOK
		switch {
		case r.URL.Host == "subscription.example.com":
			content = subscription
		case r.URL.String() == routingBranchURL:
			content = `{"ref":"refs/heads/meta","object":{"sha":"` + strings.Repeat("a", 40) + `","type":"commit"}}`
		case r.URL.Host == "raw.githubusercontent.com":
			switch filepath.Base(r.URL.Path) {
			case "private.yaml":
				if strings.Contains(r.URL.Path, "/geoip/") {
					content = "payload:\n - 10.0.0.0/8\n - fc00::/7\n"
				} else {
					content = "payload:\n - 'DOMAIN-SUFFIX,local'\n - 'DOMAIN-REGEX,^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$'\n"
				}
			default:
				content = "payload:\n - 'DOMAIN-SUFFIX," + strings.TrimSuffix(filepath.Base(r.URL.Path), ".yaml") + ".example.com'\n - 'DOMAIN-KEYWORD," + strings.TrimSuffix(filepath.Base(r.URL.Path), ".yaml") + "'\n"
			}
		default:
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(content)), Header: http.Header{"Subscription-Userinfo": []string{"upload=1; download=2; total=100"}}, Request: r}, nil
	})}
	return s
}

func routingBuilderSpec() routingProfileSpec {
	return routingProfileSpec{Name: "家庭分流", Sources: []string{"https://subscription.example.com/secret"}, Clients: []string{"mihomo", "openclash", "stash"}, Global: routingFilter{Regions: []string{"JP"}}, Strategy: "select", Match: "proxy", Rules: []routingRuleChoice{
		{ID: "google", Action: "proxy"},
		{ID: "gemini", Action: "proxy", Filter: &routingFilter{Regions: []string{"US"}}, Strategy: "fallback"},
		{ID: "private-domain", Action: "direct"},
		{ID: "private-ip", Action: "direct"},
		{ID: "apple-cn", Action: "direct"},
	}}
}

func TestRoutingBuilderIndependentFiltersPreserveNodeFields(t *testing.T) {
	s := routingBuilderFixtureServer(t, routingBuilderNodeFixture)
	result, err := s.buildRouting(context.Background(), routingBuilderSpec())
	if err != nil {
		t.Fatal(err)
	}
	if result.NodeCount != 3 || len(result.Outputs) != 3 {
		t.Fatalf("nodes=%d outputs=%d", result.NodeCount, len(result.Outputs))
	}
	groups := map[string]routingGroupPreview{}
	for _, g := range result.Groups {
		groups[g.ID] = g
	}
	if !reflect.DeepEqual(groups["global"].Nodes, []string{"日本 A", "日本 A (2)"}) {
		t.Fatalf("global=%+v", groups["global"])
	}
	if !reflect.DeepEqual(groups["gemini"].Nodes, []string{"美国, A | premium"}) || groups["gemini"].Strategy != "fallback" {
		t.Fatalf("override did not select outside global: %+v", groups["gemini"])
	}
	if !reflect.DeepEqual(groups["google"].Nodes, groups["global"].Nodes) {
		t.Fatal("nil filter did not inherit")
	}
	for target, output := range result.Outputs {
		if err := validateYAMLReferences([]byte(output)); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		for _, forbidden := range []string{"666OS", "Nextin", "Pro_cn", "this-must-never", "untrusted-source-dns", "rule-providers:", "proxy-providers:"} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("%s leaked %s", target, forbidden)
			}
		}
		var cfg struct {
			Proxies []map[string]any `yaml:"proxies"`
			Rules   []string         `yaml:"rules"`
			Groups  []map[string]any `yaml:"proxy-groups"`
		}
		if err := yaml.Unmarshal([]byte(output), &cfg); err != nil {
			t.Fatal(err)
		}
		vless := cfg.Proxies[0]
		if vless["encryption"] != "none" || vless["servername"] != "www.example.com" || vless["flow"] != "xtls-rprx-vision" {
			t.Fatalf("%s lost VLESS fields: %+v", target, vless)
		}
		reality := vless["reality-opts"].(map[string]any)
		if reality["spider-x"] != "/hello" || reality["short-id"] != "0123456789abcdef" {
			t.Fatalf("%s lost Reality fields", target)
		}
		if cfg.Rules[len(cfg.Rules)-1] != "MATCH,CB · GLOBAL" {
			t.Fatal("final fallback missing")
		}
		gemini, google := -1, -1
		for i, r := range cfg.Rules {
			if strings.Contains(r, ",google-gemini.example.com,") {
				gemini = i
			}
			if strings.Contains(r, ",google.example.com,") {
				google = i
			}
		}
		if gemini < 0 || google < gemini {
			t.Fatalf("specific rule shadowed: gemini=%d google=%d", gemini, google)
		}
		if !strings.Contains(output, "IP-CIDR6,fc00::/7,DIRECT,no-resolve") {
			t.Fatal("IPv6/no-resolve lost")
		}
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "重复") || result.UsageHeader == "" {
		t.Fatal("dedup/usage metadata missing")
	}
	foundInfo := false
	for _, n := range result.Nodes {
		if strings.Contains(n.Name, "剩余流量") {
			foundInfo = strings.Contains(n.Excluded, "始终剔除")
		}
	}
	if !foundInfo {
		t.Fatal("system node not explained in preview")
	}
}

func TestRoutingBuilderEmptyGroupAndUnsupportedTargetBlockPublication(t *testing.T) {
	for _, scenario := range []string{"global", "override", "stash-field"} {
		t.Run(scenario, func(t *testing.T) {
			sub := routingBuilderNodeFixture
			spec := routingBuilderSpec()
			switch scenario {
			case "global":
				spec.Global.Regions = []string{"DE"}
			case "override":
				spec.Rules[1].Filter.Regions = []string{"DE"}
			case "stash-field":
				sub = strings.Replace(sub, "encryption: none", "encryption: none, packet-encoding: xudp", 1)
			}
			s := routingBuilderFixtureServer(t, sub)
			_, err := s.buildRouting(context.Background(), spec)
			if err == nil {
				t.Fatal("published invalid/empty group")
			}
			if scenario == "stash-field" && !strings.Contains(err.Error(), "Stash") {
				t.Fatal(err)
			}
		})
	}
}

func TestRoutingExplicitEmptyFilterOverridesGlobal(t *testing.T) {
	s := routingBuilderFixtureServer(t, routingBuilderNodeFixture)
	spec := routingBuilderSpec()
	spec.Rules[1].Filter = &routingFilter{}
	result, err := s.buildRouting(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range result.Groups {
		if g.ID == "gemini" && len(g.Nodes) != 3 {
			t.Fatalf("empty independent filter should select all real nodes: %+v", g)
		}
	}
}

func TestRoutingDuplicateNamesMatchOriginalSubscriptionNames(t *testing.T) {
	s := routingBuilderFixtureServer(t, routingBuilderNodeFixture)
	spec := routingBuilderSpec()
	spec.Global.Include = "^日本 A$"
	result, err := s.buildRouting(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Groups[0].Nodes) != 2 {
		t.Fatalf("renaming changed filtering: %+v", result.Groups[0])
	}
}

func TestRoutingDirectCollectionsDoNotShadowSpecificProxyChoices(t *testing.T) {
	s := routingBuilderFixtureServer(t, routingBuilderNodeFixture)
	spec := routingBuilderSpec()
	spec.Rules[0].Action = "direct"
	result, err := s.buildRouting(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	output := result.Outputs["mihomo"]
	if strings.Index(output, "DOMAIN-SUFFIX,google.example.com,DIRECT") < strings.Index(output, "DOMAIN-SUFFIX,google-gemini.example.com,CB · gemini") {
		t.Fatal("broad direct collection shadowed specific proxy rule")
	}
}

func TestRoutingInlineRegexPreservesQuantifierSemantics(t *testing.T) {
	patterns := []string{`^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`, `^(foo,bar|ab{1,3}|x{2,}|[,a]{1,3})$`, `^([ab]{1,3}){1,2}?$`, `^\Qx,y\E$`}
	values := []string{"a", "ab", "a-b", strings.Repeat("a", 63), strings.Repeat("a", 64), "a-", "1a", "foo,bar", "abbb", "abbbb", "xx", "xxxx", ",a", ",,,", "x,y", "", "abab", "aaaaaa", "aaaaaaa"}
	for _, original := range patterns {
		converted, err := routingInlineRegex(original)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(converted, ",") {
			t.Fatalf("unescaped rule delimiter: %s", converted)
		}
		a, b := regexp.MustCompile(original), regexp.MustCompile(converted)
		for _, value := range values {
			if a.MatchString(value) != b.MatchString(value) {
				t.Fatalf("regex changed on %q: %s -> %s", value, original, converted)
			}
		}
	}
}

func TestRoutingURIAndBase64PreserveRealityAndTransport(t *testing.T) {
	uri := "vless://6d345895-691c-4f74-9b50-ab638c735687@node.example.com:443?encryption=none&security=reality&sni=www.example.com&fp=chrome&pbk=XFRkJRx4ZI58JWglCWFTcSLwcmCWuZfYXRY_DdpiNUo&sid=0123456789abcdef&spx=%2Fprobe&type=grpc&serviceName=my-service#%E7%BE%8E%E5%9B%BD"
	for _, body := range []string{uri, base64.StdEncoding.EncodeToString([]byte(uri))} {
		nodes, _, err := routingParseNodes([]byte(body))
		if err != nil || len(nodes) != 1 {
			t.Fatalf("nodes=%d err=%v", len(nodes), err)
		}
		n := nodes[0]
		if err := routingValidateNode(n); err != nil {
			t.Fatal(err)
		}
		if n["encryption"] != "none" || n["servername"] != "www.example.com" || n["network"] != "grpc" || n["name"] != "美国" {
			t.Fatalf("bad URI mapping: %+v", n)
		}
		if n["reality-opts"].(map[string]any)["spider-x"] != "/probe" || n["grpc-opts"].(map[string]any)["grpc-service-name"] != "my-service" {
			t.Fatal("lost nested URI params")
		}
	}
	for _, suffix := range []string{"&unknown=1", "&type=ws", "&security=none", "&mode=multi"} {
		parts := strings.Split(uri, "#")
		if _, err := routingParseURI(parts[0] + suffix + "#" + parts[1]); err == nil {
			t.Fatalf("silently accepted unsupported URI %s", suffix)
		}
	}
	ss := "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:secret")) + "@ss.example.com:443#Japan"
	vmess := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"US VMess","add":"vmess.example.com","port":"443","id":"6d345895-691c-4f74-9b50-ab638c735687","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.example.com","path":"/ws","tls":"tls","sni":"tls.example.com"}`))
	for _, raw := range []string{ss, vmess, "trojan://secret@tr.example.com:443?sni=cdn.example.com&type=ws&path=%2Fws#Japan", "hysteria2://user:secret@hy.example.com:443?sni=cdn.example.com&obfs=salamander&obfs-password=secret#US"} {
		n, err := routingParseURI(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := routingValidateNode(n); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRoutingValidationRejectsUnsafeAndAmbiguousInput(t *testing.T) {
	for _, source := range []string{"http://127.0.0.1/sub", "http://localhost/sub", "http://169.254.169.254/latest", "https://user:pass@source.example/sub", "file:///etc/passwd", "https://source.example/sub#fragment", "http://[::1]/sub"} {
		if routingValidateSourceURL(source) == nil {
			t.Fatalf("accepted unsafe URL %s", source)
		}
	}
	for _, mutate := range []func(*routingProfileSpec){
		func(s *routingProfileSpec) { s.Name = "" }, func(s *routingProfileSpec) { s.Clients = []string{"surge"} }, func(s *routingProfileSpec) { s.Global.Include = "(?=US)" }, func(s *routingProfileSpec) { s.Global.Regions = []string{"Europe"} }, func(s *routingProfileSpec) { s.Rules[0].ID = "old-rule" }, func(s *routingProfileSpec) { s.Rules = append(s.Rules, s.Rules[0]) }, func(s *routingProfileSpec) { s.Match = "reject" }, func(s *routingProfileSpec) { s.IntervalHours = -1 },
	} {
		spec := routingBuilderSpec()
		mutate(&spec)
		if validateRoutingSpec(&spec) == nil {
			t.Fatal("accepted invalid spec")
		}
	}
	for _, body := range []string{"proxy-providers: {remote: {url: 'http://127.0.0.1/secret'}}", routingBuilderNodeFixture + "\n---\nproxies: []", strings.Replace(routingBuilderNodeFixture, "flow: xtls-rprx-vision", "dialer-proxy: old-group", 1)} {
		s := routingBuilderFixtureServer(t, body)
		if _, err := s.buildRouting(context.Background(), routingBuilderSpec()); err == nil {
			t.Fatal("accepted unsupported provider/multi-doc/node fields")
		}
	}
	s := routingBuilderFixtureServer(t, strings.Repeat("x", routingMaxSourceBytes+1))
	if _, err := s.buildRouting(context.Background(), routingBuilderSpec()); err == nil || !strings.Contains(err.Error(), "8 MiB") {
		t.Fatalf("oversized input err=%v", err)
	}
}

func TestRoutingNodeSchemaRejectsInvalidParameters(t *testing.T) {
	for _, mutation := range []func(map[string]any){
		func(n map[string]any) { n["uuid"] = "invalid" },
		func(n map[string]any) { n["alpn"] = 123 },
		func(n map[string]any) { n["reality-opts"] = map[string]any{"public-key": "invalid"} },
		func(n map[string]any) { n["reality-opts"].(map[string]any)["short-id"] = "xyz" },
		func(n map[string]any) { n["network"] = "ws" },
		func(n map[string]any) { n["tls"] = false },
		func(n map[string]any) { n["sni"] = "conflicting.example.com" },
	} {
		nodes, _, err := routingParseNodes([]byte(routingBuilderNodeFixture))
		if err != nil {
			t.Fatal(err)
		}
		n := nodes[0]
		mutation(n)
		if err := routingValidateNode(n); err == nil {
			t.Fatal("accepted malformed node")
		}
	}
	for _, jsonText := range []string{`{"v":"2","net":4}`, `{"v":"2","net":"ws","net":"tcp"}`} {
		if _, err := routingParseVMess(base64.StdEncoding.EncodeToString([]byte(jsonText))); err == nil {
			t.Fatal("accepted ambiguous VMess JSON")
		}
	}
}

func TestRoutingRegionsIndependentCountryCodes(t *testing.T) {
	if len(routingRegions()) < 15 {
		t.Fatal("country catalog incomplete")
	}
	for name, want := range map[string]string{"香港-01": "HK", "UK_London": "GB", "德国 Frankfurt": "DE", "🇯🇵 premium": "JP", "印度尼西亚": "ID", "IN-1": "IN", "unknown": "OTHER"} {
		if got := routingNodeRegion(name); got != want {
			t.Fatalf("%s region=%s want=%s", name, got, want)
		}
	}
}

// Optional integration: run the same compiler output through the repository's
// pinned Mihomo container, offline. Normal unit tests do not require Docker.
func TestRoutingMihomoCore(t *testing.T) {
	image := os.Getenv("ROUTING_TEST_CORE_IMAGE")
	if image == "" {
		t.Skip("set ROUTING_TEST_CORE_IMAGE to run the real core parser")
	}
	s := routingBuilderFixtureServer(t, routingBuilderNodeFixture)
	spec := routingBuilderSpec()
	spec.Clients = []string{"mihomo"}
	result, err := s.buildRouting(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte(result.Outputs["mihomo"]), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("docker", "run", "--rm", "--network", "none", "-v", fmt.Sprintf("%s:/config:ro", dir), image, "-t", "-f", "/config/config.yaml")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Mihomo parser failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}
