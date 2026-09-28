package main

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Emit native text rule sets, not YAML payloads or independently curated geo
// lists. Loon carries no-resolve on IP entries; Surge carries it on RULE-SET.
func miaomiaowuClientRules(client, behavior string, entries []string, noResolve bool) ([]byte, error) {
	if miaomiaowuClientExtension(client) == "" || len(entries) < 1 || len(entries) > 1000000 || noResolve && behavior != "ipcidr" {
		return nil, fmt.Errorf("客户端规则类型、条目或 no-resolve 语义无效")
	}
	var result strings.Builder
	for _, entry := range entries {
		rule, err := nativeMRSRule(behavior, entry, "CoralBayPayload", false)
		if err != nil {
			return nil, err
		}
		result.WriteString(strings.TrimSuffix(rule, ",CoralBayPayload"))
		if client == "loon" && noResolve {
			result.WriteString(",no-resolve")
		}
		result.WriteByte('\n')
		if result.Len() > miaomiaowuClientMaxBytes {
			return nil, fmt.Errorf("客户端文本规则超过大小上限")
		}
	}
	return []byte(result.String()), nil
}

// The empty Proxy section is intentional: Miaomiaowu injects real nodes there.
// Surge's native include-all-proxies and Loon's NameRegex remote filter select
// those nodes. YAML-only placeholder strings would become invalid INI policies.
func (s *server) miaomiaowuClientConfig(input []byte, resources legacyResourceManifest, client, source string) ([]byte, map[string]bool, error) {
	fail := func(reason string) ([]byte, map[string]bool, error) {
		return nil, nil, fmt.Errorf("客户端模板适配失败：%s", reason)
	}
	if miaomiaowuClientExtension(client) == "" {
		return fail("不支持的客户端")
	}
	adapted, _, _, _, err := s.miaomiaowuConfig(input, resources, "local")
	if err != nil {
		return nil, nil, err
	}
	if !miaomiaowuSourceValid(source) {
		return fail("不支持的来源")
	}
	var layout struct {
		Groups []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
		Providers map[string]struct {
			URL      string `yaml:"url"`
			Behavior string `yaml:"behavior"`
		} `yaml:"rule-providers"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(adapted, &layout); err != nil {
		return fail("适配配置无法解析")
	}
	providerPaths := map[string]string{}
	for name, provider := range layout.Providers {
		if name == "" || strings.ContainsAny(name, "\r\n,=") {
			return fail("规则集名称无法用于原生配置")
		}
		path := strings.TrimPrefix(provider.URL, "https://"+s.domain+"/_rule-resources/666os/"+resources.Status.ReleaseID+"/")
		if !legacyKnownPath(path) || provider.Behavior != miaomiaowuRulesetBehavior(path) {
			return fail("规则集没有已验证的 MRS 原件映射")
		}
		providerPaths[name] = path
	}
	noResolve := map[string]bool{}
	remoteRules := []string{}
	final := ""
	for _, rule := range layout.Rules {
		parts := strings.Split(rule, ",")
		if parts[0] == "MATCH" {
			final = "FINAL," + parts[1]
			continue
		}
		path := providerPaths[parts[1]]
		flag := len(parts) == 4
		if flag && miaomiaowuRulesetBehavior(path) != "ipcidr" {
			return fail("域名规则集包含未适配的 no-resolve")
		}
		if previous, exists := noResolve[path]; exists && previous != flag {
			return fail("同一规则集以不同 no-resolve 语义重复使用，需要重新适配")
		}
		noResolve[path] = flag
		address := s.miaomiaowuClientURL(client, resources.Status.ReleaseID, source, miaomiaowuRulesetID(path)+".list")
		if client == "surge" {
			line := "RULE-SET," + address + "," + parts[2]
			if flag {
				line += ",no-resolve"
			}
			remoteRules = append(remoteRules, line)
		} else {
			remoteRules = append(remoteRules, address+", policy="+parts[2]+", tag="+parts[1]+", enabled=true")
		}
	}
	var output strings.Builder
	output.WriteString("# CoralBay / 妙妙屋 " + strings.ToUpper(client[:1]) + client[1:] + " 原生模板\n")
	output.WriteString("# 规则来源：666OS / YYDS Pro_cn；33 个 MRS 原件精确转换为文本，28 项规则集引用保持原序，另加 FINAL 兜底。\n")
	output.WriteString("# 17 个业务分流组保留名称及直连/代理/广告默认；地区策略适配为全球手动、全球自动与故障转移。\n")
	output.WriteString("# 模板及转换文本由 CoralBay 固定托管；来源 " + source + "，规则提交 " + resources.Status.Commit + "。\n")
	output.WriteString("# 节点由妙妙屋生成订阅时注入；本模板不含节点、订阅链接、控制器凭据或 YAML 占位符。\n")
	if client == "surge" {
		output.WriteString("# 当前妙妙屋 Surge 节点转换器不输出 VLESS；请在妙妙屋选择其可转换且客户端支持的节点。\n\n[General]\nproxy-test-url = https://cp.cloudflare.com/generate_204\n")
	} else {
		output.WriteString("# Loon 原生域名/IP 匹配优先级与 Mihomo 不同；保留源引用顺序不代表逐条执行行为完全一致。\n\n[General]\n")
	}
	output.WriteString("\n[Proxy]\n\n[Proxy Group]\n")
	for _, group := range layout.Groups {
		if group.Name == "全球手动" || group.Name == "全球自动" || group.Name == "故障转移" {
			continue
		}
		output.WriteString(group.Name + " = select, " + strings.Join(group.Proxies, ", ") + "\n")
	}
	if client == "surge" {
		output.WriteString("全球手动 = select, 全球自动, 故障转移, include-all-proxies=true\n")
		output.WriteString("全球自动 = url-test, include-all-proxies=true, interval=300, tolerance=50\n")
		output.WriteString("故障转移 = fallback, include-all-proxies=true, interval=300\n")
		output.WriteString("\n[Rule]\n" + strings.Join(remoteRules, "\n") + "\n" + final + "\n")
	} else {
		output.WriteString("全球手动 = select, 全球自动, 故障转移, 全部节点\n")
		output.WriteString("全球自动 = url-test, 全部节点, url=https://cp.cloudflare.com/generate_204, interval=300, tolerance=50\n")
		output.WriteString("故障转移 = fallback, 全部节点, url=https://cp.cloudflare.com/generate_204, interval=300, max-timeout=2000\n")
		output.WriteString("\n[Remote Filter]\n全部节点 = NameRegex, FilterKey=\".*\"\n")
		output.WriteString("\n[Remote Rule]\n" + strings.Join(remoteRules, "\n") + "\n\n[Rule]\n" + final + "\n")
	}
	return []byte(output.String()), noResolve, nil
}
