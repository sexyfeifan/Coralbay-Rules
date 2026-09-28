package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Settings are shared by the three export surfaces; artifacts are content
// addressed and never rewritten when settings or upstream inputs change.
const templateGroupingGenerator = "grouping-v1"

var templateGroupingStoreMu sync.Mutex
var templateGroupingPublishMu sync.Mutex
var templateGroupingFileName = regexp.MustCompile(`^(miaomiaowu|config|ppanel-(clash|mihomo|openclash))\.yaml$|^overwrite\.conf$`)

type templateGroupingSavedScope struct {
	Inherit        bool                    `json:"inherit"`
	Profile        templateGroupingProfile `json:"profile"`
	LastGeneration string                  `json:"last_generation,omitempty"`
}

type templateGroupingStore struct {
	Version int                                   `json:"version"`
	Scopes  map[string]templateGroupingSavedScope `json:"scopes"`
}

type templateGroupingArtifact struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256"`
	Bytes       int64  `json:"bytes"`
	Content     string `json:"content,omitempty"`
}

type templateGroupingGeneration struct {
	ID            string                     `json:"id"`
	Generator     string                     `json:"generator"`
	AppVersion    string                     `json:"app_version"`
	Scope         string                     `json:"scope"`
	Client        string                     `json:"client"`
	Source        string                     `json:"source"`
	Profile       templateGroupingProfile    `json:"profile"`
	ProfileHash   string                     `json:"profile_hash"`
	InputRevision string                     `json:"input_revision"`
	RuleRevision  string                     `json:"rule_revision"`
	CreatedAt     string                     `json:"created_at"`
	ProviderCount int                        `json:"provider_count"`
	GroupCount    int                        `json:"group_count"`
	RuleCount     int                        `json:"rule_count"`
	Warnings      []string                   `json:"warnings"`
	Artifacts     []templateGroupingArtifact `json:"artifacts"`
}

func (s *server) registerTemplateGroupingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/template-grouping/profiles/{scope}", s.auth(s.templateGroupingGetProfile))
	mux.HandleFunc("PUT /api/template-grouping/profiles/{scope}", s.auth(s.templateGroupingPutProfile))
	mux.HandleFunc("POST /api/template-grouping/preview", s.auth(s.templateGroupingPreviewHandler))
	mux.HandleFunc("POST /api/template-grouping/generate", s.auth(s.templateGroupingGenerateHandler))
	mux.HandleFunc("GET /_grouped-templates/{id}/{file}", s.templateGroupingFileHandler)
}

func templateGroupingScopeValid(scope string) bool {
	return scope == "common" || scope == "miaomiaowu" || scope == "ppanel" || scope == "overwrite"
}

func (s *server) templateGroupingSettingsPath() string {
	return filepath.Join(s.dataDir, "settings", "template-grouping.json")
}

// Caller holds templateGroupingStoreMu. An absent store uses defaults without
// making a read-only request write files or reset an existing malformed store.
func (s *server) templateGroupingReadStore() (templateGroupingStore, error) {
	store := templateGroupingStore{Version: 1, Scopes: map[string]templateGroupingSavedScope{}}
	data, err := routingReadBoundedFile(s.templateGroupingSettingsPath(), 1<<20)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return store, err
	}
	if err = json.Unmarshal(data, &store); err != nil || store.Version != 1 || store.Scopes == nil || len(store.Scopes) > 4 {
		return store, fmt.Errorf("模板分组设置文件无效，请从备份恢复")
	}
	for scope, saved := range store.Scopes {
		if !templateGroupingScopeValid(scope) || (scope == "common" && saved.Inherit) {
			return store, fmt.Errorf("模板分组设置作用域无效")
		}
		profile, err := normalizeTemplateGroupingProfile(saved.Profile)
		if err != nil {
			return store, fmt.Errorf("已保存的 %s 设置无效：%w", scope, err)
		}
		saved.Profile = profile
		store.Scopes[scope] = saved
	}
	return store, nil
}

func templateGroupingEffective(store templateGroupingStore, scope string) (templateGroupingProfile, bool) {
	common, exists := store.Scopes["common"]
	if !exists {
		common.Profile = defaultTemplateGroupingProfile()
	}
	if scope == "common" {
		return common.Profile, false
	}
	local, exists := store.Scopes[scope]
	if !exists || local.Inherit {
		return common.Profile, true
	}
	return local.Profile, false
}

func (s *server) templateGroupingProfileResponse(scope string, store templateGroupingStore) map[string]any {
	profile, inherit := templateGroupingEffective(store, scope)
	data, _ := json.Marshal(profile)
	result := map[string]any{"scope": scope, "inherit": inherit, "profile": profile, "defaults": defaultTemplateGroupingProfile(), "catalog": templateGroupingCatalog(), "revision": routingSHA256(data)}
	if id := store.Scopes[scope].LastGeneration; routingHashPattern.MatchString(id) {
		if manifest, err := s.templateGroupingReadGeneration(id, false); err == nil {
			result["last_generation"] = manifest
		}
	}
	return result
}

func (s *server) templateGroupingGetProfile(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	scope := r.PathValue("scope")
	if !templateGroupingScopeValid(scope) {
		writeJSON(w, 404, map[string]string{"error": "模板设置作用域不存在"})
		return
	}
	templateGroupingStoreMu.Lock()
	defer templateGroupingStoreMu.Unlock()
	store, err := s.templateGroupingReadStore()
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, s.templateGroupingProfileResponse(scope, store))
}

func (s *server) templateGroupingPutProfile(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	scope := r.PathValue("scope")
	if !templateGroupingScopeValid(scope) {
		writeJSON(w, 404, map[string]string{"error": "模板设置作用域不存在"})
		return
	}
	var request struct {
		Inherit    bool                     `json:"inherit"`
		Profile    *templateGroupingProfile `json:"profile"`
		ApplyScope string                   `json:"apply_scope,omitempty"`
	}
	if !routingDecode(w, r, &request) {
		return
	}
	if scope == "common" && request.Inherit {
		writeJSON(w, 400, map[string]string{"error": "共用方案不能继承自身"})
		return
	}
	if request.ApplyScope != "" && (scope != "common" || request.ApplyScope == "common" || !templateGroupingScopeValid(request.ApplyScope)) {
		writeJSON(w, 400, map[string]string{"error": "只有共用方案可以同时应用到妙妙屋、PPanel 或覆写页面"})
		return
	}
	if !request.Inherit && request.Profile == nil {
		writeJSON(w, 400, map[string]string{"error": "请选择共用方案或提供完整设置"})
		return
	}
	templateGroupingStoreMu.Lock()
	defer templateGroupingStoreMu.Unlock()
	store, err := s.templateGroupingReadStore()
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	profile, _ := templateGroupingEffective(store, scope)
	if request.Profile != nil {
		profile, err = normalizeTemplateGroupingProfile(*request.Profile)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
	}
	saved := store.Scopes[scope]
	saved.Inherit, saved.Profile = request.Inherit, profile
	store.Scopes[scope] = saved
	if request.ApplyScope != "" {
		// Save both changes in one transaction-shaped atomic write. A second
		// HTTP mutation would race other edits and hit the shared rate limiter.
		target := store.Scopes[request.ApplyScope]
		target.Inherit, target.Profile = true, profile
		store.Scopes[request.ApplyScope] = target
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err == nil {
		err = routingRulesAtomicWrite(s.templateGroupingSettingsPath(), data)
	}
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "模板分组设置保存失败"})
		return
	}
	writeJSON(w, 200, s.templateGroupingProfileResponse(scope, store))
}

func (s *server) templateGroupingPreviewHandler(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	var request struct {
		Profile templateGroupingProfile `json:"profile"`
		Names   []string                `json:"names"`
	}
	if !routingDecode(w, r, &request) {
		return
	}
	if len(request.Names) > 2000 {
		writeJSON(w, 400, map[string]string{"error": "节点归属预览最多支持 2000 个名称"})
		return
	}
	for _, name := range request.Names {
		if len(name) > 512 || strings.ContainsAny(name, "\r\n\x00") {
			writeJSON(w, 400, map[string]string{"error": "节点名称过长或含控制字符"})
			return
		}
	}
	result, err := templateGroupingPreview(request.Profile, request.Names)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, result)
}

func templateGroupingTargetValid(scope, client string) bool {
	switch scope {
	case "miaomiaowu":
		return client == "clash"
	case "ppanel":
		return client == "clash" || client == "mihomo" || client == "openclash"
	case "overwrite":
		return client == "mihomo"
	}
	return false
}

func (s *server) templateGroupingGenerateHandler(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	var request struct {
		Scope   string                   `json:"scope"`
		Client  string                   `json:"client"`
		Source  string                   `json:"source"`
		Profile *templateGroupingProfile `json:"profile"`
	}
	if !routingDecode(w, r, &request) {
		return
	}
	if !templateGroupingTargetValid(request.Scope, request.Client) {
		writeJSON(w, 400, map[string]string{"error": "此设置方案支持妙妙屋 Clash/Mihomo、PPanel Clash/Mihomo/OpenClash 及 MihomoPro 覆写；其他客户端请继续使用原有专用模板"})
		return
	}
	if !miaomiaowuSourceValid(request.Source) {
		writeJSON(w, 400, map[string]string{"error": "请选择本机镜像或上游来源"})
		return
	}
	templateGroupingStoreMu.Lock()
	store, err := s.templateGroupingReadStore()
	profile, _ := templateGroupingEffective(store, request.Scope)
	templateGroupingStoreMu.Unlock()
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	if request.Profile != nil {
		profile = *request.Profile
	}
	profile, err = normalizeTemplateGroupingProfile(profile)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	generation, err := s.templateGroupingGenerate(request.Scope, request.Client, request.Source, profile)
	if err != nil {
		writeJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	// Track the last successful generation separately from settings: previewing
	// unsaved settings never silently changes the active shared profile.
	templateGroupingStoreMu.Lock()
	store, err = s.templateGroupingReadStore()
	if err == nil {
		saved, exists := store.Scopes[request.Scope]
		if !exists {
			saved.Profile, saved.Inherit = templateGroupingEffective(store, request.Scope)
		}
		saved.LastGeneration = generation.ID
		store.Scopes[request.Scope] = saved
		data, marshalErr := json.MarshalIndent(store, "", "  ")
		if marshalErr == nil {
			err = routingRulesAtomicWrite(s.templateGroupingSettingsPath(), data)
		} else {
			err = marshalErr
		}
	}
	templateGroupingStoreMu.Unlock()
	if err != nil {
		generation.Warnings = append(generation.Warnings, "文件已生成，最近生成记录保存失败；请保留下载链接")
	}
	writeJSON(w, 200, generation)
}

func (s *server) templateGroupingDir(id string) string {
	return filepath.Join(s.dataDir, "grouped-templates", id)
}

func (s *server) templateGroupingGenerate(scope, client, source string, profile templateGroupingProfile) (templateGroupingGeneration, error) {
	if !templateGroupingTargetValid(scope, client) || !miaomiaowuSourceValid(source) {
		return templateGroupingGeneration{}, fmt.Errorf("模板目标或规则来源无效")
	}
	profile, err := normalizeTemplateGroupingProfile(profile)
	if err != nil {
		return templateGroupingGeneration{}, err
	}
	resources, err := s.retainLegacyResources()
	if err != nil {
		return templateGroupingGeneration{}, fmt.Errorf("规则快照尚未就绪，请先同步：%w", err)
	}
	input, err := s.legacyVerifiedResource(resources, "_templates/MihomoPro.yaml")
	if err != nil {
		return templateGroupingGeneration{}, err
	}
	var nodeTemplate []byte
	if scope == "ppanel" {
		nodeTemplate, err = s.legacyVerifiedResource(resources, "_templates/clients/"+client+".gotmpl")
		if err != nil {
			return templateGroupingGeneration{}, fmt.Errorf("PPanel 节点模板尚未就绪：%w", err)
		}
	}
	profileBytes, _ := json.Marshal(profile)
	identity, _ := json.Marshal(map[string]any{"generator": templateGroupingGenerator, "app_version": version, "scope": scope, "client": client, "source": source, "profile": profile, "revision": resources.Status.ReleaseID, "rules": resources.Status.Commit, "input": routingSHA256(input), "nodes": routingSHA256(nodeTemplate), "overwrite": routingSHA256([]byte(mihomoProOverwrite)), "domain": s.domain})
	id := routingSHA256(identity)
	templateGroupingPublishMu.Lock()
	defer templateGroupingPublishMu.Unlock()
	if existing, err := s.templateGroupingReadGeneration(id, true); err == nil {
		return existing, nil
	} else if !os.IsNotExist(err) {
		return templateGroupingGeneration{}, err
	}
	var mapped []byte
	mode := "mihomo"
	if scope == "ppanel" {
		mode = "ppanel"
	}
	if scope == "miaomiaowu" {
		mode = "miaomiaowu"
		mapped, _, _, _, err = s.miaomiaowuConfig(input, resources, source)
	} else {
		mapped, _, err = s.mihomoProConfig(input, resources, source)
	}
	if err != nil {
		return templateGroupingGeneration{}, err
	}
	var cfg map[string]any
	if err = yaml.Unmarshal(mapped, &cfg); err != nil {
		return templateGroupingGeneration{}, err
	}
	cfg, err = applyTemplateGrouping(cfg, profile, mode, s.domain)
	if err != nil {
		return templateGroupingGeneration{}, err
	}
	if err = s.templateGroupingVerifyProviders(cfg, resources, source); err != nil {
		return templateGroupingGeneration{}, err
	}
	files := map[string][]byte{}
	names := []string{}
	switch scope {
	case "ppanel":
		name := "ppanel-" + client + ".yaml"
		files[name], err = templateGroupingPPanel(nodeTemplate, cfg, profile)
		names = append(names, name)
	case "miaomiaowu":
		files["miaomiaowu.yaml"], err = yaml.Marshal(cfg)
		names = append(names, "miaomiaowu.yaml")
	case "overwrite":
		files["config.yaml"], err = yaml.Marshal(cfg)
		configURL := "https://" + s.domain + "/_grouped-templates/" + id + "/config.yaml"
		clientPath := "/etc/openclash/config/CoralBay-Grouped-" + id[:16] + ".yaml"
		overwrite := strings.ReplaceAll(mihomoProOverwrite, "__MIHOMOPRO_CONFIG_URL__", configURL)
		overwrite = strings.ReplaceAll(overwrite, "/etc/openclash/config/MihomoPro.yaml", clientPath)
		overwrite = strings.ReplaceAll(overwrite, "force=false", "force=true")
		files["overwrite.conf"] = []byte("# CoralBay 分组方案 " + id[:16] + "；与专属配置成对使用。\n" + overwrite)
		names = append(names, "config.yaml", "overwrite.conf")
	}
	if err != nil {
		return templateGroupingGeneration{}, err
	}
	manifest := templateGroupingGeneration{ID: id, Generator: templateGroupingGenerator, AppVersion: version, Scope: scope, Client: client, Source: source, Profile: profile, ProfileHash: routingSHA256(profileBytes), InputRevision: resources.Status.ReleaseID, RuleRevision: resources.Status.Commit, CreatedAt: time.Now().UTC().Format(time.RFC3339), Warnings: []string{}, Artifacts: []templateGroupingArtifact{}}
	if providers, ok := cfg["rule-providers"].(map[string]any); ok {
		manifest.ProviderCount = len(providers)
	}
	if groups, ok := cfg["proxy-groups"].([]any); ok {
		manifest.GroupCount = len(groups)
	}
	switch rules := cfg["rules"].(type) {
	case []any:
		manifest.RuleCount = len(rules)
	case []string:
		manifest.RuleCount = len(rules)
	}
	if scope == "ppanel" {
		manifest.Warnings = append(manifest.Warnings, "这是包含 Go 模板语法的 PPanel 模板，需粘贴到 PPanel 后由订阅接口渲染；不能直接导入代理客户端")
	}
	if scope == "miaomiaowu" {
		manifest.Warnings = append(manifest.Warnings, "这是妙妙屋 V3 模板；节点归属需要妙妙屋注入实际订阅后确认，空地区由渲染器处理")
	} else {
		manifest.Warnings = append(manifest.Warnings, "没有匹配节点的地区组会拒绝流量；请选择包含节点的地区或全球自动")
		if scope == "overwrite" {
			manifest.Warnings = append(manifest.Warnings, "订阅 provider 的单节点由内核运行时动态加入，可能显示在地区候选之后")
		}
	}
	manifest.Warnings = append(manifest.Warnings, "地区按节点名称匹配；含多个地区的名称按设置顺序及人工覆盖确定归属")
	manifest.Warnings = append(manifest.Warnings, "这是固定版本快照；规则同步仍会继续，采用新来源或新设置时请重新生成并替换模板链接")
	destination := s.templateGroupingDir(id)
	if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return manifest, err
	}
	candidate, err := os.MkdirTemp(filepath.Dir(destination), ".candidate-")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(candidate)
	for _, name := range names {
		content := files[name]
		if len(content) == 0 || len(content) > legacyResourceMaxBytes {
			return manifest, fmt.Errorf("生成文件大小无效")
		}
		if err = os.WriteFile(filepath.Join(candidate, name), content, 0644); err != nil {
			return manifest, err
		}
		url := "https://" + s.domain + "/_grouped-templates/" + id + "/" + name
		manifest.Artifacts = append(manifest.Artifacts, templateGroupingArtifact{Name: name, URL: url, DownloadURL: url + "?download=1", SHA256: routingSHA256(content), Bytes: int64(len(content))})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(candidate, "manifest.json"), data, 0644)
	}
	if err == nil {
		err = os.Rename(candidate, destination)
	}
	if err != nil {
		return manifest, err
	}
	return s.templateGroupingReadGeneration(id, true)
}

// Check every generated rule URL against the retained release, including
// optional media sets. A setting must not publish a plausible but missing URL.
func (s *server) templateGroupingVerifyProviders(cfg map[string]any, resources legacyResourceManifest, source string) error {
	providers, ok := cfg["rule-providers"].(map[string]any)
	if !ok {
		return fmt.Errorf("生成配置缺少规则来源")
	}
	prefix := "https://" + s.domain + "/_rule-resources/666os/" + resources.Status.ReleaseID + "/"
	if source == "upstream" {
		prefix = "https://raw.githubusercontent.com/666OS/rules/" + resources.Status.Commit + "/"
	}
	for name, raw := range providers {
		provider, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("规则 %s 定义无效", name)
		}
		url, _ := provider["url"].(string)
		path := strings.TrimPrefix(url, prefix)
		if path == url || !legacyKnownPath(path) {
			return fmt.Errorf("规则 %s 没有已验证的固定版本来源", name)
		}
		if _, err := s.legacyVerifiedResource(resources, path); err != nil {
			return err
		}
	}
	return nil
}

// The node-rendering bytes remain unchanged. Only the top-level settings
// explicitly edited by the user and the policy/rule tail are replaced.
func templateGroupingPPanel(input []byte, cfg map[string]any, profile templateGroupingProfile) ([]byte, error) {
	marker := []byte("\nproxy-groups:")
	i := bytes.Index(input, marker)
	if i < 0 || bytes.Index(input[i+len(marker):], marker) >= 0 {
		return nil, fmt.Errorf("PPanel 模板策略边界无效，请同步当前客户端模板")
	}
	prefix := append([]byte(nil), input[:i+1]...)
	if !bytes.Contains(prefix, []byte("\nproxies:")) || !bytes.Contains(prefix, []byte("$supportedProxies")) {
		return nil, fmt.Errorf("PPanel 节点渲染结构与当前适配器不兼容")
	}
	for _, setting := range []struct {
		key     string
		enabled bool
	}{
		{"dns", profile.DNSMode != "inherit" || profile.IPv6 != "inherit"},
		{"ipv6", profile.IPv6 != "inherit"},
		{"sniffer", profile.Sniffer != "inherit"},
	} {
		if !setting.enabled {
			continue
		}
		var err error
		prefix, err = templateGroupingReplaceYAMLSetting(prefix, setting.key, cfg[setting.key])
		if err != nil {
			return nil, err
		}
	}
	tail, err := yaml.Marshal(map[string]any{"proxy-groups": cfg["proxy-groups"], "rule-providers": cfg["rule-providers"], "rules": cfg["rules"]})
	if err != nil {
		return nil, err
	}
	// Settings remain literal data inside a Go template, even if an administrator
	// supplies a URL or regular expression containing Go template delimiters.
	tail = bytes.ReplaceAll(tail, []byte("{{"), []byte(`{{ "{{" }}`))
	// The engine's explicit node marker preserves the requested order: global
	// choices, each individual subscription node, then regional choices. Expand
	// it from the same supported list used by the untouched node renderer.
	markerLine := regexp.MustCompile(`(?m)^([ \t]*)- __CORALBAY_PROXY_NODES__$`)
	tail = markerLine.ReplaceAllFunc(tail, func(line []byte) []byte {
		indent := line[:len(line)-len(bytes.TrimLeft(line, " \t"))]
		return []byte("{{ range $proxy := $supportedProxies }}" + string(indent) + "- {{ $proxy.Name | quote }}\n{{ end }}")
	})
	if bytes.Contains(tail, []byte("__CORALBAY_PROXY_NODES__")) {
		return nil, fmt.Errorf("PPanel 单节点列表占位符无法展开")
	}
	return append(prefix, tail...), nil
}

func templateGroupingReplaceYAMLSetting(input []byte, key string, value any) ([]byte, error) {
	// YAML top-level sections end at another unindented YAML key. The replaced
	// fields all precede proxies, so Go node-rendering directives never match.
	proxyAt := bytes.Index(input, []byte("\nproxies:"))
	if proxyAt < 0 {
		return nil, fmt.Errorf("缺少 PPanel 节点区块")
	}
	head, nodes := input[:proxyAt+1], input[proxyAt+1:]
	lines := strings.SplitAfter(string(head), "\n")
	start, end := -1, -1
	topLevel := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*:`)
	for i, line := range lines {
		if strings.HasPrefix(line, key+":") {
			start = i
			continue
		}
		if start >= 0 && topLevel.MatchString(line) {
			end = i
			break
		}
	}
	replacement, err := yaml.Marshal(map[string]any{key: value})
	if err != nil {
		return nil, err
	}
	var output string
	if start < 0 {
		output = string(head) + string(replacement)
	} else {
		if end < 0 {
			end = len(lines)
		}
		output = strings.Join(lines[:start], "") + string(replacement) + strings.Join(lines[end:], "")
	}
	return append([]byte(output), nodes...), nil
}

func (s *server) templateGroupingReadGeneration(id string, includeContent bool) (templateGroupingGeneration, error) {
	var manifest templateGroupingGeneration
	if !routingHashPattern.MatchString(id) {
		return manifest, fmt.Errorf("模板版本无效")
	}
	directory := s.templateGroupingDir(id)
	info, err := os.Lstat(directory)
	if err != nil {
		return manifest, err
	}
	if !info.IsDir() {
		return manifest, fmt.Errorf("模板目录无效")
	}
	info, err = os.Lstat(filepath.Join(directory, "manifest.json"))
	if err != nil || !info.Mode().IsRegular() {
		return manifest, fmt.Errorf("模板版本清单缺失或无效")
	}
	data, err := routingReadBoundedFile(filepath.Join(directory, "manifest.json"), 128<<10)
	if err != nil {
		return manifest, err
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.ID != id || manifest.Generator != templateGroupingGenerator || !templateGroupingTargetValid(manifest.Scope, manifest.Client) || !miaomiaowuSourceValid(manifest.Source) || len(manifest.Artifacts) < 1 || len(manifest.Artifacts) > 2 {
		return manifest, fmt.Errorf("模板版本清单无效")
	}
	seen := map[string]bool{}
	for i := range manifest.Artifacts {
		artifact := &manifest.Artifacts[i]
		if seen[artifact.Name] || !templateGroupingFileName.MatchString(artifact.Name) || !routingHashPattern.MatchString(artifact.SHA256) || artifact.Bytes < 1 || artifact.Bytes > legacyResourceMaxBytes {
			return manifest, fmt.Errorf("模板文件清单无效")
		}
		seen[artifact.Name] = true
		info, err := os.Lstat(filepath.Join(directory, artifact.Name))
		if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.Bytes {
			return manifest, fmt.Errorf("模板文件缺失或大小不符")
		}
		data, err := routingReadBoundedFile(filepath.Join(directory, artifact.Name), legacyResourceMaxBytes)
		if err != nil || int64(len(data)) != artifact.Bytes || routingSHA256(data) != artifact.SHA256 {
			return manifest, fmt.Errorf("模板文件摘要不一致")
		}
		artifact.Content = ""
		if includeContent {
			artifact.Content = string(data)
		}
	}
	return manifest, nil
}

func (s *server) templateGroupingFileHandler(w http.ResponseWriter, r *http.Request) {
	id, file := r.PathValue("id"), r.PathValue("file")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !routingHashPattern.MatchString(id) || !templateGroupingFileName.MatchString(file) {
		http.NotFound(w, r)
		return
	}
	manifest, err := s.templateGroupingReadGeneration(id, true)
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "固定版本模板尚未就绪或摘要校验失败", 503)
		return
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.Name != file {
			continue
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("ETag", `"`+artifact.SHA256+`"`)
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", `attachment; filename="CoralBay-`+file+`"`)
		}
		if r.Header.Get("If-None-Match") == w.Header().Get("ETag") {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(artifact.Content))
		return
	}
	http.NotFound(w, r)
}
