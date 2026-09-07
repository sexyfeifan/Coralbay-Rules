package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func routingPrimeProviderFixture(t *testing.T) (*server, routingProfileSpec, routingRuleSnapshot) {
	t.Helper()
	s := routingBuilderFixtureServer(t, routingBuilderNodeFixture)
	s.domain = "rules.example.com"
	spec := routingBuilderSpec()
	ids := []string{}
	for _, r := range spec.Rules {
		ids = append(ids, r.ID)
	}
	if _, err := s.loadRoutingRuleSnapshot(context.Background(), ids, false); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.loadPublishedRoutingRuleSnapshot(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	spec.RuleDelivery = &routingRuleDelivery{Mode: "provider", Source: "local"}
	return s, spec, snapshot
}

func TestRoutingProviderLocalAndUpstreamUseIdenticalPublishedRaw(t *testing.T) {
	s, spec, snapshot := routingPrimeProviderFixture(t)
	oldTransport := s.routingHTTPClient.Transport
	var ruleRequests atomic.Int32
	s.routingHTTPClient.Transport = routingBuilderTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "subscription.example.com" {
			ruleRequests.Add(1)
			return nil, fmt.Errorf("rule upstream is offline")
		}
		return oldTransport.RoundTrip(req)
	})
	local, err := s.buildRouting(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.RuleDelivery = &routingRuleDelivery{Mode: "provider", Source: "upstream"}
	upstream, err := s.buildRouting(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if ruleRequests.Load() != 0 {
		t.Fatal("provider generation unexpectedly contacted rule upstream")
	}
	if local.Revision != snapshot.Revision || upstream.Revision != local.Revision || local.RuleLibrary != "metacubex" || local.GeneratedAt == "" {
		t.Fatal("effective source metadata missing")
	}
	if !reflect.DeepEqual(local.Groups, upstream.Groups) || !reflect.DeepEqual(local.RuleOrder, upstream.RuleOrder) {
		t.Fatal("switching delivery changed policies or ordering")
	}
	for _, target := range spec.Clients {
		var a, b map[string]any
		if yaml.Unmarshal([]byte(local.Outputs[target]), &a) != nil || yaml.Unmarshal([]byte(upstream.Outputs[target]), &b) != nil {
			t.Fatal("invalid provider config")
		}
		pa, pb := a["rule-providers"].(map[string]any), b["rule-providers"].(map[string]any)
		for id, resource := range snapshot.Resources {
			x, y := pa[routingProviderName(id)].(map[string]any), pb[routingProviderName(id)].(map[string]any)
			if x["url"] != resource.LocalURL || y["url"] != resource.SourceURL || x["behavior"] != resource.Behavior {
				t.Fatalf("%s invalid provider %s", target, id)
			}
			if target == "stash" {
				if _, exists := x["type"]; exists {
					t.Fatal("Stash should use documented URL-based provider syntax")
				}
			}
			x["url"] = y["url"]
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s changed more than download URLs", target)
		}
		if err := validateYAMLReferences([]byte(local.Outputs[target])); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(local.Outputs[target], "RULE-SET,cb-private-ip,DIRECT,no-resolve") {
			t.Fatal("IP no-resolve lost")
		}
	}
	if !strings.Contains(strings.Join(local.Warnings, " "), "Stash 原生") {
		t.Fatal("Stash native validation limit not exposed")
	}
	mux := http.NewServeMux()
	s.registerRoutingRoutes(mux)
	for _, resource := range snapshot.Resources {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", resource.LocalURL, nil))
		if w.Code != 200 || routingSHA256(w.Body.Bytes()) != resource.SHA256 || !reflect.DeepEqual(w.Body.Bytes(), resource.Content) {
			t.Fatalf("published resource differs from upstream original: %s %d", resource.ID, w.Code)
		}
	}
}

func TestRoutingProviderRequiresPublishedRawAndNeverFallsBack(t *testing.T) {
	for _, state := range []string{"missing", "corrupt"} {
		t.Run(state, func(t *testing.T) {
			var s *server
			var spec routingProfileSpec
			if state == "corrupt" {
				var snapshot routingRuleSnapshot
				s, spec, snapshot = routingPrimeProviderFixture(t)
				if err := os.WriteFile(s.routingRawPath(snapshot.Revision, "gemini"), []byte("payload: []\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				s = routingBuilderFixtureServer(t, routingBuilderNodeFixture)
				s.domain = "rules.example.com"
				spec = routingBuilderSpec()
				spec.RuleDelivery = &routingRuleDelivery{Mode: "provider", Source: "local"}
			}
			old := s.routingHTTPClient.Transport
			var requests atomic.Int32
			s.routingHTTPClient.Transport = routingBuilderTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "subscription.example.com" {
					requests.Add(1)
					return nil, fmt.Errorf("no upstream")
				}
				return old.RoundTrip(req)
			})
			for _, source := range []string{"local", "upstream"} {
				spec.RuleDelivery.Source = source
				if _, err := s.buildRouting(context.Background(), spec); err == nil {
					t.Fatalf("published %s rules without valid original bytes", source)
				}
			}
			if requests.Load() != 0 {
				t.Fatal("missing local resources triggered an upstream fetch")
			}
		})
	}
}

func TestRoutingDeliveryValidationAndLegacyInlineCompatibility(t *testing.T) {
	for _, delivery := range []*routingRuleDelivery{{}, {Mode: "provider"}, {Mode: "provider", Source: "automatic"}, {Mode: "inline", Source: "local"}, {Mode: "unknown"}} {
		spec := routingBuilderSpec()
		spec.RuleDelivery = delivery
		if validateRoutingSpec(&spec) == nil {
			t.Fatalf("accepted invalid delivery %+v", delivery)
		}
	}
	s := routingBuilderFixtureServer(t, routingBuilderNodeFixture)
	spec := routingBuilderSpec()
	old, err := s.buildRouting(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if spec.RuleDelivery != nil || old.RuleDelivery.Mode != "inline" || strings.Contains(old.Outputs["mihomo"], "rule-providers:") {
		t.Fatal("missing field migrated old behavior")
	}
	spec.RuleDelivery = &routingRuleDelivery{Mode: "inline"}
	explicit, err := s.buildRouting(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old.Outputs, explicit.Outputs) {
		t.Fatal("explicit inline changed legacy output")
	}
}

func TestRoutingProviderSwitchPreservesLinksAndOtherProfiles(t *testing.T) {
	s, spec, snapshot := routingPrimeProviderFixture(t)
	inlineSpec := spec
	inlineSpec.RuleDelivery = nil
	oldBuild, err := s.buildRouting(context.Background(), inlineSpec)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.createRoutingProfile(inlineSpec, oldBuild)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.createRoutingProfile(inlineSpec, oldBuild)
	if err != nil {
		t.Fatal(err)
	}
	build, err := s.buildRouting(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.updateRoutingProfile(first.ID, first.Version, spec, build)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Links, updated.Links) || updated.RuleDelivery.Source != "local" || len(updated.RuleResources) != len(snapshot.Resources) {
		t.Fatal("source switch lost link or persisted metadata")
	}
	other, err := s.getRoutingProfile(second.ID)
	if err != nil || other.Spec.RuleDelivery != nil || other.RuleDelivery.Mode != "inline" {
		t.Fatal("switch migrated another profile")
	}
	upstreamSpec := spec
	upstreamSpec.RuleDelivery = &routingRuleDelivery{Mode: "provider", Source: "upstream"}
	upstream, err := s.buildRouting(context.Background(), upstreamSpec)
	if err != nil {
		t.Fatal(err)
	}
	updated, err = s.updateRoutingProfile(updated.ID, updated.Version, upstreamSpec, upstream)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RuleDelivery.Source != "upstream" || !reflect.DeepEqual(first.Links, updated.Links) {
		t.Fatal("upstream switch changed stable URL")
	}
	bad := upstream
	bad.RuleResources = append([]routingProviderPreview(nil), upstream.RuleResources...)
	bad.RuleResources[0].Revision = strings.Repeat("b", 40)
	if _, err := s.updateRoutingProfile(updated.ID, updated.Version, spec, bad); err == nil {
		t.Fatal("published mismatched provider metadata")
	}
	unchanged, err := s.getRoutingProfile(updated.ID)
	if err != nil || unchanged.Version != updated.Version || unchanged.RuleDelivery.Source != "upstream" {
		t.Fatal("failed candidate changed active source")
	}
	db, _ := s.routingDatabase()
	var stored string
	if err = db.QueryRow("SELECT spec FROM profiles WHERE id=?", second.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "rule_delivery") {
		t.Fatal("old spec was rewritten during metadata migration")
	}
	_ = db.Close()
	s.routingDB = nil
	reopened, err := s.getRoutingProfile(updated.ID)
	if err != nil || reopened.RuleDelivery.Source != "upstream" || !reflect.DeepEqual(reopened.RuleResources, updated.RuleResources) {
		t.Fatal("provider metadata lost after restart")
	}
}

func TestRoutingProviderValidationFilesUseExactRawBytes(t *testing.T) {
	s, spec, snapshot := routingPrimeProviderFixture(t)
	build, err := s.buildRouting(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	candidate, providers, err := routingLocalValidationConfig(build.Outputs["stash"], snapshot.Resources, dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(candidate), "raw.githubusercontent.com") || strings.Contains(string(candidate), "https://rules.example.com") {
		t.Fatal("validation depends on online or self-request URLs")
	}
	for id, resource := range snapshot.Resources {
		provider := providers[routingProviderName(id)].(map[string]any)
		content, err := os.ReadFile(provider["path"].(string))
		if err != nil || routingSHA256(content) != resource.SHA256 {
			t.Fatalf("validation resource %s differs", id)
		}
	}
}

// Run with ROUTING_PROVIDER_TEST_IMAGE=coralbay-rules:routing-preview. This is a
// disposable network:none container with synthetic REJECT-only policies. It
// downloads raw YAML from its own loopback HTTP fixture, asserts loaded counts,
// then exercises native matches without reaching any remote destination.
func TestRoutingProviderCoreDownloadsAndMatches(t *testing.T) {
	image := os.Getenv("ROUTING_PROVIDER_TEST_IMAGE")
	if image == "" {
		t.Skip("set ROUTING_PROVIDER_TEST_IMAGE for real provider download/match integration")
	}
	// The release image deliberately has no HTTP test server. Cross-compile a
	// tiny loopback fixture rather than installing packages or enabling network.
	fixtureDir := t.TempDir()
	fixtureSource := filepath.Join(fixtureDir, "fixture.go")
	if err := os.WriteFile(fixtureSource, []byte(`package main
import ("log"; "net/http")
func main() { log.Fatal(http.ListenAndServe("127.0.0.1:18081", http.FileServer(http.Dir("/config/http")))) }
`), 0600); err != nil {
		t.Fatal(err)
	}
	arch, err := exec.Command("docker", "image", "inspect", image, "--format", "{{.Architecture}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	compile := exec.Command("go", "build", "-o", filepath.Join(fixtureDir, "fixture"), fixtureSource)
	compile.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+strings.TrimSpace(string(arch)))
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("build isolated HTTP fixture: %v %s", err, out)
	}
	s, spec, snapshot := routingPrimeProviderFixture(t)
	for _, source := range []string{"local", "upstream"} {
		t.Run(source, func(t *testing.T) {
			spec.RuleDelivery = &routingRuleDelivery{Mode: "provider", Source: source}
			build, err := s.buildRouting(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			var cfg map[string]any
			if yaml.Unmarshal([]byte(build.Outputs["mihomo"]), &cfg) != nil {
				t.Fatal("invalid generated YAML")
			}
			cfg["proxies"] = []any{}
			safeGroups := []any{map[string]any{"name": "observed-DIRECT", "type": "select", "proxies": []string{"REJECT"}}}
			for _, g := range build.Groups {
				safeGroups = append(safeGroups, map[string]any{"name": g.Name, "type": "select", "proxies": []string{"REJECT"}})
			}
			cfg["proxy-groups"] = safeGroups
			cfg["mixed-port"] = 17891
			cfg["bind-address"] = "127.0.0.1"
			cfg["external-controller"] = "127.0.0.1:19091"
			cfg["log-level"] = "debug"
			cfg["dns"] = map[string]any{"enable": false}
			for i, value := range cfg["rules"].([]any) {
				line := value.(string)
				line = strings.ReplaceAll(line, ",DIRECT", ",observed-DIRECT")
				cfg["rules"].([]any)[i] = line
			}
			dir := t.TempDir()
			httpRoot := filepath.Join(dir, "http", source, snapshot.Revision)
			if err = os.MkdirAll(httpRoot, 0700); err != nil {
				t.Fatal(err)
			}
			for id, resource := range snapshot.Resources {
				if err = os.WriteFile(filepath.Join(httpRoot, id+".yaml"), resource.Content, 0600); err != nil {
					t.Fatal(err)
				}
				provider := cfg["rule-providers"].(map[string]any)[routingProviderName(id)].(map[string]any)
				provider["url"] = "http://127.0.0.1:18081/" + source + "/" + snapshot.Revision + "/" + id + ".yaml"
				provider["proxy"] = "DIRECT" // Only the isolated loopback fixture bypasses the REJECT policies.
			}
			encoded, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "config.yaml"), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			name := "cb-provider-core-" + source + fmt.Sprint(time.Now().UnixNano())
			args := []string{"run", "-d", "--name", name, "--network", "none", "--entrypoint", "/bin/sh", "--mount", "type=bind,source=" + dir + ",target=/config", "--mount", "type=bind,source=" + fixtureDir + ",target=/fixture,readonly", image, "-c", "/fixture/fixture & exec /usr/local/bin/coralbay-probe-core -d /config -f /config/config.yaml"}
			if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
				t.Fatalf("core startup %v %s", err, out)
			}
			defer exec.Command("docker", "rm", "-f", name).Run()
			var loaded struct {
				Providers map[string]struct {
					RuleCount int `json:"ruleCount"`
				} `json:"providers"`
			}
			ready := false
			for attempt := 0; attempt < 40; attempt++ {
				out, err := exec.Command("docker", "exec", name, "curl", "-fsS", "http://127.0.0.1:19091/providers/rules").Output()
				if err == nil && json.Unmarshal(out, &loaded) == nil {
					ready = true
					for id, resource := range snapshot.Resources {
						var raw struct {
							Payload []string `yaml:"payload"`
						}
						_ = yaml.Unmarshal(resource.Content, &raw)
						if loaded.Providers[routingProviderName(id)].RuleCount != len(raw.Payload) {
							ready = false
						}
					}
					if ready {
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !ready {
				logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
				t.Fatalf("actual provider load counts mismatch: %+v\n%s", loaded, logs)
			}
			for id, resource := range snapshot.Resources {
				cached, err := os.ReadFile(filepath.Join(dir, "rules", "metacubex", snapshot.Revision, id+".yaml"))
				if err != nil || routingSHA256(cached) != resource.SHA256 {
					t.Fatalf("HTTP download/cache does not match raw %s", id)
				}
			}
			cases := map[string]string{"foo.google.example.com": "CB · google", "google-keyword.test": "CB · google", "google-gemini.example.com": "CB · gemini", "host": "observed-DIRECT", "10.1.2.3": "observed-DIRECT", "[fc00::7]": "observed-DIRECT", "unmatched.test": "CB · GLOBAL"}
			for host := range cases {
				_ = exec.Command("docker", "exec", name, "curl", "-sS", "--max-time", "2", "--socks5-hostname", "127.0.0.1:17891", "http://"+host+"/").Run()
			}
			logs, err := exec.Command("docker", "logs", name).CombinedOutput()
			if err != nil {
				t.Fatal(err)
			}
			for host, policy := range cases {
				matched := false
				for _, line := range strings.Split(string(logs), "\n") {
					if strings.Contains(line, host+":80") && strings.Contains(line, "using "+policy+"[REJECT]") {
						matched = true
						break
					}
				}
				if !matched {
					t.Fatalf("native match %s -> %s not observed\n%s", host, policy, logs)
				}
			}
			t.Logf("%s: %d raw providers downloaded, counts/bytes/native domain/keyword/regex/IPv4/IPv6/fallback matches verified", source, len(snapshot.Resources))
		})
	}
}

// Explicit opt-in for validating a complete published corpus with the installed
// Linux core. The directory is only read; all validation uses private copies.
func TestRoutingProviderPublishedCorpusCoreLoads(t *testing.T) {
	rawDir := os.Getenv("ROUTING_PROVIDER_RAW_DIR")
	if rawDir == "" {
		t.Skip("set ROUTING_PROVIDER_RAW_DIR to validate the complete published corpus")
	}
	if _, err := os.Stat("/usr/local/bin/coralbay-probe-core"); err != nil {
		t.Fatal("complete corpus verification requires the installed native core")
	}
	resources := map[string]routingRuleResource{}
	providers := map[string]any{}
	rules := []string{}
	total := 0
	for _, rule := range routingRuleCatalog() {
		content, err := os.ReadFile(filepath.Join(rawDir, rule.ID+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var raw struct {
			Payload []string `yaml:"payload"`
		}
		if err = yaml.Unmarshal(content, &raw); err != nil || len(raw.Payload) == 0 {
			t.Fatalf("invalid raw resource %s: %v", rule.ID, err)
		}
		resources[rule.ID] = routingRuleResource{ID: rule.ID, Behavior: rule.Behavior, Format: "yaml", Content: content, SHA256: routingSHA256(content), Count: len(raw.Payload), Bytes: int64(len(content))}
		providers[routingProviderName(rule.ID)] = map[string]any{"type": "http", "behavior": rule.Behavior, "format": "yaml", "url": "https://fixture.invalid/" + rule.ID + ".yaml"}
		rules = append(rules, "RULE-SET,"+routingProviderName(rule.ID)+",DIRECT,no-resolve")
		total += len(raw.Payload)
		if rule.ID == "private-domain" || rule.ID == "ads" || rule.ID == "global" {
			t.Logf("%s raw entries: %d", rule.ID, len(raw.Payload))
		}
	}
	rules = append(rules, "MATCH,DIRECT")
	encoded, err := yaml.Marshal(map[string]any{"mode": "rule", "rules": rules, "rule-providers": providers, "dns": map[string]any{"enable": false}})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := routingCoreValidate(context.Background(), map[string]string{"mihomo": string(encoded)}, resources)
	if err != nil || !validated {
		t.Fatalf("complete corpus actual core loading failed: %v", err)
	}
	t.Logf("native core loaded all %d providers and %d raw entries exactly", len(resources), total)
}

func TestRoutingProviderCoreRejectsSilentlyDroppedRule(t *testing.T) {
	const binary = "/usr/local/bin/coralbay-probe-core"
	if _, err := os.Stat(binary); err != nil {
		t.Skip("requires installed native core")
	}
	content := []byte("payload:\n  - DOMAIN-SUFFIX,example.com\n  - DOMAIN-REGEX,([\n")
	resource := routingRuleResource{ID: "google", Behavior: "classical", Format: "yaml", Content: content, SHA256: routingSHA256(content)}
	output := "rule-providers:\n  cb-google:\n    type: http\n    behavior: classical\n    url: https://fixture.invalid/google.yaml\nrules:\n  - RULE-SET,cb-google,DIRECT\n  - MATCH,DIRECT\n"
	dir := t.TempDir()
	candidate, providers, err := routingLocalValidationConfig(output, map[string]routingRuleResource{"google": resource}, dir)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "config.yaml")
	if err = os.WriteFile(file, candidate, 0600); err != nil {
		t.Fatal(err)
	}
	// This is the exact failure that a successful syntax check cannot catch.
	if out, err := exec.Command(binary, "-t", "-d", dir, "-f", file).CombinedOutput(); err != nil {
		t.Fatalf("fixture should pass syntax check and lose one entry: %v %s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = routingVerifyLoadedProviders(ctx, binary, dir, providers, map[string]routingRuleResource{"google": resource})
	if err == nil || !strings.Contains(err.Error(), "实际加载 1 条，期望 2 条") {
		t.Fatalf("silently dropped rule was not rejected precisely: %v", err)
	}
}
