package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestStashTargetUsesModernClashShape(t *testing.T) {
	params, err := subscriptionParams(subscriptionRequest{Target: "stash", URL: "https://1.1.1.1/sub", Interval: 24, ClashDoH: true})
	if err != nil {
		t.Fatal(err)
	}
	if params.Get("target") != "stash" || params.Get("clash.doh") != "true" {
		t.Fatalf("unexpected Stash params: %v", params)
	}
	cloned := cloneURLValues(params)
	cloned.Set("target", "clash")
	cloned.Set("expand", "true")
	cloned.Set("expand_rulesets", "true")
	if params.Get("target") != "stash" || cloned.Get("target") != "clash" {
		t.Fatal("target alias mutated signed parameters")
	}
	if cloned.Get("expand") != "true" {
		t.Fatal("Stash conversion must expand preset rules")
	}
	if cloned.Get("expand_rulesets") != "true" {
		t.Fatal("Stash conversion must force subconverter rule expansion")
	}
	content := []byte("proxies:\n  - name: example\n    type: vless\nproxy-groups:\n")
	if got := convertedNodeCount("stash", content); got != 1 {
		t.Fatalf("Stash node count = %d, want 1", got)
	}
}

func TestMihomoProPresetHasGlobalAndRegionalFallbacks(t *testing.T) {
	content, err := os.ReadFile("templates/subconverter/mihomopro.ini")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, marker := range []string{"全球自动`url-test`.*", "故障转移`fallback`.*", "欧洲自动", "东南亚自动", "https://www.gstatic.com/generate_204"} {
		if !strings.Contains(text, marker) {
			t.Errorf("missing Stash-safe preset marker %q", marker)
		}
	}
	for _, marker := range []string{
		"香港策略`select`[]香港自动`[]故障转移`[]全球手动`[]DIRECT`(?i)(香港|",
		"日本策略`select`[]日本自动`[]故障转移`[]全球手动`[]DIRECT`(?i)(日本|",
		"美国策略`select`[]美国自动`[]故障转移`[]全球手动`[]DIRECT`(?i)(美国|",
	} {
		if !strings.Contains(text, marker) {
			t.Errorf("regional manual group must include matching nodes: missing %q", marker)
		}
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

func TestTemplateVariantsAndHistoryControlsArePresent(t *testing.T) {
	content, err := os.ReadFile("web/admin.html")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, marker := range []string{`id="templateVariant"`, `Perfect Panel 原始版`, `设置项目`, `复用或单独删除`} {
		if !strings.Contains(text, marker) {
			t.Errorf("missing UI marker %q", marker)
		}
	}
	for _, id := range []string{"clash", "mihomo", "openclash"} {
		found := false
		for _, client := range clientTemplates {
			found = found || client.ID == id
		}
		if !found {
			t.Errorf("missing split template %q", id)
		}
	}
}

func TestDeleteSingleSubscriptionHistory(t *testing.T) {
	s := &server{dataDir: t.TempDir()}
	s.appendSubscriptionHistory(subscriptionHistoryItem{ID: "keep", Target: "clash"})
	s.appendSubscriptionHistory(subscriptionHistoryItem{ID: "delete", Target: "surge", Settings: &subscriptionRequest{Target: "surge", Interval: 12, Emoji: true}})
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/subscription-history/delete", nil)
	req.SetPathValue("id", "delete")
	recorder := httptest.NewRecorder()
	s.deleteSubscriptionHistory(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	items := s.readSubscriptionHistory()
	if len(items) != 1 || items[0].ID != "keep" {
		t.Fatalf("unexpected history after delete: %+v", items)
	}
	content, err := os.ReadFile(s.historyPath())
	if err != nil {
		t.Fatal(err)
	}
	var persisted []subscriptionHistoryItem
	if json.Unmarshal(content, &persisted) != nil || len(persisted) != 1 {
		t.Fatalf("invalid persisted history: %s", content)
	}
}
