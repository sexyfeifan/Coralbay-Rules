package main

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed templates/subconverter/mihomopro.ini
var builtinConversionINI string

// Only the authenticated converter proxy serves this exact, embedded file.
// No public DNS, user URL, mutable file or node credential is involved.
const conversionGroupsURL = "http://coralbay-rules.internal/mihomopro-groups-v1.ini"

func conversionGroupsINI() string {
	var lines []string
	for _, line := range strings.Split(builtinConversionINI, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "ruleset=") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n") + "\nruleset=漏网之鱼,[]FINAL\n"
}

func serveConversionGroups(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Host != "coralbay-rules.internal" {
		return false
	}
	if r.URL.String() != conversionGroupsURL {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(conversionGroupsINI()))
	}
	return true
}

type subscriptionRuleOption struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Available   bool     `json:"available"`
	Reason      string   `json:"reason,omitempty"`
	Description string   `json:"description"`
	Targets     []string `json:"targets"`
}

type subscriptionRuleResource struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Count  int    `json:"count"`
	Used   bool   `json:"used"`
}

type subscriptionRuleMetadata struct {
	Library      string                     `json:"library"`
	Source       string                     `json:"source"`
	ActualSource string                     `json:"actual_source"`
	Revision     string                     `json:"revision"`
	RuleRevision string                     `json:"rule_revision"`
	GeneratedAt  string                     `json:"generated_at"`
	Delivery     string                     `json:"delivery"`
	Count        int                        `json:"count"`
	Uncovered    int                        `json:"uncovered"`
	Resources    []subscriptionRuleResource `json:"resources"`
	Warnings     []string                   `json:"warnings"`
}

func (s *server) ordinaryRuleOptions(builtin bool) []subscriptionRuleOption {
	reason := "此配置尚未适配分流规则来源，请使用原有方式"
	if builtin {
		reason = ""
		if _, err := s.retainLegacyResources(); err != nil {
			reason = "本地完整规则尚未验证，请先同步 666OS 规则"
		}
		if s.mrsDecoder == nil {
			if _, err := os.Stat("/usr/local/bin/coralbay-probe-core"); err != nil {
				reason = "MRS 解码内核不可用，请使用官方完整镜像"
			}
		}
	}
	return []subscriptionRuleOption{
		{ID: "local", Label: "本机镜像", Available: reason == "", Reason: reason, Description: "生成时读取本机原始 MRS，同版本规则内嵌到结果", Targets: []string{"clash", "stash"}},
		{ID: "upstream", Label: "上游源", Available: reason == "", Reason: reason, Description: "生成时读取固定提交的上游 MRS，校验后内嵌到结果", Targets: []string{"clash", "stash"}},
	}
}

func nativeMRSRule(behavior, entry, policy string, noResolve bool) (string, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" || strings.ContainsAny(entry, ",\r\n\t ") {
		return "", fmt.Errorf("MRS 含无法保持语义的条目")
	}
	if behavior == "ipcidr" {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return "", fmt.Errorf("MRS IP 条目不是有效网段：%s", entry)
		}
		kind := "IP-CIDR"
		if prefix.Addr().Is6() {
			kind = "IP-CIDR6"
		}
		rule := kind + "," + entry + "," + policy
		if noResolve {
			rule += ",no-resolve"
		}
		return rule, nil
	}
	if behavior != "domain" {
		return "", fmt.Errorf("不支持的 MRS 类型")
	}
	kind := "DOMAIN"
	if strings.HasPrefix(entry, "+.") {
		kind, entry = "DOMAIN-SUFFIX", strings.TrimPrefix(entry, "+.")
	}
	if entry == "" || strings.HasPrefix(entry, ".") || strings.ContainsAny(entry, "*+/#:()") {
		return "", fmt.Errorf("MRS 域名通配表达式尚未适配：%s", entry)
	}
	return kind + "," + entry + "," + policy, nil
}

// Capture one retained release before any fetch. Both choices require the same
// verified baseline and upstream bytes must match it; there is no fallback.
func (s *server) prepareOrdinaryRules(ctx context.Context, source string) ([]string, *subscriptionRuleMetadata, error) {
	manifest, err := s.retainLegacyResources()
	if err != nil {
		return nil, nil, fmt.Errorf("规则版本不可用，请同步或修复 666OS 规则：%w", err)
	}
	config, err := s.legacyVerifiedResource(manifest, "_templates/MihomoPro.yaml")
	if err != nil {
		return nil, nil, err
	}
	var layout struct {
		Rules     []string `yaml:"rules"`
		Providers map[string]struct {
			URL      string `yaml:"url"`
			Behavior string `yaml:"behavior"`
			Format   string `yaml:"format"`
		} `yaml:"rule-providers"`
	}
	if yaml.Unmarshal(config, &layout) != nil || len(layout.Providers) != len(legacyResourcePaths()) || len(layout.Rules) < 2 {
		return nil, nil, fmt.Errorf("MihomoPro 规则模板不完整")
	}
	type decodedSet struct {
		behavior string
		entries  []string
		resource subscriptionRuleResource
	}
	sets := make(map[string]decodedSet, len(layout.Providers))
	var mu sync.Mutex
	var firstErr error
	slots := make(chan struct{}, 4)
	var workers sync.WaitGroup
	paths := make(map[string]bool)
	for id, provider := range layout.Providers {
		path := ""
		for _, candidate := range legacyResourcePaths() {
			if strings.HasSuffix(provider.URL, "/"+candidate) {
				path = candidate
				break
			}
		}
		if path == "" || paths[path] || provider.Format != "mrs" || (provider.Behavior != "domain" && provider.Behavior != "ipcidr") {
			return nil, nil, fmt.Errorf("规则提供者 %s 无完整原件映射", id)
		}
		paths[path] = true
	}
	for id, provider := range layout.Providers {
		path := ""
		for candidate := range paths {
			if strings.HasSuffix(provider.URL, "/"+candidate) {
				path = candidate
				break
			}
		}
		workers.Add(1)
		go func(id, path, behavior string) {
			defer workers.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			var raw []byte
			var err error
			address := "https://" + s.domain + "/_rule-resources/666os/" + manifest.Status.ReleaseID + "/" + path
			if source == "local" {
				raw, err = s.legacyVerifiedResource(manifest, path)
			} else {
				address = "https://raw.githubusercontent.com/666OS/rules/" + manifest.Status.Commit + "/" + path
				raw, err = s.legacyFetch(ctx, address)
				meta := manifest.Files[path]
				if err == nil && (int64(len(raw)) != meta.Bytes || routingSHA256(raw) != meta.SHA256) {
					err = fmt.Errorf("上游原件与所选版本摘要不一致")
				}
			}
			var entries []string
			if err == nil {
				entries, err = s.decodedMRS(ctx, behavior, raw)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s：%w", path, err)
				}
				return
			}
			sets[id] = decodedSet{behavior, entries, subscriptionRuleResource{ID: id, Source: source, URL: address, SHA256: manifest.Files[path].SHA256, Count: len(entries)}}
		}(id, path, provider.Behavior)
	}
	workers.Wait()
	if firstErr != nil {
		return nil, nil, firstErr
	}
	metadata := &subscriptionRuleMetadata{Library: "666os", Source: source, ActualSource: source, Revision: manifest.Status.ReleaseID, RuleRevision: manifest.Status.Commit, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Delivery: "inline", Warnings: []string{}, Resources: []subscriptionRuleResource{}}
	var rules []string
	used := map[string]bool{}
	for i, rule := range layout.Rules {
		parts := strings.Split(rule, ",")
		if len(parts) == 2 && parts[0] == "MATCH" && i == len(layout.Rules)-1 {
			rules = append(rules, rule)
			continue
		}
		if len(parts) < 3 || len(parts) > 4 || parts[0] != "RULE-SET" || (len(parts) == 4 && parts[3] != "no-resolve") {
			return nil, nil, fmt.Errorf("MihomoPro 新规则尚未适配：%s", rule)
		}
		set, ok := sets[parts[1]]
		if !ok {
			return nil, nil, fmt.Errorf("规则引用缺少原件：%s", parts[1])
		}
		used[parts[1]] = true
		for _, entry := range set.entries {
			native, err := nativeMRSRule(set.behavior, entry, parts[2], len(parts) == 4)
			if err != nil {
				return nil, nil, fmt.Errorf("%s：%w", parts[1], err)
			}
			rules = append(rules, native)
		}
	}
	if len(rules) < 2 || !strings.HasPrefix(rules[len(rules)-1], "MATCH,") {
		return nil, nil, fmt.Errorf("完整分流必须包含最终兜底")
	}
	// Deterministic order for provenance as well as the final rules.
	for _, path := range legacyResourcePaths() {
		for id, set := range sets {
			if strings.HasSuffix(set.resource.URL, "/"+path) {
				resource := set.resource
				resource.Used = used[id]
				metadata.Resources = append(metadata.Resources, resource)
			}
		}
	}
	metadata.Count = len(rules)
	return rules, metadata, nil
}

func (s *server) convertSubscriptionWithMetadata(ctx context.Context, params url.Values) ([]byte, http.Header, *subscriptionRuleMetadata, error) {
	source := params.Get("rule_source")
	if source == "" {
		content, headers, err := s.convertSubscriptionBackend(ctx, params)
		return content, headers, nil, err
	}
	if len(params["rule_source"]) != 1 || (source != "local" && source != "upstream") {
		return nil, nil, nil, fmt.Errorf("分流规则来源无效")
	}
	if params.Get("config") != "https://"+s.domain+"/_configs/coralbay-mihomopro.ini" || (params.Get("target") != "clash" && params.Get("target") != "stash") || params.Get("list") == "true" {
		return nil, nil, nil, fmt.Errorf("此分流规则来源只适用于内置 MihomoPro 的 Clash/Mihomo 或 Stash 完整配置，请选择原有方式")
	}
	rules, metadata, err := s.prepareOrdinaryRules(ctx, source)
	if err != nil {
		return nil, nil, nil, err
	}
	backendParams := cloneURLValues(params)
	backendParams.Set("config", conversionGroupsURL)
	backendParams.Set("expand", "true")
	backendParams.Set("new_name", "true")
	content, headers, err := s.convertSubscriptionBackend(ctx, backendParams)
	if err != nil {
		return nil, nil, nil, err
	}
	if params.Get("target") == "stash" {
		content = transformStashSubscription(content, "https://"+s.domain+"/_assets/icons/")
	}
	var doc map[string]any
	if yaml.Unmarshal(content, &doc) != nil || doc == nil {
		return nil, nil, nil, fmt.Errorf("转换结果不是完整 YAML 配置")
	}
	if params.Get("target") == "stash" {
		if proxies, ok := doc["proxies"].([]any); ok {
			for _, raw := range proxies {
				if node, ok := raw.(map[string]any); ok {
					if err := routingNormalizeNodeAliases(node); err != nil {
						return nil, nil, nil, err
					}
					if err := routingStashNode(node); err != nil {
						return nil, nil, nil, err
					}
				}
			}
		}
		metadata.Warnings = append(metadata.Warnings, "已校验 Stash 字段结构；原生 Stash 客户端仍需实际导入验收")
	}
	doc["rules"] = rules
	delete(doc, "rule-providers")
	content, err = yaml.Marshal(doc)
	if err != nil {
		return nil, nil, nil, err
	}
	if err = validateYAMLReferences(content); err != nil {
		return nil, nil, nil, err
	}
	if len(content) > 16<<20 {
		return nil, nil, nil, fmt.Errorf("完整分流配置超过 16 MiB，未交付截断结果")
	}
	headers.Set("X-CoralBay-Rule-Source", source)
	headers.Set("X-CoralBay-Rule-Revision", metadata.RuleRevision)
	headers.Set("X-CoralBay-Rule-Delivery", "inline")
	return content, headers, metadata, nil
}
