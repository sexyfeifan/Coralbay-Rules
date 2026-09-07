package main

// The routing compiler extracts nodes only. It never imports upstream rules,
// scripts, providers, DNS, or a legacy subconverter preset.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const routingMaxSourceBytes = 8 << 20
const routingMaxNodes = 5000

type routingRegion struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Flag string `json:"flag"`
}

var routingRegionDefinitions = []struct {
	routingRegion
	pattern *regexp.Regexp
}{
	{routingRegion{"HK", "香港", "🇭🇰"}, regexp.MustCompile(`(?i)🇭🇰|香港|hong[ -]*kong|\bHKG?([_ -]|\d|\b)`)},
	{routingRegion{"TW", "台湾", "🇹🇼"}, regexp.MustCompile(`(?i)🇹🇼|台湾|台灣|台北|taiwan|taipei|\bTW([_ -]|\d|\b)`)},
	{routingRegion{"JP", "日本", "🇯🇵"}, regexp.MustCompile(`(?i)🇯🇵|日本|东京|東京|大阪|japan|tokyo|osaka|\bJP([_ -]|\d|\b)`)},
	{routingRegion{"SG", "新加坡", "🇸🇬"}, regexp.MustCompile(`(?i)🇸🇬|新加坡|狮城|獅城|singapore|\bSG([_ -]|\d|\b)`)},
	{routingRegion{"US", "美国", "🇺🇸"}, regexp.MustCompile(`(?i)🇺🇸|美国|美國|洛杉矶|洛杉磯|硅谷|西雅图|纽约|united[ -]*states|america|los[ -]*angeles|\bUSA?([_ -]|\d|\b)`)},
	{routingRegion{"KR", "韩国", "🇰🇷"}, regexp.MustCompile(`(?i)🇰🇷|韩国|韓國|首尔|首爾|korea|seoul|\bKR([_ -]|\d|\b)`)},
	{routingRegion{"GB", "英国", "🇬🇧"}, regexp.MustCompile(`(?i)🇬🇧|英国|英國|伦敦|倫敦|britain|united[ -]*kingdom|london|\b(GB|UK)([_ -]|\d|\b)`)},
	{routingRegion{"DE", "德国", "🇩🇪"}, regexp.MustCompile(`(?i)🇩🇪|德国|德國|法兰克福|germany|frankfurt|\bDE([_ -]|\d|\b)`)},
	{routingRegion{"FR", "法国", "🇫🇷"}, regexp.MustCompile(`(?i)🇫🇷|法国|法國|巴黎|france|paris|\bFR([_ -]|\d|\b)`)},
	{routingRegion{"NL", "荷兰", "🇳🇱"}, regexp.MustCompile(`(?i)🇳🇱|荷兰|荷蘭|netherlands|amsterdam|\bNL([_ -]|\d|\b)`)},
	{routingRegion{"CA", "加拿大", "🇨🇦"}, regexp.MustCompile(`(?i)🇨🇦|加拿大|canada|toronto|vancouver|\bCA([_ -]|\d|\b)`)},
	{routingRegion{"AU", "澳大利亚", "🇦🇺"}, regexp.MustCompile(`(?i)🇦🇺|澳大利亚|澳洲|悉尼|australia|sydney|\bAU([_ -]|\d|\b)`)},
	{routingRegion{"IN", "印度", "🇮🇳"}, regexp.MustCompile(`(?i)🇮🇳|印度(?:[^尼]|$)|india(?:[^n]|$)|mumbai|\bIN([_ -]|\d|\b)`)},
	{routingRegion{"ID", "印度尼西亚", "🇮🇩"}, regexp.MustCompile(`(?i)🇮🇩|印度尼西亚|印尼|indonesia|jakarta|\bID([_ -]|\d|\b)`)},
	{routingRegion{"TH", "泰国", "🇹🇭"}, regexp.MustCompile(`(?i)🇹🇭|泰国|泰國|曼谷|thailand|bangkok|\bTH([_ -]|\d|\b)`)},
	{routingRegion{"MY", "马来西亚", "🇲🇾"}, regexp.MustCompile(`(?i)🇲🇾|马来西亚|馬來西亞|malaysia|\bMY([_ -]|\d|\b)`)},
	{routingRegion{"VN", "越南", "🇻🇳"}, regexp.MustCompile(`(?i)🇻🇳|越南|vietnam|\bVN([_ -]|\d|\b)`)},
	{routingRegion{"PH", "菲律宾", "🇵🇭"}, regexp.MustCompile(`(?i)🇵🇭|菲律宾|菲律賓|philippines|\bPH([_ -]|\d|\b)`)},
	{routingRegion{"RU", "俄罗斯", "🇷🇺"}, regexp.MustCompile(`(?i)🇷🇺|俄罗斯|俄羅斯|russia|moscow|\bRU([_ -]|\d|\b)`)},
	{routingRegion{"CN", "中国大陆", "🇨🇳"}, regexp.MustCompile(`(?i)🇨🇳|中国|中國|大陆|大陸|北京|上海|china|\bCN([_ -]|\d|\b)`)},
}

func routingRegions() []routingRegion {
	result := make([]routingRegion, 0, len(routingRegionDefinitions)+1)
	for _, item := range routingRegionDefinitions {
		result = append(result, item.routingRegion)
	}
	return append(result, routingRegion{"OTHER", "未识别 / 其他", "🌐"})
}

func routingNodeRegion(name string) string {
	for _, item := range routingRegionDefinitions {
		if item.pattern.MatchString(name) {
			return item.Code
		}
	}
	return "OTHER"
}

func routingHasControl(s string) bool {
	return !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0
}

func validateRoutingSpec(spec *routingProfileSpec) error {
	if spec == nil {
		return fmt.Errorf("缺少分流方案")
	}
	spec.Name = strings.TrimSpace(spec.Name)
	if spec.Name == "" || utf8.RuneCountInString(spec.Name) > 80 || routingHasControl(spec.Name) {
		return fmt.Errorf("方案名称须为 1–80 个字符，不能含控制字符")
	}
	if len(spec.Sources) < 1 || len(spec.Sources) > 8 {
		return fmt.Errorf("请填写 1–8 个原始订阅链接")
	}
	sourceSeen := map[string]bool{}
	for i := range spec.Sources {
		spec.Sources[i] = strings.TrimSpace(spec.Sources[i])
		if err := routingValidateSourceURL(spec.Sources[i]); err != nil {
			return fmt.Errorf("第 %d 个订阅：%w", i+1, err)
		}
		if sourceSeen[spec.Sources[i]] {
			return fmt.Errorf("第 %d 个订阅链接重复", i+1)
		}
		sourceSeen[spec.Sources[i]] = true
	}
	if len(spec.Clients) == 0 {
		spec.Clients = []string{"mihomo"}
	}
	if len(spec.Clients) > 3 {
		return fmt.Errorf("目标客户端过多")
	}
	seen := map[string]bool{}
	for _, client := range spec.Clients {
		if client != "mihomo" && client != "openclash" && client != "stash" {
			return fmt.Errorf("不支持的目标客户端：%s", client)
		}
		if seen[client] {
			return fmt.Errorf("目标客户端重复：%s", client)
		}
		seen[client] = true
	}
	if spec.Strategy == "" {
		spec.Strategy = "select"
	}
	if !routingValidStrategy(spec.Strategy) {
		return fmt.Errorf("全局策略只支持 select、url-test 或 fallback")
	}
	if spec.Match == "" {
		spec.Match = "proxy"
	}
	if spec.Match != "proxy" && spec.Match != "direct" {
		return fmt.Errorf("未命中流量只支持 proxy 或 direct")
	}
	if spec.IntervalHours == 0 {
		spec.IntervalHours = 6
	}
	if spec.IntervalHours < 1 || spec.IntervalHours > 168 {
		return fmt.Errorf("更新间隔须为 1–168 小时")
	}
	if err := validateRoutingFilter(&spec.Global); err != nil {
		return fmt.Errorf("全局筛选：%w", err)
	}
	catalog := map[string]routingRule{}
	for _, r := range routingRuleCatalog() {
		catalog[r.ID] = r
	}
	if len(spec.Rules) == 0 || len(spec.Rules) > len(catalog) {
		return fmt.Errorf("请至少选择一个有效分流规则")
	}
	seen = map[string]bool{}
	for i := range spec.Rules {
		r := &spec.Rules[i]
		entry, ok := catalog[r.ID]
		if !ok {
			return fmt.Errorf("未知规则：%s", r.ID)
		}
		if seen[r.ID] {
			return fmt.Errorf("规则重复：%s", r.ID)
		}
		seen[r.ID] = true
		if r.Action == "" {
			r.Action = entry.DefaultAction
		}
		if r.Action != "proxy" && r.Action != "direct" && r.Action != "reject" {
			return fmt.Errorf("规则 %s 的去向无效", r.ID)
		}
		if r.Strategy != "" && !routingValidStrategy(r.Strategy) {
			return fmt.Errorf("规则 %s 的策略无效", r.ID)
		}
		if r.Filter != nil {
			if err := validateRoutingFilter(r.Filter); err != nil {
				return fmt.Errorf("规则 %s：%w", r.ID, err)
			}
		}
		if r.Action != "proxy" && (r.Filter != nil || r.Strategy != "") {
			return fmt.Errorf("直连或拦截规则 %s 不能设置节点筛选或代理策略", r.ID)
		}
	}
	return nil
}

func routingValidateSourceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || routingHasControl(raw) || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("仅支持无用户认证、无片段的公网 HTTP(S) 订阅链接")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || !strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return fmt.Errorf("禁止访问非公网地址")
	}
	if ip := net.ParseIP(host); ip != nil && !publicIP(ip) {
		return fmt.Errorf("禁止访问非公网地址")
	}
	if u.Port() != "" {
		if port, err := strconv.Atoi(u.Port()); err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("订阅端口无效")
		}
	}
	return nil
}

func routingValidStrategy(strategy string) bool {
	return strategy == "select" || strategy == "url-test" || strategy == "fallback"
}

func validateRoutingFilter(filter *routingFilter) error {
	valid := map[string]bool{}
	for _, region := range routingRegions() {
		valid[region.Code] = true
	}
	for _, list := range [][]string{filter.Regions, filter.ExcludeRegions} {
		seen := map[string]bool{}
		for _, code := range list {
			if !valid[code] || seen[code] {
				return fmt.Errorf("未知或重复地区：%s", code)
			}
			seen[code] = true
		}
	}
	for _, code := range filter.Regions {
		for _, exclude := range filter.ExcludeRegions {
			if code == exclude {
				return fmt.Errorf("地区 %s 不能同时包含和排除", code)
			}
		}
	}
	for _, expression := range []string{filter.Include, filter.Exclude} {
		if len(expression) > 1024 || routingHasControl(expression) {
			return fmt.Errorf("正则最多 1024 字节且不能含控制字符")
		}
		if expression != "" {
			if _, err := regexp.Compile("(?i)" + expression); err != nil {
				return fmt.Errorf("节点筛选正则无效（使用 RE2 语法，不支持环视和反向引用）")
			}
		}
	}
	return nil
}

type routingMatcher struct {
	regions, excludes map[string]bool
	include, exclude  *regexp.Regexp
}

func routingNewMatcher(f routingFilter) routingMatcher {
	m := routingMatcher{regions: map[string]bool{}, excludes: map[string]bool{}}
	for _, c := range f.Regions {
		m.regions[c] = true
	}
	for _, c := range f.ExcludeRegions {
		m.excludes[c] = true
	}
	if f.Include != "" {
		m.include = regexp.MustCompile("(?i)" + f.Include)
	}
	if f.Exclude != "" {
		m.exclude = regexp.MustCompile("(?i)" + f.Exclude)
	}
	return m
}
func (m routingMatcher) matches(name, region string) bool {
	return (len(m.regions) == 0 || m.regions[region]) && !m.excludes[region] && (m.include == nil || m.include.MatchString(name)) && (m.exclude == nil || !m.exclude.MatchString(name))
}

var routingInfoNode = regexp.MustCompile(`(?i)(剩余流量|套餐到期|到期时间|过期时间|流量重置|订阅更新|更新订阅|官网地址|官方网站|剩餘流量|到期時間|traffic[ _-]*remaining|remaining[ _-]*traffic|expire[sd]?[ _-]*(at|time)|reset[ _-]*traffic)`)

type routingNode struct {
	data                     map[string]any
	name, sourceName, region string
}

func (s *server) buildRouting(ctx context.Context, spec routingProfileSpec) (routingBuildResult, error) {
	result := routingBuildResult{Outputs: map[string]string{}, Groups: []routingGroupPreview{}, Nodes: []routingNodePreview{}, Warnings: []string{"节点地区仅由名称推断；不代表实际出口或服务解锁结果。"}}
	if err := validateRoutingSpec(&spec); err != nil {
		return result, err
	}
	client := s.routingHTTPClient
	if client == nil {
		client = safeHTTPClient(30 * time.Second)
	}
	var all []map[string]any
	for i, source := range spec.Sources {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		req.Header.Set("User-Agent", "Clash.Meta/CoralBay-Routing")
		req.Header.Set("Accept", "application/yaml,text/yaml,text/plain,*/*")
		resp, err := client.Do(req)
		if err != nil {
			return result, fmt.Errorf("第 %d 个订阅抓取失败，请检查地址和公网可达性", i+1)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, routingMaxSourceBytes+1))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return result, fmt.Errorf("第 %d 个订阅返回 HTTP %d", i+1, resp.StatusCode)
		}
		if readErr != nil {
			return result, fmt.Errorf("第 %d 个订阅读取失败", i+1)
		}
		if len(body) > routingMaxSourceBytes {
			return result, fmt.Errorf("第 %d 个订阅超过 8 MiB 限制", i+1)
		}
		nodes, warnings, err := routingParseNodes(body)
		if err != nil {
			return result, fmt.Errorf("第 %d 个订阅：%w", i+1, err)
		}
		all = append(all, nodes...)
		result.Warnings = append(result.Warnings, warnings...)
		if len(all) > routingMaxNodes {
			return result, fmt.Errorf("合并后的节点数超过 %d", routingMaxNodes)
		}
		if len(spec.Sources) == 1 {
			result.UsageHeader = resp.Header.Get("Subscription-Userinfo")
		}
	}
	var nodes []routingNode
	names := map[string]bool{"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true, "COMPATIBLE": true, "GLOBAL": true, "CB · GLOBAL": true}
	for _, choice := range spec.Rules {
		names["CB · "+choice.ID] = true
	}
	identities := map[[32]byte]bool{}
	duplicates, renamed := 0, 0
	global := routingNewMatcher(spec.Global)
	for i, node := range all {
		if raw, exists := node["name"]; exists {
			if _, ok := raw.(string); !ok {
				return result, fmt.Errorf("第 %d 个节点名称须为字符串", i+1)
			}
		}
		name, _ := node["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			name = fmt.Sprintf("节点 %d", i+1)
		}
		if routingHasControl(name) || utf8.RuneCountInString(name) > 256 {
			return result, fmt.Errorf("第 %d 个节点名称无效或超过 256 字符", i+1)
		}
		node["name"] = name
		region := routingNodeRegion(name)
		kind, _ := node["type"].(string)
		if routingInfoNode.MatchString(name) {
			result.Nodes = append(result.Nodes, routingNodePreview{Name: name, Type: kind, Region: region, Excluded: "套餐或订阅提示项，始终剔除"})
			continue
		}
		if err := routingValidateNode(node); err != nil {
			return result, fmt.Errorf("节点 %s：%w", name, err)
		}
		identity := make(map[string]any, len(node))
		for k, v := range node {
			if k != "name" {
				identity[k] = v
			}
		}
		encoded, err := json.Marshal(identity)
		if err != nil {
			return result, fmt.Errorf("节点 %s 包含不可序列化字段", name)
		}
		hash := sha256.Sum256(encoded)
		if identities[hash] {
			duplicates++
			continue
		}
		identities[hash] = true
		base := name
		for suffix := 2; names[name]; suffix++ {
			name = fmt.Sprintf("%s (%d)", base, suffix)
		}
		if name != base {
			renamed++
		}
		names[name] = true
		node["name"] = name
		nodes = append(nodes, routingNode{data: node, name: name, sourceName: base, region: region})
		preview := routingNodePreview{Name: name, Type: kind, Region: region}
		if !global.matches(base, region) {
			preview.Excluded = "不匹配全局筛选；独立规则仍可选用"
		}
		result.Nodes = append(result.Nodes, preview)
	}
	if duplicates > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("已合并 %d 个连接参数完全相同的重复节点，保留首次出现的名称。", duplicates))
	}
	if renamed > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("已为 %d 个重名或保留名称节点追加编号；筛选仍匹配上游原始名称。", renamed))
	}
	if len(nodes) == 0 {
		return result, fmt.Errorf("订阅没有可用节点")
	}
	used := map[string]bool{}
	addGroup := func(id, name, strategy string, filter routingFilter) error {
		matcher := routingNewMatcher(filter)
		members := []string{}
		for _, node := range nodes {
			if matcher.matches(node.sourceName, node.region) {
				members = append(members, node.name)
				used[node.name] = true
			}
		}
		if len(members) == 0 {
			return fmt.Errorf("策略 %s 未匹配任何节点，请调整筛选；不会自动改为直连", name)
		}
		result.Groups = append(result.Groups, routingGroupPreview{ID: id, Name: name, Strategy: strategy, Nodes: members})
		return nil
	}
	if spec.Match == "proxy" {
		if err := addGroup("global", "CB · GLOBAL", spec.Strategy, spec.Global); err != nil {
			return result, err
		}
	}
	catalog := map[string]routingRule{}
	for _, rule := range routingRuleCatalog() {
		catalog[rule.ID] = rule
	}
	choices := append([]routingRuleChoice(nil), spec.Rules...)
	// Specific explicit direct exceptions precede broad collections. Keep the
	// catalog's private-network and advertising priorities at the front.
	priority := func(c routingRuleChoice) int {
		p := catalog[c.ID].Priority
		if c.Action == "direct" && p > 100 && p < 400 {
			return 90
		}
		return p
	}
	sort.SliceStable(choices, func(i, j int) bool { return priority(choices[i]) < priority(choices[j]) })
	ids := make([]string, 0, len(choices))
	for _, choice := range choices {
		ids = append(ids, choice.ID)
	}
	result.RuleOrder = append([]string(nil), ids...)
	snapshot, err := s.loadRoutingRuleSnapshot(ctx, ids, false)
	if err != nil {
		return result, err
	}
	result.Revision = snapshot.Revision
	if snapshot.Stale {
		result.Warnings = append(result.Warnings, "规则版本检查 / 同步警告："+snapshot.LastError)
	}
	compiled := []string{}
	for _, choice := range choices {
		policy := strings.ToUpper(choice.Action)
		if choice.Action == "proxy" {
			policy = "CB · " + choice.ID
			filter := spec.Global
			if choice.Filter != nil {
				filter = *choice.Filter
			}
			strategy := choice.Strategy
			if strategy == "" {
				strategy = spec.Strategy
			}
			if err := addGroup(choice.ID, policy, strategy, filter); err != nil {
				return result, err
			}
		}
		lines := snapshot.Rules[choice.ID]
		if len(lines) == 0 {
			return result, fmt.Errorf("规则 %s 的快照为空", choice.ID)
		}
		for _, line := range lines {
			rule, err := routingCompileRule(line, policy)
			if err != nil {
				return result, fmt.Errorf("规则 %s：%w", choice.ID, err)
			}
			compiled = append(compiled, rule)
		}
	}
	matchPolicy := "DIRECT"
	if spec.Match == "proxy" {
		matchPolicy = "CB · GLOBAL"
	}
	compiled = append(compiled, "MATCH,"+matchPolicy)
	var exported []map[string]any
	for _, node := range nodes {
		if used[node.name] {
			exported = append(exported, node.data)
		}
	}
	if len(exported) == 0 { // A fully direct/reject profile still carries its filtered subscription nodes.
		for _, node := range nodes {
			if global.matches(node.sourceName, node.region) {
				exported = append(exported, node.data)
			}
		}
		if len(exported) == 0 {
			return result, fmt.Errorf("全局筛选未匹配任何可交付节点")
		}
	}
	result.NodeCount = len(exported)
	for _, target := range spec.Clients {
		output, err := routingRender(target, exported, result.Groups, compiled)
		if err != nil {
			return result, err
		}
		result.Outputs[target] = output
	}
	validated, err := routingCoreValidate(ctx, result.Outputs)
	if err != nil {
		return result, err
	}
	if !validated {
		result.Warnings = append(result.Warnings, "当前运行环境未安装校验内核，本次完成结构校验；正式镜像会额外执行内核语法检查。")
	}
	result.Warnings = append(result.Warnings, "已验证配置结构、引用和支持范围；请使用新版 Mihomo 内核或 Stash，并在客户端验证实际连接与规则命中。")
	return result, nil
}

func routingCoreValidate(ctx context.Context, outputs map[string]string) (bool, error) {
	const binary = "/usr/local/bin/coralbay-probe-core"
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("无法读取配置校验内核")
	}
	dir, err := os.MkdirTemp("", "coralbay-routing-check-")
	if err != nil {
		return false, fmt.Errorf("无法创建配置校验目录")
	}
	defer os.RemoveAll(dir)
	checked := map[[32]byte]bool{}
	for _, target := range []string{"mihomo", "openclash", "stash"} {
		output, ok := outputs[target]
		if !ok {
			continue
		}
		digest := sha256.Sum256([]byte(output))
		if checked[digest] {
			continue
		}
		file := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(file, []byte(output), 0600); err != nil {
			return false, fmt.Errorf("无法写入待校验配置")
		}
		runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		cmd := exec.CommandContext(runCtx, binary, "-t", "-d", dir, "-f", file)
		// Core errors can contain a full node, password, or entire rule. Do not
		// expose raw stdout/stderr through the API, persisted history, or logs.
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		err = cmd.Run()
		cancel()
		if err != nil {
			return false, fmt.Errorf("%s 的内核配置校验未通过，请检查节点协议、密钥和传输参数；未发布候选配置", target)
		}
		checked[digest] = true
	}
	return true, nil
}

func routingCompileRule(line, policy string) (string, error) {
	kind, payload, ok := strings.Cut(line, ",")
	if !ok || payload == "" || routingHasControl(line) {
		return "", fmt.Errorf("规则格式无效")
	}
	switch kind {
	case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-WILDCARD":
		if strings.Contains(payload, ",") {
			return "", fmt.Errorf("%s 规则含多余参数", kind)
		}
	case "DOMAIN-REGEX":
		var err error
		payload, err = routingInlineRegex(payload)
		if err != nil {
			return "", err
		}
	case "IP-CIDR", "IP-CIDR6":
		cidr := strings.TrimSuffix(payload, ",no-resolve")
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return "", fmt.Errorf("IP 规则格式无效")
		}
		if (kind == "IP-CIDR6") != prefix.Addr().Is6() {
			return "", fmt.Errorf("IP 规则地址族不一致")
		}
		return kind + "," + cidr + "," + policy + ",no-resolve", nil
	default:
		return "", fmt.Errorf("暂不支持规则类型 %s，已阻止发布以避免丢失规则", kind)
	}
	return kind + "," + payload + "," + policy, nil
}

// Native inline rules use commas as delimiters. Expand ranged repetition in
// the regex AST before escaping literal commas; replacing the comma in {0,61}
// directly would silently change the matching language.
func routingInlineRegex(expression string) (string, error) {
	parsed, err := syntax.Parse(expression, syntax.Perl)
	if err != nil {
		return "", fmt.Errorf("不支持的域名正则语法")
	}
	if !strings.Contains(expression, ",") {
		return expression, nil
	}
	var expand func(*syntax.Regexp) *syntax.Regexp
	expand = func(re *syntax.Regexp) *syntax.Regexp {
		copy := *re
		copy.Sub = make([]*syntax.Regexp, len(re.Sub))
		for i, child := range re.Sub {
			copy.Sub[i] = expand(child)
		}
		if copy.Op != syntax.OpRepeat || copy.Min == copy.Max {
			return &copy
		}
		base := copy.Sub[0]
		parts := make([]*syntax.Regexp, 0, copy.Min+1)
		for i := 0; i < copy.Min; i++ {
			parts = append(parts, base)
		}
		if copy.Max < 0 {
			parts = append(parts, &syntax.Regexp{Op: syntax.OpStar, Flags: copy.Flags, Sub: []*syntax.Regexp{base}})
		} else {
			for i := copy.Min; i < copy.Max; i++ {
				parts = append(parts, &syntax.Regexp{Op: syntax.OpQuest, Flags: copy.Flags, Sub: []*syntax.Regexp{base}})
			}
		}
		if len(parts) == 0 {
			return &syntax.Regexp{Op: syntax.OpEmptyMatch}
		}
		if len(parts) == 1 {
			return parts[0]
		}
		return &syntax.Regexp{Op: syntax.OpConcat, Sub: parts}
	}
	result := strings.ReplaceAll(expand(parsed).String(), ",", `\x2c`)
	if len(result) > 65536 {
		return "", fmt.Errorf("域名正则内嵌后过大")
	}
	if _, err := regexp.Compile(result); err != nil {
		return "", fmt.Errorf("域名正则转换失败")
	}
	return result, nil
}

func routingRender(target string, nodes []map[string]any, groups []routingGroupPreview, rules []string) (string, error) {
	proxies := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		copy := map[string]any{}
		for k, v := range node {
			copy[k] = v
		}
		if target == "stash" {
			if err := routingStashNode(copy); err != nil {
				return "", fmt.Errorf("Stash 节点 %s：%w", node["name"], err)
			}
		} else if fingerprint, ok := copy["server-cert-fingerprint"]; ok {
			if existing, exists := copy["fingerprint"]; exists && existing != fingerprint {
				return "", fmt.Errorf("节点 %s 的证书指纹字段冲突", copy["name"])
			}
			copy["fingerprint"] = fingerprint
			delete(copy, "server-cert-fingerprint")
		}
		proxies = append(proxies, copy)
	}
	proxyGroups := []map[string]any{}
	for _, group := range groups {
		g := map[string]any{"name": group.Name, "type": group.Strategy, "proxies": group.Nodes}
		if group.Strategy != "select" {
			g["url"] = "https://www.gstatic.com/generate_204"
			g["interval"] = 300
			if target == "stash" {
				g["url"] = "http://www.apple.com/"
			}
		}
		if group.Strategy == "url-test" {
			g["tolerance"] = 50
		}
		proxyGroups = append(proxyGroups, g)
	}
	cfg := map[string]any{"mode": "rule", "log-level": "info", "ipv6": true, "proxies": proxies, "proxy-groups": proxyGroups, "rules": rules,
		"dns": map[string]any{"enable": true, "ipv6": true, "nameserver": []string{"https://dns.alidns.com/dns-query", "https://1.1.1.1/dns-query"}, "default-nameserver": []string{"223.5.5.5", "1.1.1.1"}}}
	if target != "stash" {
		cfg["mixed-port"] = 7890
		cfg["allow-lan"] = false
		cfg["dns"].(map[string]any)["enhanced-mode"] = "fake-ip"
	}
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("无法生成 %s YAML", target)
	}
	if err := validateYAMLReferences(encoded); err != nil {
		return "", err
	}
	if len(encoded) > 32<<20 {
		return "", fmt.Errorf("生成的配置超过 32 MiB，请减少规则集合")
	}
	return "# CoralBay custom routing · MetaCubeX rules\n" + string(encoded), nil
}

// Both modern clients share these native node fields. Unknown configuration
// features fail closed instead of being silently stripped by a converter.
var routingNodeCommonFields = strings.Fields("name type server port udp ip-version tls servername sni alpn skip-cert-verify client-fingerprint fingerprint server-cert-fingerprint")
var routingNodeProtocolFields = map[string]string{
	"ss":        "cipher password plugin plugin-opts udp-over-tcp udp-over-tcp-version",
	"vmess":     "uuid alterId cipher network ws-opts h2-opts http-opts grpc-opts reality-opts packet-encoding",
	"vless":     "uuid flow encryption network ws-opts h2-opts http-opts grpc-opts reality-opts packet-encoding",
	"trojan":    "password network ws-opts grpc-opts reality-opts",
	"hysteria2": "password up down obfs obfs-password ports hop-interval",
	"tuic":      "uuid password token heartbeat-interval disable-sni reduce-rtt request-timeout udp-relay-mode congestion-controller max-udp-relay-packet-size fast-open max-open-streams",
	"http":      "username password headers",
	"socks5":    "username password",
}

func routingValidateNode(node map[string]any) error {
	kind, ok := node["type"].(string)
	fields, supported := routingNodeProtocolFields[kind]
	if !ok || !supported {
		return fmt.Errorf("暂不支持协议 %v；支持 SS、VMess、VLESS、Trojan、Hysteria2、TUIC、HTTP、SOCKS5 的原生 YAML", node["type"])
	}
	allowed := map[string]bool{}
	for _, key := range append(routingNodeCommonFields, strings.Fields(fields)...) {
		allowed[key] = true
	}
	for key := range node {
		if !allowed[key] {
			return fmt.Errorf("暂不支持字段 %s；为避免丢失连接参数已阻止发布", key)
		}
	}
	for _, key := range strings.Fields("name server uuid password cipher username token sni servername client-fingerprint fingerprint server-cert-fingerprint ip-version network flow encryption packet-encoding plugin obfs obfs-password ports congestion-controller udp-relay-mode") {
		if value, ok := node[key]; ok {
			if str, ok := value.(string); !ok || routingHasControl(str) || len(str) > 16384 {
				return fmt.Errorf("%s 须为有效字符串", key)
			}
		}
	}
	if value, ok := node["alpn"]; ok && !routingStringList(value) {
		return fmt.Errorf("alpn 须为字符串列表")
	}
	for _, key := range strings.Fields("udp-over-tcp-version heartbeat-interval request-timeout max-udp-relay-packet-size max-open-streams hop-interval") {
		if value, ok := node[key]; ok {
			number, err := routingInteger(value)
			if err != nil || number < 0 {
				return fmt.Errorf("%s 须为非负整数", key)
			}
			node[key] = number
		}
	}
	if _, ok := node["uuid"]; ok {
		if !regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`).MatchString(node["uuid"].(string)) {
			return fmt.Errorf("uuid 格式无效，须为标准 UUID")
		}
	}
	if kind == "vmess" || kind == "vless" {
		if alias, ok := node["sni"]; ok {
			if existing, exists := node["servername"]; exists && existing != alias {
				return fmt.Errorf("sni 与 servername 冲突")
			}
			node["servername"] = alias
			delete(node, "sni")
		}
	} else if kind == "trojan" || kind == "hysteria2" || kind == "tuic" {
		if alias, ok := node["servername"]; ok {
			if existing, exists := node["sni"]; exists && existing != alias {
				return fmt.Errorf("sni 与 servername 冲突")
			}
			node["sni"] = alias
			delete(node, "servername")
		}
		if tls, ok := node["tls"]; ok && tls != true {
			return fmt.Errorf("此协议要求 TLS，不能设置 tls=false")
		}
	}
	host, ok := node["server"].(string)
	if !ok || strings.TrimSpace(host) == "" || len(host) > 253 || routingHasControl(host) || strings.ContainsAny(host, " /\\@,") || strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return fmt.Errorf("服务器地址无效")
	}
	port, err := routingInteger(node["port"])
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("端口须为 1–65535")
	}
	node["port"] = port
	for _, key := range strings.Fields("tls udp skip-cert-verify udp-over-tcp disable-sni reduce-rtt fast-open") {
		if value, ok := node[key]; ok {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%s 须为布尔值", key)
			}
		}
	}
	required := ""
	switch kind {
	case "ss":
		required = "cipher password"
	case "vmess", "vless":
		required = "uuid"
	case "trojan", "hysteria2":
		required = "password"
	case "tuic":
		if node["token"] == nil {
			required = "uuid password"
		} else {
			required = "token"
		}
	}
	for _, key := range strings.Fields(required) {
		value, ok := node[key].(string)
		if !ok || value == "" || routingHasControl(value) {
			return fmt.Errorf("缺少或无效的 %s", key)
		}
	}
	if value, ok := node["alterId"]; ok {
		n, err := routingInteger(value)
		if err != nil || n < 0 {
			return fmt.Errorf("alterId 无效")
		}
		node["alterId"] = n
	}
	if netw, ok := node["network"]; ok {
		if netw != "tcp" && netw != "ws" && netw != "grpc" && netw != "h2" && netw != "http" {
			return fmt.Errorf("暂不支持传输方式 %v", netw)
		}
		if kind == "trojan" && netw != "tcp" && netw != "ws" && netw != "grpc" {
			return fmt.Errorf("Trojan 不支持此传输方式")
		}
	}
	if flow, ok := node["flow"]; ok && flow != "" && flow != "xtls-rprx-vision" {
		return fmt.Errorf("暂不支持 VLESS flow %v", flow)
	}
	if flow, _ := node["flow"].(string); flow != "" && node["network"] != nil && node["network"] != "tcp" {
		return fmt.Errorf("XTLS Vision 需要 TCP 传输")
	}
	if flow, _ := node["flow"].(string); flow != "" && node["tls"] != true && (node["encryption"] == nil || node["encryption"] == "" || node["encryption"] == "none") {
		return fmt.Errorf("XTLS Vision 需要 TLS 或 VLESS Encryption")
	}
	if value, ok := node["packet-encoding"]; ok && value != "" && value != "xudp" && value != "packetaddr" && value != "packet" {
		return fmt.Errorf("不支持的 packet-encoding")
	}
	if kind == "hysteria2" {
		for _, key := range []string{"up", "down"} {
			if value, ok := node[key]; ok {
				if number, err := routingInteger(value); err == nil && number >= 0 {
					continue
				}
				str, ok := value.(string)
				if !ok || !regexp.MustCompile(`(?i)^\d+(\.\d+)?\s*[kmg]?bps$`).MatchString(str) {
					return fmt.Errorf("%s 带宽格式无效", key)
				}
			}
		}
		if value, ok := node["ports"].(string); ok {
			for _, entry := range strings.Split(value, ",") {
				parts := strings.Split(strings.TrimSpace(entry), "-")
				if len(parts) > 2 {
					return fmt.Errorf("端口跳跃范围无效")
				}
				previous := 0
				for _, p := range parts {
					port, err := strconv.Atoi(p)
					if err != nil || port < 1 || port > 65535 || port < previous {
						return fmt.Errorf("端口跳跃范围无效")
					}
					previous = port
				}
			}
		}
		if obfs, ok := node["obfs"]; ok && obfs != "" && obfs != "salamander" {
			return fmt.Errorf("首版 Hysteria2 只支持 salamander 混淆")
		}
		if node["obfs"] == "salamander" && (node["obfs-password"] == nil || node["obfs-password"] == "") {
			return fmt.Errorf("salamander 缺少混淆密码")
		}
	}
	for key, fields := range map[string]string{"reality-opts": "public-key short-id spider-x", "ws-opts": "path headers max-early-data early-data-header-name", "h2-opts": "host path", "http-opts": "method path headers", "grpc-opts": "grpc-service-name"} {
		if value, ok := node[key]; ok {
			opts, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%s 须为对象", key)
			}
			valid := map[string]bool{}
			for _, field := range strings.Fields(fields) {
				valid[field] = true
			}
			for field := range opts {
				if !valid[field] {
					return fmt.Errorf("暂不支持 %s.%s", key, field)
				}
				value := opts[field]
				switch field {
				case "headers":
					if err := routingValidateHeaders(value, key == "http-opts"); err != nil {
						return fmt.Errorf("%s.headers：%w", key, err)
					}
				case "max-early-data":
					number, err := routingInteger(value)
					if err != nil || number < 0 {
						return fmt.Errorf("max-early-data 须为非负整数")
					}
					opts[field] = number
				case "host":
					if key == "h2-opts" && !routingStringList(value) {
						return fmt.Errorf("h2-opts.host 须为字符串列表")
					}
				case "path":
					if key == "http-opts" {
						if !routingStringList(value) {
							return fmt.Errorf("http-opts.path 须为字符串列表")
						}
					} else if str, ok := value.(string); !ok || routingHasControl(str) {
						return fmt.Errorf("%s.path 须为字符串", key)
					}
				default:
					if str, ok := value.(string); !ok || routingHasControl(str) {
						return fmt.Errorf("%s.%s 须为字符串", key, field)
					}
				}
			}
			if key == "reality-opts" {
				if node["network"] == "ws" {
					return fmt.Errorf("首版不支持 Reality 与 WebSocket 组合")
				}
				if pub, ok := opts["public-key"].(string); !ok || pub == "" {
					return fmt.Errorf("Reality 缺少 public-key")
				}
				keyBytes, err := base64.RawURLEncoding.DecodeString(opts["public-key"].(string))
				if err != nil || len(keyBytes) != 32 {
					return fmt.Errorf("Reality public-key 格式无效")
				}
				if sid, ok := opts["short-id"]; ok {
					value := sid.(string)
					if len(value) > 16 {
						return fmt.Errorf("Reality short-id 过长")
					}
					if _, err := hex.DecodeString(value); err != nil {
						return fmt.Errorf("Reality short-id 须为偶数位十六进制")
					}
				}
				if kind != "trojan" && node["tls"] != true {
					return fmt.Errorf("Reality 需要启用 tls")
				}
			}
		}
	}
	if plugin, ok := node["plugin"]; ok {
		if plugin != "obfs" && plugin != "v2ray-plugin" {
			return fmt.Errorf("仅支持内置 obfs / v2ray-plugin，不接受外部插件")
		}
		opts, ok := node["plugin-opts"].(map[string]any)
		if !ok {
			return fmt.Errorf("plugin-opts 须为对象")
		}
		fields := "mode host"
		if plugin == "v2ray-plugin" {
			fields += " path tls mux skip-cert-verify headers"
		}
		valid := map[string]bool{}
		for _, field := range strings.Fields(fields) {
			valid[field] = true
		}
		for field, value := range opts {
			if !valid[field] {
				return fmt.Errorf("暂不支持 plugin-opts.%s", field)
			}
			switch field {
			case "tls", "mux", "skip-cert-verify":
				if _, ok := value.(bool); !ok {
					return fmt.Errorf("plugin-opts.%s 须为布尔值", field)
				}
			case "headers":
				if err := routingValidateHeaders(value, false); err != nil {
					return err
				}
			default:
				if str, ok := value.(string); !ok || routingHasControl(str) {
					return fmt.Errorf("plugin-opts.%s 须为字符串", field)
				}
			}
		}
		if plugin == "obfs" && opts["mode"] != "http" && opts["mode"] != "tls" {
			return fmt.Errorf("obfs mode 须为 http 或 tls")
		}
		if plugin == "v2ray-plugin" && opts["mode"] != nil && opts["mode"] != "websocket" {
			return fmt.Errorf("首版 v2ray-plugin 只支持 websocket")
		}
	} else if _, ok := node["plugin-opts"]; ok {
		return fmt.Errorf("plugin-opts 缺少 plugin")
	}
	if value, ok := node["headers"]; ok {
		if err := routingValidateHeaders(value, false); err != nil {
			return err
		}
	}
	return nil
}

func routingStringList(value any) bool {
	if list, ok := value.([]string); ok {
		for _, item := range list {
			if routingHasControl(item) {
				return false
			}
		}
		return true
	}
	list, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range list {
		str, ok := item.(string)
		if !ok || routingHasControl(str) {
			return false
		}
	}
	return true
}

func routingValidateHeaders(value any, listValues bool) error {
	headers, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("请求头须为对象")
	}
	for key, v := range headers {
		if key == "" || routingHasControl(key) || strings.ContainsAny(key, ": ") {
			return fmt.Errorf("请求头名称无效")
		}
		if listValues {
			if !routingStringList(v) {
				return fmt.Errorf("请求头值须为字符串列表")
			}
		} else if str, ok := v.(string); !ok || routingHasControl(str) {
			return fmt.Errorf("请求头值须为字符串")
		}
	}
	return nil
}

func routingStashNode(node map[string]any) error {
	// Preserve the documented VLESS/VMess servername and nested Reality fields.
	// Features with differing schemas are rejected, never opportunistically removed.
	for _, key := range []string{"udp-over-tcp", "udp-over-tcp-version", "ports", "hop-interval", "packet-encoding"} {
		if _, ok := node[key]; ok {
			return fmt.Errorf("当前 Stash 适配未验证字段 %s，请取消 Stash 输出或使用兼容节点", key)
		}
	}
	if fingerprint, ok := node["fingerprint"]; ok {
		if existing, exists := node["server-cert-fingerprint"]; exists && existing != fingerprint {
			return fmt.Errorf("证书指纹字段冲突")
		}
		node["server-cert-fingerprint"] = fingerprint
		delete(node, "fingerprint")
	}
	if node["type"] == "tuic" && node["token"] != nil {
		return fmt.Errorf("Stash 输出仅支持使用 uuid/password 的 TUIC v5")
	}
	return nil
}

func routingInteger(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case string:
		return strconv.Atoi(v)
	case float64:
		if v == float64(int(v)) {
			return int(v), nil
		}
	}
	return 0, fmt.Errorf("非整数")
}

func routingParseNodes(body []byte) ([]map[string]any, []string, error) {
	body = bytes.TrimSpace(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf}))
	if len(body) == 0 {
		return nil, nil, fmt.Errorf("订阅内容为空")
	}
	var cfg map[string]any
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decodeErr := decoder.Decode(&cfg)
	if decodeErr == nil && cfg != nil {
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, nil, fmt.Errorf("订阅不能包含多个 YAML 文档")
		}
		items, ok := cfg["proxies"].([]any)
		if !ok || len(items) == 0 {
			return nil, nil, fmt.Errorf("YAML 订阅须直接包含 proxies 节点列表；首版不展开 proxy-providers")
		}
		if len(items) > routingMaxNodes {
			return nil, nil, fmt.Errorf("订阅节点超过 %d", routingMaxNodes)
		}
		nodes := make([]map[string]any, 0, len(items))
		for _, item := range items {
			node, ok := item.(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("节点列表格式无效")
			}
			nodes = append(nodes, node)
		}
		warnings := []string{}
		if _, exists := cfg["proxy-providers"]; exists {
			return nil, nil, fmt.Errorf("订阅包含 proxy-providers；首版不展开远程节点集，请提供直接包含全部节点的订阅")
		}
		for _, key := range []string{"rules", "proxy-groups", "dns", "script"} {
			if _, exists := cfg[key]; exists {
				warnings = append(warnings, "已仅提取上游 YAML 的节点，原策略、DNS、规则和脚本由新方案取代。")
				break
			}
		}
		return nodes, warnings, nil
	}
	text := string(body)
	if !strings.Contains(text, "://") {
		decoded, err := routingDecodeBase64(strings.Join(strings.Fields(text), ""))
		if err != nil {
			return nil, nil, fmt.Errorf("不支持的订阅格式；请提供 Clash/Stash YAML 或 SS/VMess/VLESS/Trojan/Hysteria2 URI/Base64 订阅")
		}
		text = string(decoded)
	}
	var nodes []map[string]any
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		node, err := routingParseURI(line)
		if err != nil {
			return nil, nil, fmt.Errorf("第 %d 行节点：%w", i+1, err)
		}
		nodes = append(nodes, node)
		if len(nodes) > routingMaxNodes {
			return nil, nil, fmt.Errorf("订阅节点超过 %d", routingMaxNodes)
		}
	}
	if len(nodes) == 0 {
		return nil, nil, fmt.Errorf("订阅中没有节点")
	}
	return nodes, nil, nil
}

func routingDecodeBase64(raw string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(raw); err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("无效 Base64")
}

func routingParseURI(raw string) (map[string]any, error) {
	if strings.HasPrefix(raw, "vmess://") {
		return routingParseVMess(strings.TrimPrefix(raw, "vmess://"))
	}
	if strings.HasPrefix(raw, "ss://") {
		return routingParseSS(raw)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User == nil {
		return nil, fmt.Errorf("节点 URI 格式无效")
	}
	kind := strings.ToLower(u.Scheme)
	if kind == "hy2" {
		kind = "hysteria2"
	}
	if kind != "vless" && kind != "trojan" && kind != "hysteria2" {
		return nil, fmt.Errorf("URI 暂不支持 %s；可改用含原生 proxies 的 YAML", kind)
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("节点 URI 含未支持路径")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("节点 URI 参数无效")
	}
	allowed := "sni peer alpn insecure allowInsecure"
	if kind == "hysteria2" {
		allowed += " obfs obfs-password"
	} else {
		allowed += " security type host path serviceName mode fp pbk sid spx flow encryption packetEncoding headerType"
	}
	allowedKeys := map[string]bool{}
	for _, key := range strings.Fields(allowed) {
		allowedKeys[key] = true
	}
	for key, values := range q {
		if !allowedKeys[key] || len(values) != 1 {
			return nil, fmt.Errorf("暂不支持或重复的 URI 参数 %s，已阻止发布以避免丢失连接参数", key)
		}
	}
	port := 443
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil {
			return nil, fmt.Errorf("端口无效")
		}
	}
	name := u.Fragment
	if name == "" {
		name = u.Hostname()
	}
	node := map[string]any{"name": name, "type": kind, "server": u.Hostname(), "port": port, "udp": true}
	auth := u.User.Username()
	if password, present := u.User.Password(); present {
		if kind != "hysteria2" {
			return nil, fmt.Errorf("节点认证格式无效")
		}
		auth += ":" + password
	}
	if kind == "vless" {
		node["uuid"] = auth
	} else {
		node["password"] = auth
	}
	if kind == "vless" || kind == "vmess" {
		if q.Get("sni") != "" {
			node["servername"] = q.Get("sni")
		}
	} else {
		sni := q.Get("sni")
		if sni == "" {
			sni = q.Get("peer")
		}
		if sni != "" {
			node["sni"] = sni
		}
	}
	if q.Get("peer") != "" && kind != "hysteria2" {
		return nil, fmt.Errorf("此协议未验证 peer 参数，请使用 sni")
	}
	if q.Get("alpn") != "" {
		node["alpn"] = strings.Split(q.Get("alpn"), ",")
	}
	for _, key := range []string{"insecure", "allowInsecure"} {
		if value := q.Get(key); value != "" {
			enabled, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("%s 须为布尔值", key)
			}
			node["skip-cert-verify"] = enabled
		}
	}
	if kind == "hysteria2" {
		for _, key := range []string{"obfs", "obfs-password"} {
			if q.Get(key) != "" {
				node[key] = q.Get(key)
			}
		}
		return node, nil
	}
	security := q.Get("security")
	if kind == "trojan" && security == "" {
		security = "tls"
	}
	if security != "" && security != "none" && security != "tls" && security != "reality" {
		return nil, fmt.Errorf("不支持 security 参数")
	}
	if kind == "trojan" && security == "none" {
		return nil, fmt.Errorf("Trojan URI 不支持无 TLS 配置")
	}
	if security == "tls" || security == "reality" {
		node["tls"] = true
	}
	for source, dest := range map[string]string{"fp": "client-fingerprint", "flow": "flow", "encryption": "encryption", "packetEncoding": "packet-encoding"} {
		if q.Get(source) != "" {
			node[dest] = q.Get(source)
		}
	}
	if security == "reality" {
		if q.Get("pbk") == "" {
			return nil, fmt.Errorf("Reality 缺少 pbk")
		}
		opts := map[string]any{"public-key": q.Get("pbk")}
		for source, dest := range map[string]string{"sid": "short-id", "spx": "spider-x"} {
			if _, ok := q[source]; ok {
				opts[dest] = q.Get(source)
			}
		}
		node["reality-opts"] = opts
	} else if q.Get("pbk") != "" || q.Get("sid") != "" || q.Get("spx") != "" {
		return nil, fmt.Errorf("Reality 参数需要 security=reality")
	}
	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	node["network"] = network
	if q.Get("headerType") != "" && q.Get("headerType") != "none" {
		return nil, fmt.Errorf("暂不支持 headerType，请改用原生 YAML")
	}
	switch network {
	case "tcp":
		if q.Get("host") != "" || q.Get("path") != "" || q.Get("serviceName") != "" || q.Get("mode") != "" {
			return nil, fmt.Errorf("TCP URI 含不兼容的传输参数")
		}
	case "ws":
		if q.Get("mode") != "" || q.Get("serviceName") != "" {
			return nil, fmt.Errorf("WebSocket URI 含不兼容参数")
		}
		opts := map[string]any{"path": q.Get("path")}
		if q.Get("host") != "" {
			opts["headers"] = map[string]any{"Host": q.Get("host")}
		}
		node["ws-opts"] = opts
	case "grpc":
		if q.Get("host") != "" || q.Get("path") != "" || q.Get("mode") != "" && q.Get("mode") != "gun" {
			return nil, fmt.Errorf("gRPC URI 参数未验证，请使用原生 YAML")
		}
		node["grpc-opts"] = map[string]any{"grpc-service-name": q.Get("serviceName")}
	case "h2":
		if q.Get("serviceName") != "" || q.Get("mode") != "" {
			return nil, fmt.Errorf("HTTP/2 URI 含不兼容参数")
		}
		opts := map[string]any{"path": q.Get("path")}
		if q.Get("host") != "" {
			opts["host"] = strings.Split(q.Get("host"), ",")
		}
		node["h2-opts"] = opts
	default:
		return nil, fmt.Errorf("URI 暂不支持传输 %s，请使用支持的原生 YAML", network)
	}
	return node, nil
}

func routingParseSS(raw string) (map[string]any, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("SS URI 无效")
	}
	if u.RawQuery != "" {
		return nil, fmt.Errorf("SS URI 扩展参数暂不支持，请使用原生 YAML 保留插件配置")
	}
	if u.User != nil && u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("SS URI 含未支持路径")
	}
	var credentials, hostPort string
	if u.User == nil {
		payload := strings.TrimPrefix(strings.SplitN(raw, "#", 2)[0], "ss://")
		decoded, err := routingDecodeBase64(payload)
		if err != nil {
			return nil, fmt.Errorf("SS Base64 无效")
		}
		separator := strings.LastIndexByte(string(decoded), '@')
		if separator < 0 {
			return nil, fmt.Errorf("SS 认证无效")
		}
		credentials, hostPort = string(decoded[:separator]), string(decoded[separator+1:])
	} else {
		hostPort = u.Host
		if pass, ok := u.User.Password(); ok {
			credentials = u.User.Username() + ":" + pass
		} else {
			decoded, err := routingDecodeBase64(u.User.Username())
			if err != nil {
				return nil, fmt.Errorf("SS 认证 Base64 无效")
			}
			credentials = string(decoded)
		}
	}
	cipher, password, ok := strings.Cut(credentials, ":")
	if !ok {
		return nil, fmt.Errorf("SS 缺少密码")
	}
	host, portText, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, fmt.Errorf("SS 服务器地址无效")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, fmt.Errorf("SS 端口无效")
	}
	name := u.Fragment
	if name == "" {
		name = host
	}
	return map[string]any{"name": name, "type": "ss", "server": host, "port": port, "cipher": cipher, "password": password, "udp": true}, nil
}

func routingParseVMess(raw string) (map[string]any, error) {
	decoded, err := routingDecodeBase64(raw)
	if err != nil {
		return nil, fmt.Errorf("VMess Base64 无效")
	}
	var value map[string]any
	// YAML's map decoder also rejects duplicate JSON keys, unlike json.Unmarshal.
	if !json.Valid(decoded) || yaml.Unmarshal(decoded, &value) != nil {
		return nil, fmt.Errorf("VMess JSON 无效")
	}
	valid := map[string]bool{}
	for _, key := range strings.Fields("v ps add port id aid scy net type host path tls sni alpn fp allowInsecure") {
		valid[key] = true
	}
	for key := range value {
		if !valid[key] {
			return nil, fmt.Errorf("暂不支持 VMess 字段 %s", key)
		}
		if key != "v" && key != "port" && key != "aid" && key != "allowInsecure" {
			if _, ok := value[key].(string); !ok {
				return nil, fmt.Errorf("VMess 字段 %s 须为字符串", key)
			}
		}
	}
	if version, ok := value["v"]; ok && fmt.Sprint(version) != "2" {
		return nil, fmt.Errorf("仅支持 VMess v2 JSON URI")
	}
	get := func(key string) string {
		if v, ok := value[key].(string); ok {
			return v
		}
		return ""
	}
	port, err := routingInteger(value["port"])
	if err != nil {
		return nil, fmt.Errorf("VMess 端口无效")
	}
	alterID := 0
	if value["aid"] != nil {
		alterID, err = routingInteger(value["aid"])
		if err != nil {
			return nil, fmt.Errorf("VMess alterId 无效")
		}
	}
	cipher := get("scy")
	if cipher == "" {
		cipher = "auto"
	}
	name := get("ps")
	if name == "" {
		name = get("add")
	}
	node := map[string]any{"name": name, "type": "vmess", "server": get("add"), "port": port, "uuid": get("id"), "alterId": alterID, "cipher": cipher, "udp": true}
	if get("tls") != "" && get("tls") != "none" {
		if get("tls") != "tls" {
			return nil, fmt.Errorf("VMess TLS 模式不支持")
		}
		node["tls"] = true
	}
	if get("sni") != "" {
		node["servername"] = get("sni")
	}
	if get("fp") != "" {
		node["client-fingerprint"] = get("fp")
	}
	if get("alpn") != "" {
		node["alpn"] = strings.Split(get("alpn"), ",")
	}
	if v, ok := value["allowInsecure"]; ok {
		b, ok := v.(bool)
		if !ok {
			b, err = strconv.ParseBool(fmt.Sprint(v))
			if err != nil {
				return nil, fmt.Errorf("VMess allowInsecure 无效")
			}
		}
		node["skip-cert-verify"] = b
	}
	network := get("net")
	if network == "" {
		network = "tcp"
	}
	node["network"] = network
	if get("type") != "" && get("type") != "none" {
		return nil, fmt.Errorf("VMess 伪装类型暂不支持，请使用原生 YAML")
	}
	switch network {
	case "tcp":
		if get("host") != "" || get("path") != "" {
			return nil, fmt.Errorf("VMess TCP 含未支持传输参数")
		}
	case "ws":
		opts := map[string]any{"path": get("path")}
		if get("host") != "" {
			opts["headers"] = map[string]any{"Host": get("host")}
		}
		node["ws-opts"] = opts
	case "h2":
		opts := map[string]any{"path": get("path")}
		if get("host") != "" {
			opts["host"] = strings.Split(get("host"), ",")
		}
		node["h2-opts"] = opts
	case "grpc":
		if get("host") != "" {
			return nil, fmt.Errorf("VMess gRPC host 参数未验证")
		}
		node["grpc-opts"] = map[string]any{"grpc-service-name": get("path")}
	default:
		return nil, fmt.Errorf("VMess 传输 %s 暂不支持", network)
	}
	return node, nil
}
