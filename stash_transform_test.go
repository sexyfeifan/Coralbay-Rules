package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRestoreStashVLESSFields(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("vless://test-uuid@example.com:443?security=reality&encryption=none&spx=%2Fpath#node"))
	}))
	defer source.Close()
	input := []byte("proxies:\n    - name: node\n      type: vless\n      uuid: test-uuid\n      reality-opts:\n        public-key: key\n        short-id: id\nproxy-groups:\n")
	got := string(restoreStashVLESSFields(context.Background(), source.URL, input))
	if !strings.Contains(got, `spider-x: "/path"`) {
		t.Fatalf("spider-x was not preserved:\n%s", got)
	}
	if !strings.Contains(got, `encryption: "none"`) {
		t.Fatalf("encryption was not preserved:\n%s", got)
	}
}

func TestTransformStashSubscription(t *testing.T) {
	input := "proxies:\n    - name: VN-1\n      type: vless\n      servername: example.com\n    - name: DE-1\n      type: vless\n      servername: example.org\nproxy-groups:\n    - name: 广告拦截\n      type: select\n      proxies:\n        - DIRECT\n    - name: 欧洲策略\n      type: url-test\n      proxies:\n        - DE-1\n      url: https://www.gstatic.com/generate_204\n    - name: 东南亚策略\n      type: select\n      proxies:\n        - VN-1\nrules:\n    - MATCH,DIRECT\n"
	got := string(transformStashSubscription([]byte(input), "https://rules.example/_assets/icons/"))
	for _, expected := range []string{"servername: example.com", `name: "🇻🇳 VN-1"`, `- "🇩🇪 DE-1"`, "icon: https://rules.example/_assets/icons/Global.png"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("missing %q in:\n%s", expected, got)
		}
	}
	if strings.Index(got, "name: 欧洲策略") > strings.Index(got, "name: 广告拦截") {
		t.Fatal("country groups should be first")
	}
	if strings.Contains(got, "\n      sni:") {
		t.Fatal("Stash VLESS Reality output must keep servername")
	}
	if !strings.Contains(got, "url: http://www.apple.com/") || strings.Contains(got, "gstatic.com") {
		t.Fatal("Stash health checks must use the Stash-compatible Apple endpoint")
	}
	if !strings.Contains(got, "    - MATCH,漏网之鱼") || !strings.Contains(got, "behavior: classical") {
		t.Fatal("Stash transformation must install native rule providers")
	}
	if strings.Index(got, "media:\n") > strings.Index(got, "google-domain:\n") {
		t.Fatal("international media rules must be evaluated before Google rules")
	}
}
