package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v3"
)

type ruleSourceTransport func(*http.Request) (*http.Response, error)

func (f ruleSourceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func ordinarySourceFixture(t *testing.T) (*server, url.Values) {
	t.Helper()
	s, _ := mihomoProFixture(t)
	s.mrsDecoder = func(_ context.Context, behavior string, raw []byte) ([]string, error) {
		if behavior == "ipcidr" {
			return []string{"198.51.100.0/24", "2001:db8::/32"}, nil
		}
		name := strings.ToLower(strings.TrimSuffix(filepath.Base(string(raw)), ".mrs"))
		return []string{"+." + name + ".example", "exact." + name + ".example"}, nil
	}
	var groups []map[string]any
	for _, line := range strings.Split(builtinConversionINI, "\n") {
		if strings.HasPrefix(line, "custom_proxy_group=") {
			name, _, _ := strings.Cut(strings.TrimPrefix(line, "custom_proxy_group="), "`")
			groups = append(groups, map[string]any{"name": name, "type": "select", "proxies": []string{"DIRECT"}})
		}
	}
	doc := map[string]any{"proxies": []map[string]any{{"name": "Test", "type": "ss", "server": "example.com", "port": 443, "cipher": "aes-128-gcm", "password": "test"}}, "proxy-groups": groups, "rules": []string{"MATCH,漏网之鱼"}}
	body, _ := yaml.Marshal(doc)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("config") != conversionGroupsURL || r.URL.Query().Has("rule_source") || r.URL.Query().Get("expand") != "true" {
			t.Errorf("invalid backend parameters: %v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "text/yaml")
		w.Write(body)
	}))
	t.Cleanup(backend.Close)
	s.subconverterURL = backend.URL
	return s, url.Values{"target": {"clash"}, "url": {"ss://fixture"}, "config": {"https://" + s.domain + "/_configs/coralbay-mihomopro.ini"}, "rule_source": {"local"}}
}

func TestOrdinaryRuleSourcesEquivalentAndStrict(t *testing.T) {
	s, params := ordinarySourceFixture(t)
	var fetches atomic.Int32
	s.resourceHTTPClient = &http.Client{Transport: ruleSourceTransport(func(r *http.Request) (*http.Response, error) {
		fetches.Add(1)
		prefix := "https://raw.githubusercontent.com/666OS/rules/" + strings.Repeat("a", 40) + "/"
		if !strings.HasPrefix(r.URL.String(), prefix) {
			return nil, fmt.Errorf("unpinned request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("binary:" + strings.TrimPrefix(r.URL.String(), prefix)))}, nil
	})}
	local, headers, meta, err := s.convertSubscriptionWithMetadata(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if fetches.Load() != 0 || meta.Count != 57 || len(meta.Resources) != 33 || meta.Uncovered != 0 || headers.Get("X-CoralBay-Rule-Source") != "local" {
		t.Fatalf("incomplete local generation: %+v requests=%d", meta, fetches.Load())
	}
	params.Set("rule_source", "upstream")
	upstream, _, upmeta, err := s.convertSubscriptionWithMetadata(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(local, upstream) || fetches.Load() != 33 || upmeta.RuleRevision != meta.RuleRevision {
		t.Fatal("sources are not equivalent")
	}
	params.Set("target", "stash")
	stash, _, stashMeta, err := s.convertSubscriptionWithMetadata(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	var stashDoc struct {
		Rules     []string       `yaml:"rules"`
		Providers map[string]any `yaml:"rule-providers"`
	}
	var clashDoc struct {
		Rules []string `yaml:"rules"`
	}
	if yaml.Unmarshal(stash, &stashDoc) != nil || yaml.Unmarshal(local, &clashDoc) != nil || len(stashDoc.Providers) != 0 || !reflect.DeepEqual(stashDoc.Rules, clashDoc.Rules) || stashMeta.Count != len(stashDoc.Rules) || strings.Contains(string(stash), "_converted/") {
		t.Fatal("Stash transformation replaced selected-source rules")
	}
	s.resourceHTTPClient = &http.Client{Transport: ruleSourceTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("corrupt"))}, nil
	})}
	if _, _, _, err = s.convertSubscriptionWithMetadata(context.Background(), params); err == nil || !strings.Contains(err.Error(), "摘要") {
		t.Fatal("upstream mismatch silently fell back")
	}
	params.Set("rule_source", "local")
	if err = os.WriteFile(filepath.Join(s.legacyResourceDir(meta.Revision), "mihomo/domain/Advertising.mrs"), []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.convertSubscriptionWithMetadata(context.Background(), params); err == nil {
		t.Fatal("local corruption silently fell back")
	}
}

func TestNativeMRSSemanticsRejectUnknown(t *testing.T) {
	for _, tc := range []struct{ behavior, entry, want string }{
		{"domain", "+.example.com", "DOMAIN-SUFFIX,example.com,Proxy"},
		{"domain", "exact.example.com", "DOMAIN,exact.example.com,Proxy"},
		{"ipcidr", "2001:db8::/32", "IP-CIDR6,2001:db8::/32,Proxy,no-resolve"},
	} {
		got, err := nativeMRSRule(tc.behavior, tc.entry, "Proxy", true)
		if err != nil || got != tc.want {
			t.Fatalf("%q %v", got, err)
		}
	}
	for _, entry := range []string{"*.example.com", "+.foo.*", ".example.com", "example.com,REJECT", "", "a/b"} {
		if _, err := nativeMRSRule("domain", entry, "Proxy", false); err == nil {
			t.Fatalf("silently broadened %q", entry)
		}
	}
}

func TestOrdinarySourcesDoNotAlterLegacyParameters(t *testing.T) {
	body := subscriptionRequest{Target: "clash", URL: "https://8.8.8.8/sub"}
	legacy, err := subscriptionParams(body)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Has("rule_source") {
		t.Fatal("old link gained source field")
	}
	body.RuleSource = "local"
	modern, err := subscriptionParams(body)
	if err != nil || modern.Get("rule_source") != "local" {
		t.Fatal("source not signed")
	}
	modern.Del("rule_source")
	if modern.Encode() != legacy.Encode() {
		t.Fatal("unrelated parameters changed")
	}
	s, p := ordinarySourceFixture(t)
	for _, change := range []func(url.Values){func(p url.Values) { p.Set("target", "loon") }, func(p url.Values) { p.Set("list", "true") }, func(p url.Values) { p.Set("config", "https://example.com/custom.ini") }, func(p url.Values) { p.Set("rule_source", "invalid") }} {
		q := cloneURLValues(p)
		change(q)
		if _, _, _, err = s.convertSubscriptionWithMetadata(context.Background(), q); err == nil {
			t.Fatal("unsupported source mode accepted")
		}
	}
}

func TestConversionGroupsOnlyThroughAuthenticatedExactProxyPath(t *testing.T) {
	s := &server{actionToken: "test-only"}
	for _, tc := range []struct {
		address, auth string
		want          int
	}{
		{conversionGroupsURL, "", 407},
		{conversionGroupsURL, "test-only", 200},
		{conversionGroupsURL + "?file=/etc/passwd", "test-only", 404},
		{"http://coralbay-rules.internal/../../etc/passwd", "test-only", 404},
	} {
		r := httptest.NewRequest("GET", tc.address, nil)
		if tc.auth != "" {
			r.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("coralbay:"+tc.auth)))
		}
		w := httptest.NewRecorder()
		s.egressProxy(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: %d", tc.address, w.Code)
		}
		if w.Code == 200 && (strings.Contains(w.Body.String(), "_converted/") || strings.Count(w.Body.String(), "ruleset=") != 1) {
			t.Fatal("old incomplete rules leaked into generated config")
		}
	}
}

func TestConvertedResponseRejectsValidTruncation(t *testing.T) {
	body := "proxies:\n  - {name: node, type: ss, server: example.com, port: 443, cipher: aes-128-gcm, password: test}\n#" + strings.Repeat(" ", 16<<20) + "\nrules: [MATCH,DIRECT]\n"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
	defer backend.Close()
	s := &server{subconverterURL: backend.URL}
	if _, _, err := s.convertSubscription(context.Background(), url.Values{"target": {"clash"}}); err == nil || !strings.Contains(err.Error(), "16 MiB") {
		t.Fatal("oversized valid-prefix output accepted")
	}
}

func TestRemoteINIRejectsBadCandidatesPreservingCache(t *testing.T) {
	s := &server{dataDir: t.TempDir()}
	source := remoteConfigSource{URL: "https://8.8.8.8/template.ini"}
	valid := "[custom]\nenable_rule_generator=true\nruleset=Proxy,[]FINAL\n"
	for i, body := range []string{valid, "<!DOCTYPE html><html>upstream error page</html>", strings.Repeat(" ", 2<<20) + valid, "[custom]\nrandom_nonsense=not-a-config\n"} {
		s.resourceHTTPClient = &http.Client{Transport: ruleSourceTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		err := s.fetchRemoteConfig(context.Background(), source)
		if (i == 0) != (err == nil) {
			t.Fatalf("candidate %d: %v", i, err)
		}
		got, _ := os.ReadFile(s.remoteConfigPath(remoteConfigID(source.URL)))
		if string(got) != valid {
			t.Fatal("last good config changed")
		}
	}
}

func TestManagedNativeRulesAreLocalAndUnknownIsNotExternal(t *testing.T) {
	s := &server{dataDir: t.TempDir(), domain: "rules.example.com"}
	path := filepath.Join(s.dataDir, "current", "surge", "AI.txt")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte("DOMAIN-SUFFIX,example.com\n"), 0644)
	got := s.analyzeRemoteRuleDependencies("ruleset=AI,https://rules.example.com/surge/AI.txt\nruleset=AI,https://rules.example.com/surge/missing.txt\nruleset=AI,https://rules.example.com/unmanaged.txt\n")
	if got.Local != 2 || got.Missing != 1 || got.Unknown != 1 || got.External != 0 || !got.Items[0].Available {
		t.Fatalf("misclassified: %+v", got)
	}
}

func TestOrdinarySourceLinkRoundTripAndTampering(t *testing.T) {
	s, _ := ordinarySourceFixture(t)
	s.domain = "8.8.8.8"
	body := subscriptionRequest{Target: "clash", URL: "https://1.1.1.1/sub", Config: "https://8.8.8.8/_configs/coralbay-mihomopro.ini", RuleSource: "local"}
	encoded, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	s.createSubscriptionLinkV2(w, httptest.NewRequest("POST", "/", strings.NewReader(string(encoded))))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		URL      string                   `json:"url"`
		Metadata subscriptionRuleMetadata `json:"rule_source_metadata"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Metadata.ActualSource != "local" || result.Metadata.Count != 57 {
		t.Fatal("missing generation provenance")
	}
	history := s.readSubscriptionHistory()
	if len(history) != 1 || history[0].Settings.RuleSource != "local" {
		t.Fatal("history lost source selection")
	}
	u, _ := url.Parse(result.URL)
	download := httptest.NewRecorder()
	s.signedSubscription(download, httptest.NewRequest("GET", u.String(), nil))
	if download.Code != 200 || download.Header().Get("X-CoralBay-Rule-Source") != "local" || !strings.Contains(download.Body.String(), "DOMAIN-SUFFIX,advertising.example,广告拦截") {
		t.Fatal(download.Code, download.Body.String())
	}
	q := u.Query()
	q.Set("rule_source", "upstream")
	u.RawQuery = q.Encode()
	tampered := httptest.NewRecorder()
	s.signedSubscription(tampered, httptest.NewRequest("GET", u.String(), nil))
	if tampered.Code != http.StatusForbidden {
		t.Fatal("source parameter was not protected by signature", tampered.Code)
	}
}
