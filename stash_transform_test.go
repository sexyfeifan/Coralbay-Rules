package main

import (
	"strings"
	"testing"
)

func TestTransformStashSubscription(t *testing.T) {
	input := "proxies:\n    - name: VN-1\n      type: vless\n      servername: example.com\n    - name: DE-1\n      type: vless\n      servername: example.org\nproxy-groups:\n    - name: 广告拦截\n      type: select\n      proxies:\n        - DIRECT\n    - name: 欧洲策略\n      type: select\n      proxies:\n        - DE-1\n    - name: 东南亚策略\n      type: select\n      proxies:\n        - VN-1\nrules:\n    - MATCH,DIRECT\n"
	got := string(transformStashSubscription([]byte(input), "https://rules.example/_assets/icons/"))
	for _, expected := range []string{"sni: example.com", `name: "🇻🇳 VN-1"`, `- "🇩🇪 DE-1"`, "icon: https://rules.example/_assets/icons/Global.png"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("missing %q in:\n%s", expected, got)
		}
	}
	if strings.Index(got, "name: 欧洲策略") > strings.Index(got, "name: 广告拦截") {
		t.Fatal("country groups should be first")
	}
	if strings.Contains(got, "servername:") {
		t.Fatal("Stash output must use sni")
	}
}
