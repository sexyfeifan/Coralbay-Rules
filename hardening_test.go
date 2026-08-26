package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyUsageMigrationAndPagination(t *testing.T) {
	s := &server{dataDir: t.TempDir(), domain: "example.com"}
	raw := "https://example.com/sub?target=stash&url=https%3A%2F%2Fexample.org%2Fsub&sig=old"
	id := usageID(raw)
	old := map[string]*subscriptionUsage{id: {ID: id, URL: raw, Target: "stash", Disabled: true, Success: 8}}
	if e := os.MkdirAll(filepath.Dir(s.usagePath()), 0700); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(old)
	os.WriteFile(s.usagePath(), b, 0600)
	_, disabled, e := s.startSubscriptionAccess(raw, "Stash")
	if e != nil || !disabled {
		t.Fatalf("migration: %v %v", disabled, e)
	}
	req := httptest.NewRequest("POST", "/", nil)
	req.SetPathValue("id", id)
	res := httptest.NewRecorder()
	s.renewSubscriptionLink(res, req)
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	items, e := s.readUsage()
	if e != nil || !items[id].Disabled || items[id].Success != 8 || !strings.Contains(items[id].URL, "sig=v2.") {
		t.Fatal("renew lost state")
	}
	s.usageDB.Close()
	s.usageDB = nil
	os.WriteFile(s.usagePath(), []byte("legacy backup no longer authoritative"), 0600)
	if _, disabled, e = s.startSubscriptionAccess(raw, "Stash"); e != nil || !disabled {
		t.Fatalf("reimported old backup: %v", e)
	}
	res = httptest.NewRecorder()
	s.subscriptionUsageCatalog(res, httptest.NewRequest("GET", "/?page=999&page_size=1&state=disabled", nil))
	var page struct {
		Count, Page, Pages int
		Links              []subscriptionUsage
	}
	json.Unmarshal(res.Body.Bytes(), &page)
	if res.Code != 200 || page.Count != 1 || page.Page != 1 || page.Pages != 1 || page.Links[0].Blocked != 2 {
		t.Fatalf("wrong pagination %s", res.Body.String())
	}
}
func TestCustomStashPresetUnchanged(t *testing.T) {
	original := "proxies: [{name: test, type: ss, server: 1.1.1.1, port: 443}]\nproxy-groups: [{name: Custom, type: select, proxies: [test]}]\nrules: ['MATCH,Custom']\n"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(original)) }))
	defer backend.Close()
	s := &server{subconverterURL: backend.URL, domain: "rules.example"}
	got, _, e := s.convertSubscription(context.Background(), url.Values{"target": {"stash"}, "config": {"https://custom.example/preset.ini"}})
	if e != nil || string(got) != original {
		t.Fatalf("custom config changed: %v", e)
	}
}
func TestProbeRejectsUnsafeConfiguration(t *testing.T) {
	for _, node := range []map[string]any{
		{"name": "a", "type": "vless", "server": "127.0.0.1"},
		{"name": "a", "type": "ss", "server": "1.1.1.1", "plugin": "evil"},
		{"name": "a", "type": "vless", "server": "1.1.1.1", "dialer-proxy": "DIRECT"},
	} {
		if _, e := probeNode(context.Background(), node); e == nil {
			t.Fatal("unsafe node accepted")
		}
	}
	node := map[string]any{"name": "original", "type": "vless", "server": "1.1.1.1", "uuid": "fixture", "servername": "example.org"}
	got, e := probeNode(context.Background(), node)
	if e != nil || got["servername"] != "example.org" || node["name"] != "original" {
		t.Fatal("probe mutated node")
	}
}

func TestIndependentKeysAndLegacy(t *testing.T) {
	s := &server{dataDir: t.TempDir(), domain: "example.com", adminPassword: "old", actionToken: "token"}
	p := url.Values{"target": {"stash"}, "url": {"https://example.org/sub"}}
	legacy := s.sign(p.Encode())
	link, e := s.subscriptionLink(p)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(link)
	sig := u.Query().Get("sig")
	s.adminPassword = "new"
	s.actionToken = "new"
	if !s.verifySubscriptionSignature(sig, p.Encode()) || !s.verifySubscriptionSignature(legacy, p.Encode()) {
		t.Fatal("password change broke link")
	}
	if s.verifySubscriptionSignature(sig, p.Encode()+"x") {
		t.Fatal("tamper accepted")
	}
	k, _ := s.loadSubscriptionKeys()
	k.LegacyUntil = time.Now().Add(-time.Hour)
	b, _ := json.Marshal(k)
	os.WriteFile(filepath.Join(s.dataDir, "settings", "subscription-keys.json"), b, 0600)
	if s.verifySubscriptionSignature(legacy, p.Encode()) || !s.verifySubscriptionSignature(sig, p.Encode()) {
		t.Fatal("legacy expiry incorrect")
	}
}
func TestPublicIPRanges(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "100.64.0.1", "192.168.1.2", "169.254.169.254", "::1", "::ffff:127.0.0.1", "fc00::1", "2002:7f00:1::", "64:ff9b::7f00:1"} {
		if publicIP(net.ParseIP(ip)) {
			t.Errorf("accepted %s", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicIP(net.ParseIP(ip)) {
			t.Errorf("rejected %s", ip)
		}
	}
	if _, e := safeDial(context.Background(), "tcp", "127.0.0.1:80"); e == nil {
		t.Fatal("private dial accepted")
	}
	res := httptest.NewRecorder()
	(&server{actionToken: "x"}).egressProxy(res, httptest.NewRequest("CONNECT", "http://127.0.0.1:80", nil))
	if res.Code != 407 {
		t.Fatal(res.Code)
	}
}
func TestPresetBoundaryAndReferences(t *testing.T) {
	s := &server{domain: "rules.example"}
	p := url.Values{"config": {"https://rules.example/_configs/coralbay-mihomopro.ini"}}
	if !s.isBuiltInStash(p) {
		t.Fatal("builtin not detected")
	}
	p.Set("config", "https://other.example/_configs/coralbay-mihomopro.ini")
	if s.isBuiltInStash(p) {
		t.Fatal("custom preset overwritten")
	}
	good := []byte("proxies: [{name: test}]\nproxy-groups: [{name: Select, proxies: [test, DIRECT]}]\nrules: [MATCH,Select]")
	good = []byte(strings.ReplaceAll(string(good), "[MATCH,Select]", "['MATCH,Select']"))
	if e := validateYAMLReferences(good); e != nil {
		t.Fatal(e)
	}
	if e := validateYAMLReferences([]byte(strings.ReplaceAll(string(good), "proxies: [test, DIRECT]", "proxies: [missing]"))); e == nil {
		t.Fatal("dangling proxy accepted")
	}
	if e := validateYAMLReferences([]byte("rules: ['RULE-SET,unknown,DIRECT']")); e == nil {
		t.Fatal("missing provider accepted")
	}
}
func TestPersistedSchedule(t *testing.T) {
	dir := t.TempDir()
	s := &server{dataDir: dir}
	s.scheduleDeadline(true, time.Hour)
	deadline := s.nextSync
	restart := &server{dataDir: dir}
	restart.scheduleDeadline(true, time.Hour)
	if !restart.nextSync.Equal(deadline) {
		t.Fatal("restart drifted deadline")
	}
	restart.scheduleDeadline(false, 2*time.Hour)
	if !restart.nextSync.After(deadline) {
		t.Fatal("interval not updated")
	}
}

func TestUsagePageInitializedBeforeFirstLoad(t *testing.T) {
	data, e := os.ReadFile("web/app.js")
	if e != nil {
		t.Fatal(e)
	}
	text := string(data)
	declaration, call := strings.Index(text, "let usagePage=1"), strings.Index(text, "installSubscriptionUsage();")
	if declaration < 0 || call < 0 || declaration > call {
		t.Fatal("usage pagination accessed in temporal dead zone")
	}
}
