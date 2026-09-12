package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const miaomiaowuTemplateFormat = "miaomiaowu-v3"
const miaomiaowuTemplateSource = "https://github.com/666OS/YYDS/blob/main/mihomo/config/cn/Pro_cn.yaml"

var miaomiaowuBusinessNames = []string{"广告拦截", "网络测试", "即时通讯", "社交平台", "人工智能", "开发服务", "EMBY", "国际媒体", "游戏平台", "货币平台", "谷歌服务", "脸书服务", "微软服务", "苹果服务", "国外流量", "国内流量", "漏网之鱼"}

type miaomiaowuTemplateOption struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Available     bool   `json:"available"`
	Reason        string `json:"reason,omitempty"`
	TemplateURL   string `json:"template_url,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	Revision      string `json:"revision"`
	RuleRevision  string `json:"rule_revision"`
	ProviderCount int    `json:"provider_count"`
	GroupCount    int    `json:"group_count"`
	RuleCount     int    `json:"rule_count"`
	Description   string `json:"description"`
}

type miaomiaowuTemplateManifest struct {
	Format   string                        `json:"format"`
	Revision string                        `json:"revision"`
	Options  []miaomiaowuTemplateOption    `json:"source_options"`
	Files    map[string]legacyResourceFile `json:"files"`
}

func (s *server) registerMiaomiaowuRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /miaomiaowu", s.adminPage)
	mux.HandleFunc("GET /miaomiaowu/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/miaomiaowu", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("GET /api/templates/miaomiaowu", s.auth(s.miaomiaowuTemplateOptions))
	mux.HandleFunc("GET /_miaomiaowu/v1/{revision}/{source}/template.yaml", s.miaomiaowuTemplateFile)
}

func miaomiaowuSourceValid(source string) bool { return source == "local" || source == "upstream" }

func (s *server) miaomiaowuDir(revision string) string {
	return filepath.Join(s.dataDir, "rule-templates", "miaomiaowu", "v1", revision)
}

// The template has a separate immutable namespace. Reading a public artifact
// never consults current, regenerates old configurations, or changes a database.
func (s *server) miaomiaowuRead(revision string) (miaomiaowuTemplateManifest, error) {
	var manifest miaomiaowuTemplateManifest
	if !legacyVersionPattern.MatchString(revision) {
		return manifest, fmt.Errorf("妙妙屋模板版本无效")
	}
	directory := s.miaomiaowuDir(revision)
	info, err := os.Lstat(directory)
	if err != nil {
		return manifest, err
	}
	if !info.IsDir() {
		return manifest, fmt.Errorf("妙妙屋模板目录无效")
	}
	info, err = os.Lstat(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return manifest, err
	}
	if !info.Mode().IsRegular() {
		return manifest, fmt.Errorf("妙妙屋模板清单类型无效")
	}
	data, err := routingReadBoundedFile(filepath.Join(directory, "manifest.json"), 64<<10)
	if err != nil {
		return manifest, err
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Format != miaomiaowuTemplateFormat || manifest.Revision != revision || len(manifest.Options) != 2 || len(manifest.Files) != 2 {
		return manifest, fmt.Errorf("妙妙屋模板清单无效")
	}
	seen := map[string]bool{}
	for _, option := range manifest.Options {
		if !miaomiaowuSourceValid(option.ID) || seen[option.ID] || !option.Available || option.Revision != revision || !legacyCommitPattern.MatchString(option.RuleRevision) || option.ProviderCount != 33 || option.GroupCount != 20 || option.RuleCount != 29 || option.SHA256 != manifest.Files[option.ID].SHA256 {
			return manifest, fmt.Errorf("妙妙屋模板来源清单无效")
		}
		seen[option.ID] = true
		if _, err := s.miaomiaowuReadFile(manifest, option.ID); err != nil {
			return manifest, err
		}
	}
	return manifest, nil
}

func (s *server) miaomiaowuReadFile(manifest miaomiaowuTemplateManifest, source string) ([]byte, error) {
	meta, ok := manifest.Files[source]
	if !legacyVersionPattern.MatchString(manifest.Revision) || !miaomiaowuSourceValid(source) || !ok || meta.Bytes < 1 || meta.Bytes > legacyResourceMaxBytes || !routingHashPattern.MatchString(meta.SHA256) {
		return nil, fmt.Errorf("妙妙屋模板文件清单无效")
	}
	path := filepath.Join(s.miaomiaowuDir(manifest.Revision), source+".yaml")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != meta.Bytes {
		return nil, fmt.Errorf("妙妙屋模板文件缺失、类型或大小不符")
	}
	data, err := routingReadBoundedFile(path, legacyResourceMaxBytes)
	if err != nil || int64(len(data)) != meta.Bytes || routingSHA256(data) != meta.SHA256 {
		return nil, fmt.Errorf("妙妙屋模板摘要校验失败")
	}
	return data, nil
}

func (s *server) ensureMiaomiaowuTemplates(resources legacyResourceManifest) (miaomiaowuTemplateManifest, error) {
	s.miaomiaowuMu.Lock()
	defer s.miaomiaowuMu.Unlock()
	var manifest miaomiaowuTemplateManifest
	revision := resources.Status.ReleaseID
	if !legacyVersionPattern.MatchString(revision) || !legacyCommitPattern.MatchString(resources.Status.Commit) {
		return manifest, fmt.Errorf("本地规则版本信息无效")
	}
	input, err := s.legacyVerifiedResource(resources, "_templates/MihomoPro.yaml")
	if err != nil {
		return manifest, fmt.Errorf("请先同步完整的 YYDS Pro 中文配置：%w", err)
	}
	// Both choices require the complete verified snapshot. A failed local check
	// must never silently enable upstream or reuse an older generated template.
	for _, path := range legacyResourcePaths() {
		if _, err = s.legacyVerifiedResource(resources, path); err != nil {
			return manifest, err
		}
	}
	if saved, err := s.miaomiaowuRead(revision); err == nil {
		return saved, nil
	} else if !os.IsNotExist(err) {
		return saved, err
	}
	// An existing but incomplete release is corruption, not permission to repair
	// an immutable URL. Only an absent release may be newly published.
	dest := s.miaomiaowuDir(revision)
	if _, err := os.Lstat(dest); err == nil || !os.IsNotExist(err) {
		return manifest, fmt.Errorf("妙妙屋模板版本目录已存在但不完整")
	}
	manifest = miaomiaowuTemplateManifest{Format: miaomiaowuTemplateFormat, Revision: revision, Files: map[string]legacyResourceFile{}}
	files := map[string][]byte{}
	for _, source := range []string{"local", "upstream"} {
		data, providers, groups, rules, err := s.miaomiaowuConfig(input, resources, source)
		if err != nil {
			return manifest, err
		}
		files[source] = data
		manifest.Files[source] = legacyResourceFile{Bytes: int64(len(data)), SHA256: routingSHA256(data)}
		label, desc := "CoralBay 本机镜像", "33 个 rule-providers 引用 CoralBay 同一固定版本的 MRS 原件；模板由本站提供。规则集 YAML 导出另用于迁移托管。"
		if source == "upstream" {
			label, desc = "666OS 上游来源", "33 个 rule-providers 引用 666OS 同一固定提交的 MRS 原件；模板由本站提供。规则集 YAML 导出另用于迁移托管。"
		}
		manifest.Options = append(manifest.Options, miaomiaowuTemplateOption{ID: source, Label: label, Available: true,
			TemplateURL: "https://" + s.domain + "/_miaomiaowu/v1/" + revision + "/" + source + "/template.yaml",
			SHA256:      routingSHA256(data), Revision: revision, RuleRevision: resources.Status.Commit,
			ProviderCount: providers, GroupCount: groups, RuleCount: rules, Description: desc})
	}
	if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return manifest, err
	}
	candidate, err := os.MkdirTemp(filepath.Dir(dest), ".candidate-")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(candidate)
	for source, data := range files {
		if err = os.WriteFile(filepath.Join(candidate, source+".yaml"), data, 0644); err != nil {
			return manifest, err
		}
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return manifest, err
	}
	if err = os.WriteFile(filepath.Join(candidate, "manifest.json"), encoded, 0644); err != nil {
		return manifest, err
	}
	if err = os.Rename(candidate, dest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

// miaomiaowuConfig retains YYDS routing semantics and adapts node selection to
// Miaomiaowu's renderer. It intentionally does not reproduce the regional chain
// groups: the renderer can remove empty leaves but leave empty parent groups.
func (s *server) miaomiaowuConfig(input []byte, resources legacyResourceManifest, source string) ([]byte, int, int, int, error) {
	fail := func(message string) ([]byte, int, int, int, error) {
		return nil, 0, 0, 0, fmt.Errorf("妙妙屋模板适配失败：%s", message)
	}
	if !miaomiaowuSourceValid(source) || !legacyVersionPattern.MatchString(resources.Status.ReleaseID) || !legacyCommitPattern.MatchString(resources.Status.Commit) {
		return fail("来源或规则版本无效")
	}
	mapped, providerCount, err := s.mihomoProConfig(input, resources, source)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	// Decoding to ordinary maps resolves all aliases and YAML merge keys, which
	// the target renderer does not interpret itself.
	var cfg map[string]any
	if err = yaml.Unmarshal(mapped, &cfg); err != nil {
		return fail("配置 YAML 无效")
	}
	groups, ok := cfg["proxy-groups"].([]any)
	if !ok {
		return fail("缺少策略组")
	}
	business := map[string]bool{}
	for _, name := range miaomiaowuBusinessNames {
		business[name] = true
	}
	seen := map[string]bool{}
	adapted := []any{}
	for _, raw := range groups {
		group, ok := raw.(map[string]any)
		if !ok {
			return fail("策略组定义无效")
		}
		name, _ := group["name"].(string)
		if name == "" || seen[name] {
			return fail("策略组名称缺失或重复")
		}
		seen[name] = true
		if !business[name] {
			continue
		}
		proxies, ok := group["proxies"].([]any)
		if !ok || len(proxies) == 0 || group["type"] != "select" {
			return fail("业务策略组必须是有默认选项的 select：" + name)
		}
		builtinOnly := true
		for _, proxy := range proxies {
			p, ok := proxy.(string)
			if !ok || p == "" {
				return fail("业务策略组选项无效：" + name)
			}
			if p != "DIRECT" && p != "REJECT" && p != "REJECT-DROP" {
				builtinOnly = false
			}
		}
		if !builtinOnly {
			if proxies[0] == "DIRECT" {
				proxies = []any{"DIRECT", "故障转移", "全球手动", "全球自动"}
			} else {
				proxies = []any{"故障转移", "全球手动", "全球自动", "DIRECT"}
			}
		}
		item := map[string]any{"name": name, "type": "select", "proxies": proxies}
		if icon, ok := group["icon"].(string); ok && icon != "" {
			const prefix = "https://github.com/Koolson/Qure/raw/master/IconSet/Color/"
			if strings.HasPrefix(icon, prefix) && !strings.Contains(strings.TrimPrefix(icon, prefix), "/") {
				icon = "https://" + s.domain + "/_assets/icons/" + strings.TrimPrefix(icon, prefix)
			}
			item["icon"] = icon
		}
		adapted = append(adapted, item)
	}
	for _, name := range miaomiaowuBusinessNames {
		if !seen[name] {
			return fail("缺少 YYDS 业务策略组：" + name)
		}
	}
	for _, entry := range []struct{ name, kind string }{{"全球手动", "select"}, {"全球自动", "url-test"}, {"故障转移", "fallback"}} {
		proxies := []any{"__PROXY_PROVIDERS__", "__PROXY_NODES__"}
		if entry.kind == "select" {
			proxies = append([]any{"全球自动", "故障转移"}, proxies...)
		}
		group := map[string]any{"name": entry.name, "type": entry.kind, "include-all": true, "include-all-proxies": true, "include-all-providers": true, "proxies": proxies}
		if entry.kind != "select" {
			group["url"], group["interval"] = "https://cp.cloudflare.com/generate_204", 300
		}
		if entry.kind == "url-test" {
			group["tolerance"] = 50
		}
		adapted = append(adapted, group)
	}
	providers := cfg["rule-providers"].(map[string]any)
	cleanProviders := map[string]any{}
	for name, raw := range providers {
		provider := raw.(map[string]any)
		cleanProviders[name] = map[string]any{"type": "http", "behavior": provider["behavior"], "format": "mrs", "url": provider["url"], "interval": 86400}
	}
	rules, ok := cfg["rules"].([]any)
	if !ok || len(rules) != 29 {
		return fail("YYDS 规则数量已变化，需要重新核对适配（预期 28 条 RULE-SET 和 1 条 MATCH）")
	}
	for i, raw := range rules {
		rule, ok := raw.(string)
		if !ok {
			return fail("规则不是字符串")
		}
		parts := strings.Split(rule, ",")
		target := ""
		switch {
		case len(parts) == 2 && parts[0] == "MATCH" && i == len(rules)-1:
			target = parts[1]
		case (len(parts) == 3 || len(parts) == 4 && parts[3] == "no-resolve") && parts[0] == "RULE-SET" && i < len(rules)-1:
			if _, ok := providers[parts[1]]; !ok {
				return fail("规则引用了未知规则集：" + parts[1])
			}
			target = parts[2]
		default:
			return fail("不支持的源规则格式：" + rule)
		}
		if !business[target] && target != "DIRECT" && target != "REJECT" && target != "REJECT-DROP" {
			return fail("规则引用了未适配的策略：" + target)
		}
	}
	output := map[string]any{"mode": "rule", "proxies": nil, "proxy-groups": adapted, "rule-providers": cleanProviders, "rules": rules}
	// These settings do not open controller/listening ports or carry credentials.
	// A strict allowlist keeps deployment-specific upstream options out of exports.
	for _, key := range []string{"ipv6", "unified-delay", "tcp-concurrent", "profile", "sniffer"} {
		if value, exists := cfg[key]; exists {
			output[key] = value
		}
	}
	if dns, ok := cfg["dns"].(map[string]any); ok {
		clean := map[string]any{}
		for _, key := range []string{"enable", "ipv6", "enhanced-mode", "fake-ip-range", "default-nameserver", "nameserver", "fake-ip-filter"} {
			if value, exists := dns[key]; exists {
				clean[key] = value
			}
		}
		output["dns"] = clean
	}
	data, err := yaml.Marshal(output)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	header := "# CoralBay · 妙妙屋 V3 / Clash Mihomo 模板\n# 规则来源：666OS / YYDS Pro_cn；保留 33 个规则集与 29 条规则原序。\n# 17 个业务分流组保留名称及直连/代理/广告默认；地区策略已适配为全球手动、全球自动与故障转移。\n# 节点由妙妙屋生成订阅时注入；本文件不含订阅链接、节点或控制器凭据。\n"
	return append([]byte(header), data...), providerCount, len(adapted), len(rules), nil
}

func (s *server) miaomiaowuTemplateOptions(w http.ResponseWriter, _ *http.Request) {
	routingPrivate(w)
	resources, err := s.retainLegacyResources()
	var manifest miaomiaowuTemplateManifest
	if err == nil {
		manifest, err = s.ensureMiaomiaowuTemplates(resources)
	}
	options := manifest.Options
	if err != nil {
		options = []miaomiaowuTemplateOption{}
		for _, source := range []string{"local", "upstream"} {
			label := "CoralBay 本机镜像"
			if source == "upstream" {
				label = "666OS 上游来源"
			}
			options = append(options, miaomiaowuTemplateOption{ID: source, Label: label, Available: false, Reason: err.Error(), Revision: resources.Status.ReleaseID, RuleRevision: resources.Status.Commit, Description: "需要先同步并验证完整的本地配置与 33 个规则集。"})
		}
	}
	writeJSON(w, 200, map[string]any{"format": miaomiaowuTemplateFormat, "source_name": "666OS / YYDS Pro_cn", "source_url": miaomiaowuTemplateSource, "docs_url": "https://miaomiaowux.com/docs/templates/", "source_options": options, "local_error": errorText(err)})
}

func (s *server) miaomiaowuTemplateFile(w http.ResponseWriter, r *http.Request) {
	revision, source := r.PathValue("revision"), r.PathValue("source")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !legacyVersionPattern.MatchString(revision) || !miaomiaowuSourceValid(source) {
		http.NotFound(w, r)
		return
	}
	manifest, err := s.miaomiaowuRead(revision)
	if os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "固定版本妙妙屋模板尚未就绪或已损坏", http.StatusServiceUnavailable)
		return
	}
	data, err := s.miaomiaowuReadFile(manifest, source)
	if err != nil {
		http.Error(w, "固定版本妙妙屋模板校验失败", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-CoralBay-Rule-Source", source)
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="CoralBay-YYDS-Miaomiaowu-`+source+`.yaml"`)
	}
	etag := `"` + manifest.Files[source].SHA256 + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(data)
}
