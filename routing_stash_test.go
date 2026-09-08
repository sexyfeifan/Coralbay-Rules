package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const routingNativeStashFixture = `proxies:
 - {name: '日本 HY2', type: hysteria2, server: hy2.example.com, port: 443, auth: synthetic, up-speed: 100, down-speed: 0.5, fast-open: true}
 - {name: '日本 TUIC', type: tuic, server: tuic.example.com, port: 443, version: 5, uuid: d0529668-8835-11ec-a8a3-0242ac120002, password: synthetic, alpn: [h3]}
`

func TestRoutingStashNativeAliasesAndOutput(t *testing.T) {
	s := routingBuilderFixtureServer(t, routingNativeStashFixture)
	spec := routingBuilderSpec()
	spec.Rules = []routingRuleChoice{{ID: "google", Action: "proxy"}}
	build, err := s.buildRouting(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	var native, mihomo struct {
		Nodes []map[string]any `yaml:"proxies"`
	}
	if err = yaml.Unmarshal([]byte(build.Outputs["stash"]), &native); err != nil {
		t.Fatal(err)
	}
	if err = yaml.Unmarshal([]byte(build.Outputs["mihomo"]), &mihomo); err != nil {
		t.Fatal(err)
	}
	h, tui := native.Nodes[0], native.Nodes[1]
	if h["auth"] != "synthetic" || h["password"] != nil || h["up-speed"] != 100 || h["down-speed"] != 0.5 || h["up"] != nil || h["down"] != nil || tui["version"] != 5 {
		t.Fatalf("Stash protocol schema mismatch")
	}
	h, tui = mihomo.Nodes[0], mihomo.Nodes[1]
	if h["password"] != "synthetic" || h["auth"] != nil || h["up"] != "100 Mbps" || h["down"] != "0.5 Mbps" || h["up-speed"] != nil || tui["version"] != nil {
		t.Fatalf("native Stash fields leaked into Mihomo")
	}
	validation, err := routingValidationOutput(build.Outputs["stash"], "stash")
	if err != nil {
		t.Fatal(err)
	}
	var checked struct {
		Nodes []map[string]any `yaml:"proxies"`
	}
	if err = yaml.Unmarshal([]byte(validation), &checked); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(checked.Nodes, mihomo.Nodes) {
		t.Fatal("supplemental Mihomo validator did not receive equivalent node parameters")
	}
	if !strings.Contains(strings.Join(build.Warnings, " "), "Stash 原生") {
		t.Fatal("native Stash validation boundary not disclosed")
	}
}

func TestRoutingStashAliasesDetectConflicts(t *testing.T) {
	for _, entry := range []string{
		"{name: hy2, type: hysteria2, server: proxy.example.com, port: 443, auth: one, password: two}",
		"{name: hy2, type: hysteria2, server: proxy.example.com, port: 443, auth: one, up-speed: 100, up: 200}",
		"{name: hy2, type: hysteria2, server: proxy.example.com, port: 443, auth: one, down-speed: -1}",
		"{name: hy2, type: hysteria2, server: proxy.example.com, port: 443, auth: one, up-speed: '100 Mbps'}",
		"{name: hy2, type: hysteria2, server: proxy.example.com, port: 443, auth: {bad: type}}",
		"{name: tuic, type: tuic, server: proxy.example.com, port: 443, version: 4, token: old}",
		"{name: tuic, type: tuic, server: proxy.example.com, port: 443, version: 5, token: old, uuid: d0529668-8835-11ec-a8a3-0242ac120002, password: one}",
	} {
		var node map[string]any
		if err := yaml.Unmarshal([]byte(entry), &node); err != nil {
			t.Fatal(err)
		}
		if err := routingValidateNode(node); err == nil {
			t.Fatalf("conflicting/invalid alias accepted: %s", entry)
		}
	}
}

func TestRoutingStashBandwidthUnitConversionPreservesMihomo(t *testing.T) {
	for _, tc := range []struct {
		up   any
		mbps float64
	}{{100, 100}, {"1 Gbps", 1000}, {"500 Kbps", 0.5}, {"500000 bps", 0.5}} {
		node := map[string]any{"name": "hy2", "type": "hysteria2", "server": "proxy.example.com", "port": 443, "password": "synthetic", "up": tc.up, "auth": "synthetic", "up-speed": tc.mbps}
		if err := routingValidateNode(node); err != nil {
			t.Fatal(err)
		}
		if node["up"] != tc.up || node["auth"] != nil || node["up-speed"] != nil {
			t.Fatal("equivalent aliases changed existing Mihomo value")
		}
		original := map[string]any{}
		for k, v := range node {
			original[k] = v
		}
		output, err := routingRender("stash", []map[string]any{node}, nil, []string{"MATCH,DIRECT"})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(node, original) {
			t.Fatal("Stash render mutated canonical node used by other clients")
		}
		var cfg struct {
			Nodes []map[string]any `yaml:"proxies"`
		}
		if err = yaml.Unmarshal([]byte(output), &cfg); err != nil {
			t.Fatal(err)
		}
		value := cfg.Nodes[0]["up-speed"]
		actual, ok := value.(float64)
		if !ok {
			actual = float64(value.(int))
		}
		if actual != tc.mbps {
			t.Fatalf("bandwidth unit changed: got %v want %v", actual, tc.mbps)
		}
	}
}
