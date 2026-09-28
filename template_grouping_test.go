package main

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func groupingBaseConfig(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("templates/openclash/Pro_cn.upstream.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func groupingGroupMap(cfg map[string]any) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, raw := range cfg["proxy-groups"].([]any) {
		group := raw.(map[string]any)
		result[group["name"].(string)] = group
	}
	return result
}

func TestTemplateGroupingCoversEveryNodeExactlyOnce(t *testing.T) {
	p := defaultTemplateGroupingProfile()
	p.Regions = append(p.Regions, "de", "ca")
	names := map[string]string{
		"香港01": "hk", "TW-02": "tw", "🇯🇵 Tokyo 3": "jp", "美国西雅图 4": "us", "🇸🇬 Singapore": "sg", "韩国1": "kr",
		"德国法兰克福": "de", "Canada CA-01": "ca", "越南 VN-02": "asia", "印度1": "asia", "🇬🇧 London": "europe", "墨西哥": "north-america",
		"巴西1": "south-america", "澳大利亚": "oceania", "南非1": "africa", "🇸🇹 冷门节点": "africa", "🇻🇺 岛屿": "oceania", "mystery-01": "other", "香港中转日本": "hk",
		"🌍 No location": "other", "node in transit": "other", "just it": "other", "choose us": "other", "NO-01": "europe", "no-02": "europe", "jp01": "jp", "🇳🇴": "europe", "Norway test": "europe",
	}
	buckets := templateGroupingBuckets(p)
	for name, expected := range names {
		var matches []string
		for _, bucket := range buckets {
			include := regexp.MustCompile(bucket.Filter).MatchString(name)
			exclude := bucket.ExcludeFilter != "" && regexp.MustCompile(bucket.ExcludeFilter).MatchString(name)
			if include && !exclude {
				matches = append(matches, bucket.Code)
			}
		}
		if !reflect.DeepEqual(matches, []string{expected}) {
			t.Errorf("%q expected only %s, got %v", name, expected, matches)
		}
	}
	preview, err := templateGroupingPreview(p, []string{"香港中转日本", "mystery-01", "", "加拿大", "🇻🇺"})
	if err != nil {
		t.Fatal(err)
	}
	result := preview.(map[string]any)
	if result["total"] != 4 || result["covered"] != 4 || result["unknown"] != 1 || result["ambiguous"] != 1 {
		t.Fatalf("coverage diagnostics incorrect: %+v", result)
	}
}

func TestTemplateGroupingManualOverridesUseSameRulesAsPreview(t *testing.T) {
	p := defaultTemplateGroupingProfile()
	p.Overrides = []templateGroupingOverride{{Pattern: "香港中转日本", Region: "jp"}, {Pattern: "private-", Region: "europe"}}
	preview, err := templateGroupingPreview(p, []string{"香港中转日本", "private-01", "香港1", "泰国1"})
	if err != nil {
		t.Fatal(err)
	}
	rows := preview.(map[string]any)["nodes"].([]map[string]any)
	for i, expected := range []string{"jp", "europe", "hk", "asia"} {
		if rows[i]["region"] != expected {
			t.Fatalf("override/regular classification mismatch: %+v", rows[i])
		}
	}
	out, err := applyTemplateGrouping(groupingBaseConfig(t), p, "miaomiaowu", "rules.example.com")
	if err != nil {
		t.Fatal(err)
	}
	groups := groupingGroupMap(out)
	for _, raw := range preview.(map[string]any)["regions"].([]templateGroupingBucket) {
		group := groups[raw.Name+"手动"]
		if group["filter"] != raw.Filter || raw.ExcludeFilter != "" && group["exclude-filter"] != raw.ExcludeFilter {
			t.Fatalf("preview/export matching differs for %s", raw.Name)
		}
	}
}

func TestTemplateGroupingNodePayloadUntouchedAndNoNestedRegionalLeaves(t *testing.T) {
	base := groupingBaseConfig(t)
	base["proxies"] = []any{map[string]any{"name": "越南01", "type": "vless", "server": "192.0.2.1", "port": 443, "uuid": "00000000-0000-0000-0000-000000000001", "flow": "xtls-rprx-vision", "tls": true, "servername": "example.com", "reality-opts": map[string]any{"public-key": "fixture", "short-id": "0123456789abcdef"}}, map[string]any{"name": "德国01", "type": "ss", "cipher": "aes-128-gcm", "password": "fixture"}}
	base["proxy-providers"] = map[string]any{"source": map[string]any{"type": "http", "url": "https://example.invalid/sub?fixture=unchanged"}}
	before, _ := json.Marshal(base)
	p := defaultTemplateGroupingProfile()
	p.Default = "日本手动"
	for _, mode := range []string{"miaomiaowu", "mihomo", "ppanel"} {
		out, err := applyTemplateGrouping(base, p, mode, "rules.example.com")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out["proxies"], base["proxies"]) || !reflect.DeepEqual(out["proxy-providers"], base["proxy-providers"]) {
			t.Fatal("connection information was rewritten")
		}
		if !reflect.DeepEqual(out["rules"], base["rules"]) || !reflect.DeepEqual(out["rule-providers"], base["rule-providers"]) {
			t.Fatal("default grouping changed source routing or providers")
		}
		groups := groupingGroupMap(out)
		if groups["默认出口"]["proxies"].([]any)[0] != "日本手动" {
			t.Fatal("default not applied")
		}
		for _, bucket := range templateGroupingBuckets(p) {
			for _, suffix := range []string{"手动", "自动", "均衡"} {
				group := groups[bucket.Name+suffix]
				if group["empty-fallback"] != "REJECT" {
					t.Fatal("empty regional group must not silently use a direct connection")
				}
				for _, ref := range templateGroupingRefs(group) {
					if _, exists := groups[ref]; exists {
						t.Fatalf("region %s references parent %s, unsafe for empty pruning", bucket.Name, ref)
					}
				}
			}
		}
		for _, name := range miaomiaowuBusinessNames {
			choices := templateGroupingRefs(groups[name])
			for _, required := range []string{"全球自动", "全球手动", "默认出口", "日本手动", "亚洲其他均衡", "其他未识别手动", "DIRECT"} {
				if !templateGroupingHas(choices, required) {
					t.Fatalf("%s missing choice %s", name, required)
				}
			}
			marker := "越南01"
			if mode == "miaomiaowu" {
				marker = "__PROXY_NODES__"
			} else if mode == "ppanel" {
				marker = "__CORALBAY_PROXY_NODES__"
			}
			if indexGroupingChoice(choices, marker) < 0 || indexGroupingChoice(choices, marker) > indexGroupingChoice(choices, "香港自动") {
				t.Fatalf("%s %s nodes do not precede regions: %v", mode, name, choices)
			}
		}
		templateGroupingAssertGraph(t, out)
	}
	after, _ := json.Marshal(base)
	if string(before) != string(after) {
		t.Fatal("generator mutated source config")
	}
}

func templateGroupingRefs(group map[string]any) []string {
	result := []string{}
	if refs, ok := group["proxies"].([]any); ok {
		for _, ref := range refs {
			result = append(result, ref.(string))
		}
	}
	return result
}

func indexGroupingChoice(options []string, name string) int {
	for i, candidate := range options {
		if candidate == name {
			return i
		}
	}
	return -1
}

func templateGroupingAssertGraph(t *testing.T, cfg map[string]any) {
	t.Helper()
	groups := groupingGroupMap(cfg)
	var visit func(string, map[string]bool)
	visit = func(name string, trail map[string]bool) {
		if trail[name] {
			t.Fatalf("cycle at %s", name)
		}
		trail[name] = true
		for _, ref := range templateGroupingRefs(groups[name]) {
			if _, exists := groups[ref]; exists {
				visit(ref, trail)
			}
		}
		delete(trail, name)
	}
	for name := range groups {
		visit(name, map[string]bool{})
	}
	for _, raw := range cfg["rules"].([]any) {
		parts := strings.Split(raw.(string), ",")
		target := len(parts) - 1
		if parts[target] == "no-resolve" {
			target--
		}
		if !templateGroupingHas([]string{"DIRECT", "REJECT", "REJECT-DROP"}, parts[target]) && groups[parts[target]] == nil {
			t.Fatalf("rule has dangling target: %s", raw)
		}
	}
}

func TestTemplateGroupingMediaOrderDisabledGroupsAndAdvancedSettings(t *testing.T) {
	base := groupingBaseConfig(t)
	p := defaultTemplateGroupingProfile()
	p.Media = append([]string{}, templateGroupingMediaNames...)
	p.DNSMode, p.IPv6, p.Sniffer = "redir-host", "on", "off"
	p.ShowNodes, p.Icons = false, false
	for i := range p.Categories {
		if p.Categories[i].Name == "谷歌服务" {
			p.Categories[i].Enabled = false
		}
	}
	out, err := applyTemplateGrouping(base, p, "mihomo", "rules.example.com")
	if err != nil {
		t.Fatal(err)
	}
	groups := groupingGroupMap(out)
	if groups["谷歌服务"] != nil || groups["YouTube"] == nil {
		t.Fatal("category availability was not applied")
	}
	rules := []string{}
	for _, raw := range out["rules"].([]any) {
		rules = append(rules, raw.(string))
	}
	if indexGroupingChoice(rules, "RULE-SET,YouTube,YouTube") > indexGroupingChoice(rules, "RULE-SET,Streaming,国际媒体") || indexGroupingChoice(rules, "RULE-SET,NetflixIP,Netflix,no-resolve") < 0 || indexGroupingChoice(rules, "RULE-SET,Google,默认出口") < 0 {
		t.Fatal("media precedence or disabled category fallback incorrect")
	}
	if out["ipv6"] != true || out["dns"].(map[string]any)["enhanced-mode"] != "redir-host" || out["sniffer"].(map[string]any)["enable"] != false {
		t.Fatal("advanced settings not applied")
	}
	for _, group := range groups {
		if _, ok := group["icon"]; ok {
			t.Fatal("icons should be disabled")
		}
	}
	if _, exists := groups["国际媒体"]["include-all"]; exists {
		t.Fatal("business single-node setting ignored")
	}
	templateGroupingAssertGraph(t, out)
	delete(base["rule-providers"].(map[string]any), "YouTube")
	if _, err := applyTemplateGrouping(base, p, "mihomo", "rules.example.com"); err == nil {
		t.Fatal("missing media resource was silently accepted")
	}
}

func TestTemplateGroupingRejectsInvalidProfile(t *testing.T) {
	for name, change := range map[string]func(*templateGroupingProfile){
		"duplicate-country": func(p *templateGroupingProfile) { p.Regions = []string{"jp", "jp"} },
		"unknown-country":   func(p *templateGroupingProfile) { p.Regions = []string{"invalid"} },
		"cycle-default":     func(p *templateGroupingProfile) { p.Default = "默认出口" },
		"missing-default":   func(p *templateGroupingProfile) { p.Default = "德国自动" },
		"lookahead": func(p *templateGroupingProfile) {
			p.Overrides = []templateGroupingOverride{{Pattern: "^(?!JP)", Region: "jp"}}
		},
		"empty-regex": func(p *templateGroupingProfile) {
			p.Overrides = []templateGroupingOverride{{Pattern: ".*", Region: "jp"}}
		},
		"other-override": func(p *templateGroupingProfile) {
			p.Overrides = []templateGroupingOverride{{Pattern: "test", Region: "other"}}
		},
		"test-url":          func(p *templateGroupingProfile) { p.TestURL = "file:///tmp/example" },
		"negative-interval": func(p *templateGroupingProfile) { p.Interval = -1 },
		"unknown-strategy":  func(p *templateGroupingProfile) { p.Strategy = "random" },
		"category-cycle":    func(p *templateGroupingProfile) { p.Categories[0].Default = "广告拦截" },
	} {
		t.Run(name, func(t *testing.T) {
			profile := defaultTemplateGroupingProfile()
			change(&profile)
			if _, err := normalizeTemplateGroupingProfile(profile); err == nil {
				t.Fatal("invalid settings accepted")
			}
		})
	}
	preview, err := templateGroupingPreview(defaultTemplateGroupingProfile(), nil)
	if err != nil || preview.(map[string]any)["total"] != 0 {
		t.Fatal("empty preview should not invent coverage", err)
	}
}
