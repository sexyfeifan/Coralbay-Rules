package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type routingRuleTestTransport func(*http.Request) (*http.Response, error)

func (fn routingRuleTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func routingRuleTestResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

type routingRulesFixture struct {
	mu           sync.Mutex
	revision     string
	failedBranch bool
	failedFile   string
	requests     []string
}

func (fixture *routingRulesFixture) client() *http.Client {
	return &http.Client{Transport: routingRuleTestTransport(func(req *http.Request) (*http.Response, error) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.requests = append(fixture.requests, req.URL.String())
		if req.URL.String() == routingBranchURL {
			if fixture.failedBranch {
				return routingRuleTestResponse(req, 503, "unavailable"), nil
			}
			return routingRuleTestResponse(req, 200, fmt.Sprintf(`{"ref":"refs/heads/meta","object":{"type":"commit","sha":%q}}`, fixture.revision)), nil
		}
		if fixture.failedFile != "" && strings.HasSuffix(req.URL.Path, "/"+fixture.failedFile+".yaml") {
			return routingRuleTestResponse(req, 200, "payload:\n  - MATCH,DIRECT\n"), nil
		}
		body := "payload:\n  - DOMAIN-SUFFIX," + fixture.revision[:8] + ".example.com\n  - DOMAIN-KEYWORD,example\n"
		if strings.Contains(req.URL.Path, "/geoip/") {
			body = "payload:\n  - 10.0.0.0/8\n  - fc00::/7\n"
		}
		return routingRuleTestResponse(req, 200, body), nil
	})}
}

func TestRoutingRuleCatalogBoundariesAndOrder(t *testing.T) {
	rules := routingRuleCatalog()
	if len(rules) != 78 {
		t.Fatalf("catalog count=%d, want 78", len(rules))
	}
	index := routingRuleIndex()
	if len(index) != len(rules) {
		t.Fatal("duplicate catalog ID")
	}
	for i, rule := range rules {
		if i > 0 && rule.Priority < rules[i-1].Priority {
			t.Fatal("catalog is not ordered")
		}
		if !strings.HasPrefix(rule.SourceURL, "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/") {
			t.Fatalf("unexpected source: %s", rule.SourceURL)
		}
		if rule.Family == "geosite" && !strings.Contains(rule.SourceURL, "/geosite/classical/") {
			t.Fatalf("domain subset used: %s", rule.ID)
		}
		if !strings.Contains("|proxy|direct|reject|", "|"+rule.DefaultAction+"|") {
			t.Fatalf("invalid action: %s", rule.DefaultAction)
		}
	}
	if r := index["tracker"]; r.Name != "BT Tracker" || r.Recommended || r.DefaultAction != "proxy" {
		t.Fatalf("tracker inherited misleading privacy behavior: %+v", r)
	}
	for _, pair := range [][2]string{{"private-ip", "ads"}, {"gemini", "google"}, {"youtube", "media-all"}, {"github-copilot", "github"}, {"steam-cn", "steam"}, {"xbox", "microsoft"}, {"cn-domain", "global"}, {"cn-ip", "global"}} {
		if index[pair[0]].Priority >= index[pair[1]].Priority {
			t.Fatalf("%s must precede %s", pair[0], pair[1])
		}
	}
	// The returned collection is private to its caller.
	rules[0].Name = "modified"
	if routingRuleCatalog()[0].Name == "modified" {
		t.Fatal("catalog shares mutable entries")
	}
}

func TestRoutingRuleParserPreservesSemantics(t *testing.T) {
	index := routingRuleIndex()
	domain := "payload:\n  - DOMAIN,api.example.com\n  - DOMAIN-SUFFIX,example.com\n  - DOMAIN-KEYWORD,service\n  - 'DOMAIN-REGEX,^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$'\n  - DOMAIN,api.example.com\n"
	got, err := parseRoutingRuleYAML(index["private-domain"], []byte(domain))
	want := []string{"DOMAIN,api.example.com", "DOMAIN-SUFFIX,example.com", "DOMAIN-KEYWORD,service", "DOMAIN-REGEX,^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("domain semantics: got %#v, %v", got, err)
	}
	got, err = parseRoutingRuleYAML(index["private-ip"], []byte("payload:\n  - 10.1.2.3/8\n  - fd12::1/7\n"))
	want = []string{"IP-CIDR,10.0.0.0/8,no-resolve", "IP-CIDR6,fc00::/7,no-resolve"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("IP semantics: got %#v, %v", got, err)
	}
}

func TestRoutingRuleParserRejectsConfigurationAndUnsupportedData(t *testing.T) {
	index := routingRuleIndex()
	for name, body := range map[string]string{
		"empty":             "payload: []",
		"other_field":       "payload: [DOMAIN,example.com]\nproxies: []",
		"duplicate_payload": "payload: [one]\npayload: [two]",
		"alias":             "payload:\n - &rule DOMAIN,example.com\n - *rule",
		"typed_scalar":      "payload: [123]",
		"unsupported_match": "payload: ['MATCH,DIRECT']",
		"action_injection":  "payload: ['DOMAIN,example.com,DIRECT']",
		"newline_injection": "payload: [\"DOMAIN,example.com\\nMATCH,DIRECT\"]",
		"invalid_regex":     "payload: ['DOMAIN-REGEX,(']",
		"multi_document":    "payload: ['DOMAIN,example.com']\n---\npayload: ['DOMAIN,other.com']",
		"yaml_tag":          "payload: [!custom DOMAIN,example.com]",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRoutingRuleYAML(index["openai"], []byte(body)); err == nil {
				t.Fatalf("accepted %q", body)
			}
		})
	}
	for _, body := range []string{"payload: ['10.0.0.0/33']", "payload: ['IP-CIDR,10.0.0.0/8']", "payload: ['::ffff:192.0.2.0/120']"} {
		if _, err := parseRoutingRuleYAML(index["private-ip"], []byte(body)); err == nil {
			t.Fatalf("accepted IP payload %q", body)
		}
	}
	if _, err := parseRoutingRuleYAML(index["openai"], []byte(strings.Repeat(" ", routingRuleMaxBytes+1))); err == nil {
		t.Fatal("accepted oversized document")
	}
}

func TestRoutingRuleSnapshotIsolationCachingAndCommitExpansion(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "current", "legacy.txt")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("legacy remains unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := &routingRulesFixture{revision: strings.Repeat("a", 40)}
	s := &server{dataDir: dir, routingHTTPClient: fixture.client()}
	first, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai"}, false)
	if err != nil || first.Revision != fixture.revision || first.Stale {
		t.Fatalf("first fetch: %+v %v", first, err)
	}
	if len(fixture.requests) != 2 {
		t.Fatalf("first fetch requests=%v", fixture.requests)
	}
	first.Rules["openai"][0] = "modified"
	second, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai"}, false)
	if err != nil || len(fixture.requests) != 2 || second.Rules["openai"][0] == "modified" {
		t.Fatalf("cache failure: %+v %v", second, err)
	}
	_, err = s.loadRoutingRuleSnapshot(context.Background(), []string{"openai", "netflix"}, false)
	if err != nil || len(fixture.requests) != 3 {
		t.Fatalf("expansion should fetch only one pinned file: requests=%v err=%v", fixture.requests, err)
	}
	fixture.revision = strings.Repeat("b", 40)
	third, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"netflix"}, true)
	if err != nil || third.Revision != fixture.revision {
		t.Fatalf("new revision: %+v %v", third, err)
	}
	state, disk, err := s.readRoutingRuleState()
	if err != nil || len(disk.Rules) != 2 {
		t.Fatalf("lost prior profile's rule: %+v %v", disk, err)
	}
	for id, doc := range disk.Rules {
		if !strings.Contains(doc.SourceURL, "/"+state.Revision+"/") || !strings.Contains(doc.Entries[0], "bbbbbbbb") {
			t.Fatalf("mixed revisions for %s: %+v", id, doc)
		}
	}
	content, err := os.ReadFile(legacy)
	if err != nil || string(content) != "legacy remains unchanged" {
		t.Fatalf("modified old rules: %s %v", content, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("unexpected top-level writes: %+v %v", entries, err)
	}
	if _, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"../../current/legacy.txt"}, false); err == nil {
		t.Fatal("accepted unknown rule path")
	}
}

func TestRoutingRuleRefreshFailureKeepsCompleteLastGoodSnapshot(t *testing.T) {
	fixture := &routingRulesFixture{revision: strings.Repeat("a", 40)}
	s := &server{dataDir: t.TempDir(), routingHTTPClient: fixture.client()}
	ids := []string{"openai", "netflix"}
	first, err := s.loadRoutingRuleSnapshot(context.Background(), ids, false)
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := s.readRoutingRuleState()
	fixture.revision = strings.Repeat("b", 40)
	fixture.failedFile = "netflix"
	stale, err := s.loadRoutingRuleSnapshot(context.Background(), ids, true)
	if err != nil || !stale.Stale || stale.LastError == "" || stale.Revision != first.Revision || !reflect.DeepEqual(first.Rules, stale.Rules) {
		t.Fatalf("last good not retained: %+v %v", stale, err)
	}
	after, _, _ := s.readRoutingRuleState()
	if after.Snapshot != before.Snapshot || after.Revision != before.Revision || after.UpdatedAt != before.UpdatedAt {
		t.Fatalf("failed candidate published: before=%+v after=%+v", before, after)
	}
	requests := len(fixture.requests)
	if _, err = s.loadRoutingRuleSnapshot(context.Background(), ids, false); err != nil || len(fixture.requests) != requests {
		t.Fatal("failure retry backoff not honored")
	}
	if _, err = s.loadRoutingRuleSnapshot(context.Background(), []string{"openai", "netflix", "telegram"}, true); err == nil {
		t.Fatal("returned partial last-good data for missing rule")
	}
	fixture.failedFile = ""
	recovered, err := s.loadRoutingRuleSnapshot(context.Background(), ids, true)
	if err != nil || recovered.Stale || recovered.LastError != "" || recovered.Revision != fixture.revision {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
}

func TestRoutingRuleExpiredSnapshotAndPinnedBootstrap(t *testing.T) {
	fixture := &routingRulesFixture{revision: strings.Repeat("a", 40), failedBranch: true}
	s := &server{dataDir: t.TempDir(), routingHTTPClient: fixture.client()}
	first, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai"}, false)
	if err != nil || first.Revision != routingBootstrapRevision || !first.Stale || first.LastError == "" {
		t.Fatalf("bootstrap: %+v %v", first, err)
	}
	state, _, _ := s.readRoutingRuleState()
	state.CheckedAt = time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	if err := s.saveRoutingRuleState(state); err != nil {
		t.Fatal(err)
	}
	fixture.failedBranch = false
	updated, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai"}, false)
	if err != nil || updated.Revision != fixture.revision || updated.Stale {
		t.Fatalf("expired data did not refresh: %+v %v", updated, err)
	}
}

func TestRoutingRuleSnapshotCorruptionAndSourceBoundary(t *testing.T) {
	fixture := &routingRulesFixture{revision: strings.Repeat("a", 40)}
	s := &server{dataDir: t.TempDir(), routingHTTPClient: fixture.client()}
	if _, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai"}, false); err != nil {
		t.Fatal(err)
	}
	state, _, _ := s.readRoutingRuleState()
	path := filepath.Join(s.routingRulesDir(), "releases", state.Revision, state.Snapshot+".json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.readRoutingRuleState(); err == nil {
		t.Fatal("accepted modified snapshot despite SHA mismatch")
	}
	response := s.routingCatalogResponse()
	if response["last_error"] == "" {
		t.Fatal("cache corruption not exposed")
	}
	for _, url := range []string{"https://example.com/rules.yaml", "https://raw.githubusercontent.com/other/repo/" + fixture.revision + "/geo/geosite/classical/openai.yaml", "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geosite/classical/openai.yaml", routingBranchURL + "?redirect=true"} {
		if _, err := s.routingSourceFetch(context.Background(), url, 100); err == nil {
			t.Fatalf("accepted unreviewed URL %s", url)
		}
	}
	goodURL := routingPinnedURL(routingRuleIndex()["openai"], fixture.revision)
	s.routingHTTPClient = &http.Client{Transport: routingRuleTestTransport(func(req *http.Request) (*http.Response, error) {
		resp := routingRuleTestResponse(req, 302, "")
		resp.Header.Set("Location", "https://example.com/")
		return resp, nil
	})}
	if _, err := s.routingSourceFetch(context.Background(), goodURL, 100); err == nil {
		t.Fatal("accepted source redirect")
	}
	s.routingHTTPClient = &http.Client{Transport: routingRuleTestTransport(func(req *http.Request) (*http.Response, error) {
		return routingRuleTestResponse(req, 200, strings.Repeat("x", 101)), nil
	})}
	if _, err := s.routingSourceFetch(context.Background(), goodURL, 100); err == nil {
		t.Fatal("accepted oversized streaming body")
	}
}

func TestRoutingRuleHandlers(t *testing.T) {
	fixture := &routingRulesFixture{revision: strings.Repeat("a", 40)}
	s := &server{dataDir: t.TempDir(), routingHTTPClient: fixture.client()}
	catalog := httptest.NewRecorder()
	s.routingCatalogHandler(catalog, httptest.NewRequest(http.MethodGet, "/api/routing/catalog", nil))
	var initial struct {
		Rules []struct {
			ID     string `json:"id"`
			Cached bool   `json:"cached"`
		} `json:"rules"`
	}
	if catalog.Code != 200 || json.Unmarshal(catalog.Body.Bytes(), &initial) != nil || len(initial.Rules) != 78 || len(fixture.requests) != 0 {
		t.Fatalf("catalog fetches network or has incorrect JSON: %s", catalog.Body.String())
	}
	details := httptest.NewRecorder()
	s.routingRuleDetailsHandler(details, httptest.NewRequest(http.MethodGet, "/api/routing/rules/details?id=openai&limit=1", nil))
	var preview struct {
		Entries   []string `json:"entries"`
		Count     int      `json:"count"`
		Truncated bool     `json:"truncated"`
		Revision  string   `json:"revision"`
	}
	if details.Code != 200 || json.Unmarshal(details.Body.Bytes(), &preview) != nil || len(preview.Entries) != 1 || preview.Count != 2 || !preview.Truncated || preview.Revision != fixture.revision {
		t.Fatalf("unexpected details: %s", details.Body.String())
	}
	syncResponse := httptest.NewRecorder()
	s.routingRuleSyncHandler(syncResponse, httptest.NewRequest(http.MethodPost, "/api/routing/rules/sync", nil))
	_, disk, err := s.readRoutingRuleState()
	if syncResponse.Code != 200 || err != nil || len(disk.Rules) != 78 {
		t.Fatalf("full sync failed: %s %v", syncResponse.Body.String(), err)
	}
	for _, handler := range []http.HandlerFunc{s.routingCatalogHandler, s.routingRuleDetailsHandler} {
		r := httptest.NewRecorder()
		handler(r, httptest.NewRequest(http.MethodPost, "/", nil))
		if r.Code != 405 {
			t.Fatalf("GET handler allowed POST: %d", r.Code)
		}
	}
	r := httptest.NewRecorder()
	s.routingRuleSyncHandler(r, httptest.NewRequest(http.MethodGet, "/", nil))
	if r.Code != 405 {
		t.Fatalf("sync allowed GET: %d", r.Code)
	}
}

// The normal suite never uses the network. A manually downloaded, commit-pinned
// corpus can additionally exercise all 78 real YAML inputs with this same parser.
func TestRoutingRuleLocalUpstreamCorpus(t *testing.T) {
	dir := os.Getenv("CORALBAY_ROUTING_CORPUS")
	if dir == "" {
		t.Skip("set CORALBAY_ROUTING_CORPUS to a directory of pinned <id>.yaml fixtures")
	}
	total := 0
	for _, rule := range routingRuleCatalog() {
		content, err := os.ReadFile(filepath.Join(dir, rule.ID+".yaml"))
		if err != nil {
			t.Errorf("%s: %v", rule.ID, err)
			continue
		}
		entries, err := parseRoutingRuleYAML(rule, content)
		if err != nil {
			t.Errorf("%s: %v", rule.ID, err)
			continue
		}
		total += len(entries)
	}
	t.Logf("validated %d upstream catalog entries across %d rules", total, len(routingRuleCatalog()))
}
