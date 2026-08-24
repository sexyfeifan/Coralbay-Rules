package main

import (
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestSubscriptionParamsCarryAdvancedOptions(t *testing.T) {
	params, err := subscriptionParams(subscriptionRequest{
		Target: "surge", URL: "https://1.1.1.1/sub", Config: "https://8.8.8.8/config.ini",
		Filename: "CoralBay", Include: "香港|日本", Exclude: "过期", Rename: "旧@新",
		SurgeVer: 4, Interval: 12, Emoji: true, Dedup: true, UDP: true, XUDP: true,
		TFO: true, TLS13: true, AppendType: true, ListOnly: true, Insert: true,
		Expand: true, NewName: true, FilterNodes: true, SurgeDoH: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]string{
		"target": "surge", "interval": "43200", "include": "香港|日本", "exclude": "过期",
		"rename": "旧@新", "ver": "4", "emoji": "true", "dedup": "true", "udp": "true",
		"xudp": "true", "tfo": "true", "tls13": "true", "append_type": "true", "list": "true",
		"insert": "true", "expand": "true", "new_name": "true", "fdn": "true", "surge.doh": "true",
	}
	for key, want := range wants {
		if got := params.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestSubscriptionParamsRejectInvalidRegex(t *testing.T) {
	_, err := subscriptionParams(subscriptionRequest{Target: "clash", URL: "https://1.1.1.1/sub", Include: "["})
	if err == nil {
		t.Fatal("invalid include regex was accepted")
	}
}

func TestValidateSignedSubscriptionLink(t *testing.T) {
	s := &server{domain: "rules.example.com", adminPassword: "password", actionToken: "token"}
	p := url.Values{"target": {"clash"}, "url": {"https://example.com/sub"}, "emoji": {"true"}}
	canonical := p.Encode()
	raw := "https://rules.example.com/sub?" + canonical + "&sig=" + url.QueryEscape(s.sign(canonical))
	parsed, err := s.validateSignedLink(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Get("target") != "clash" || parsed.Get("url") != "https://example.com/sub" {
		t.Fatalf("unexpected parsed params: %v", parsed)
	}
	if _, err := s.validateSignedLink(raw + "tampered"); err == nil {
		t.Fatal("tampered signature was accepted")
	}
}

func TestPPanelAndSubscriptionEntrancesStaySeparate(t *testing.T) {
	content, err := os.ReadFile("web/admin.html")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, marker := range []string{`data-tab="templates">PPanel 模板`, `data-tab="subscription">订阅转换`, `id="templates"`, `id="subscriptionConverter"`} {
		if !strings.Contains(text, marker) {
			t.Errorf("missing separate entrance marker %q", marker)
		}
	}
}
