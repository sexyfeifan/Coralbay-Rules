package main

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"net/url"
	"strings"
)

func (s *server) isBuiltInStash(p url.Values) bool {
	if p.Get("list") == "true" {
		return false
	}
	u, err := url.Parse(p.Get("config"))
	return err == nil && u.Scheme == "https" && u.Host == s.domain && u.Path == "/_configs/coralbay-mihomopro.ini" && u.RawQuery == "" && u.User == nil
}
func validateYAMLReferences(body []byte) error {
	var cfg struct {
		Proxies []struct {
			Name string `yaml:"name"`
		} `yaml:"proxies"`
		Groups []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
			Use     []string `yaml:"use"`
		} `yaml:"proxy-groups"`
		Providers     map[string]any `yaml:"proxy-providers"`
		RuleProviders map[string]any `yaml:"rule-providers"`
		Rules         []string       `yaml:"rules"`
	}
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		return fmt.Errorf("YAML 结构解析失败")
	}
	names := map[string]bool{"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true, "COMPATIBLE": true, "GLOBAL": true}
	for _, p := range cfg.Proxies {
		if p.Name == "" || names[p.Name] {
			return fmt.Errorf("节点名称为空或重复")
		}
		names[p.Name] = true
	}
	for _, g := range cfg.Groups {
		if g.Name == "" || names[g.Name] {
			return fmt.Errorf("策略名称为空或重复: %s", g.Name)
		}
		names[g.Name] = true
	}
	for _, g := range cfg.Groups {
		for _, p := range g.Proxies {
			if !names[p] {
				return fmt.Errorf("策略 %s 引用了不存在的节点或策略 %s", g.Name, p)
			}
		}
		for _, p := range g.Use {
			if _, ok := cfg.Providers[p]; !ok {
				return fmt.Errorf("节点集引用不存在: %s", p)
			}
		}
	}
	groups := map[string][]string{}
	for _, g := range cfg.Groups {
		groups[g.Name] = g.Proxies
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(name string) bool {
		if visiting[name] {
			return false
		}
		if done[name] {
			return true
		}
		visiting[name] = true
		for _, ref := range groups[name] {
			if _, ok := groups[ref]; ok && !visit(ref) {
				return false
			}
		}
		visiting[name] = false
		done[name] = true
		return true
	}
	for name := range groups {
		if !visit(name) {
			return fmt.Errorf("策略组存在循环引用: %s", name)
		}
	}
	for _, rule := range cfg.Rules {
		parts := strings.Split(rule, ",")
		if len(parts) < 2 {
			return fmt.Errorf("规则缺少目标策略")
		}
		last := len(parts) - 1
		for last > 0 && (strings.TrimSpace(parts[last]) == "no-resolve" || strings.TrimSpace(parts[last]) == "src") {
			last--
		}
		policy := strings.TrimSpace(parts[last])
		if !names[policy] {
			return fmt.Errorf("规则引用不存在的策略: %s", policy)
		}
		if parts[0] == "RULE-SET" && len(parts) > 2 {
			if _, ok := cfg.RuleProviders[strings.TrimSpace(parts[1])]; !ok {
				return fmt.Errorf("规则集引用不存在: %s", parts[1])
			}
		}
	}
	return nil
}
