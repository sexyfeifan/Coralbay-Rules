package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed templates/openclash/MihomoPro_overwrite.conf
var mihomoProOverwrite string

var mihomoProInputPaths = []string{"_templates/MihomoPro.yaml", "_templates/MihomoPro.upstream.yaml", "_templates/MihomoPro_overwrite.conf"}

// Read the bytes that the fixed-version public URL serves, not merely any
// non-empty file under current. Associated geo files use the same verifier.
func (s *server) legacyVerifiedResource(manifest legacyResourceManifest, path string) ([]byte, error) {
	meta, ok := manifest.Files[path]
	if !ok || filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path || strings.HasPrefix(path, "../") || meta.Bytes < 1 || meta.Bytes > legacyResourceMaxBytes || !routingHashPattern.MatchString(meta.SHA256) {
		return nil, fmt.Errorf("资源清单缺失或无效：%s", path)
	}
	filename := filepath.Join(s.legacyResourceDir(manifest.Status.ReleaseID), filepath.FromSlash(path))
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Size() != meta.Bytes {
		return nil, fmt.Errorf("资源文件缺失、类型或大小不一致：%s", path)
	}
	data, err := routingReadBoundedFile(filename, legacyResourceMaxBytes)
	if err != nil || int64(len(data)) != meta.Bytes || routingSHA256(data) != meta.SHA256 {
		return nil, fmt.Errorf("资源摘要校验失败：%s", path)
	}
	return data, nil
}

// Add newly managed template inputs to an older retained release. Existing
// resources are never overwritten; the caller holds legacyResourceMu.
func (s *server) retainMihomoProInputs(root string, manifest *legacyResourceManifest) error {
	changed := false
	for _, path := range mihomoProInputPaths {
		if _, ok := manifest.Files[path]; ok {
			continue
		}
		from := filepath.Join(root, filepath.FromSlash(path))
		info, err := os.Lstat(from)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > legacyResourceMaxBytes {
			return fmt.Errorf("MihomoPro 模板文件无效")
		}
		data, err := routingReadBoundedFile(from, legacyResourceMaxBytes)
		if err != nil {
			return err
		}
		dest := filepath.Join(s.legacyResourceDir(manifest.Status.ReleaseID), filepath.FromSlash(path))
		if err = routingRulesAtomicWrite(dest, data); err != nil {
			return err
		}
		manifest.Files[path] = legacyResourceFile{Bytes: int64(len(data)), SHA256: routingSHA256(data)}
		changed = true
	}
	if changed {
		data, err := json.Marshal(manifest)
		if err != nil {
			return err
		}
		return routingRulesAtomicWrite(filepath.Join(s.legacyResourceDir(manifest.Status.ReleaseID), "manifest.json"), data)
	}
	return nil
}

type mihomoProOption struct {
	ID               string `json:"id"`
	Label            string `json:"label"`
	Revision         string `json:"revision"`
	RuleRevision     string `json:"rule_revision"`
	ConfigURL        string `json:"config_url,omitempty"`
	OverwriteURL     string `json:"overwrite_url,omitempty"`
	ProviderCount    int    `json:"provider_count"`
	Available        bool   `json:"available"`
	Reason           string `json:"reason,omitempty"`
	ConfigSHA256     string `json:"config_sha256,omitempty"`
	OverwriteSHA256  string `json:"overwrite_sha256,omitempty"`
	ClientConfigPath string `json:"client_config_path,omitempty"`
	Description      string `json:"description"`
}

type mihomoProManifest struct {
	Version  int                           `json:"version"`
	Revision string                        `json:"revision"`
	Options  []mihomoProOption             `json:"source_options"`
	Files    map[string]legacyResourceFile `json:"files"`
}

func (s *server) mihomoProDir(revision string) string {
	return filepath.Join(s.dataDir, "rule-templates", "666os", revision, "mihomopro")
}

func (s *server) mihomoProRead(revision string) (mihomoProManifest, error) {
	var manifest mihomoProManifest
	if !legacyVersionPattern.MatchString(revision) {
		return manifest, fmt.Errorf("版本无效")
	}
	data, err := routingReadBoundedFile(filepath.Join(s.mihomoProDir(revision), "manifest.json"), 64<<10)
	if err != nil {
		return manifest, err
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != 1 || manifest.Revision != revision || len(manifest.Options) != 2 || len(manifest.Files) != 4 {
		return manifest, fmt.Errorf("MihomoPro 版本清单无效")
	}
	for _, mode := range []string{"local", "upstream"} {
		for _, kind := range []string{"config", "overwrite"} {
			name := mode + "-" + kind
			if _, err := s.mihomoProReadFile(manifest, name); err != nil {
				return manifest, err
			}
		}
	}
	return manifest, nil
}

func (s *server) mihomoProReadFile(manifest mihomoProManifest, name string) ([]byte, error) {
	meta, ok := manifest.Files[name]
	if !ok || (name != "local-config" && name != "local-overwrite" && name != "upstream-config" && name != "upstream-overwrite") || meta.Bytes < 1 || meta.Bytes > legacyResourceMaxBytes || !routingHashPattern.MatchString(meta.SHA256) {
		return nil, fmt.Errorf("MihomoPro 文件元信息无效")
	}
	path := filepath.Join(s.mihomoProDir(manifest.Revision), name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != meta.Bytes {
		return nil, fmt.Errorf("MihomoPro 文件缺失或大小不符")
	}
	data, err := routingReadBoundedFile(path, legacyResourceMaxBytes)
	if err != nil || int64(len(data)) != meta.Bytes || routingSHA256(data) != meta.SHA256 {
		return nil, fmt.Errorf("MihomoPro 文件摘要校验失败")
	}
	return data, nil
}

func (s *server) ensureMihomoProVariants(resources legacyResourceManifest) (mihomoProManifest, error) {
	s.legacyResourceMu.Lock()
	defer s.legacyResourceMu.Unlock()
	revision := resources.Status.ReleaseID
	if manifest, err := s.mihomoProRead(revision); err == nil {
		return manifest, nil
	} else if !os.IsNotExist(err) {
		return manifest, err
	}
	input, err := s.legacyVerifiedResource(resources, "_templates/MihomoPro.yaml")
	if err != nil {
		return mihomoProManifest{}, fmt.Errorf("完整 MihomoPro 配置尚未保存，请同步本地规则：%w", err)
	}
	// Both modes require a complete, verified local copy. Switching addresses
	// must not turn a missing local resource into an implicit upstream fallback.
	for _, path := range legacyResourcePaths() {
		if _, err := s.legacyVerifiedResource(resources, path); err != nil {
			return mihomoProManifest{}, err
		}
	}
	manifest := mihomoProManifest{Version: 1, Revision: revision, Options: []mihomoProOption{}, Files: map[string]legacyResourceFile{}}
	files := map[string][]byte{}
	tag := sha256.Sum256([]byte(revision))
	short := hex.EncodeToString(tag[:])[:16]
	for _, source := range []string{"local", "upstream"} {
		config, count, err := s.mihomoProConfig(input, resources, source)
		if err != nil {
			return manifest, err
		}
		base := "https://" + s.domain + "/_rule-templates/666os/" + revision + "/" + source + "/"
		configURL, overwriteURL := base+"mihomopro-config", base+"mihomopro-overwrite"
		clientPath := "/etc/openclash/config/CoralBay-MihomoPro-" + source + "-" + short + ".yaml"
		overwrite := strings.ReplaceAll(mihomoProOverwrite, "__MIHOMOPRO_CONFIG_URL__", configURL)
		overwrite = strings.ReplaceAll(overwrite, "/etc/openclash/config/MihomoPro.yaml", clientPath)
		overwrite = strings.ReplaceAll(overwrite, "force=false", "force=true")
		overwrite = "# 固定版本规则来源：" + source + "；替换旧覆写模块后应用本模块，专属文件会重新下载。不要同时启用两种来源模块。\n" + overwrite
		files[source+"-config"] = config
		files[source+"-overwrite"] = []byte(overwrite)
		label, desc := "CoralBay 本机", "33 个 MRS 从 CoralBay 同一固定版本下载；配置和图标仍由 CoralBay 提供"
		if source == "upstream" {
			label = "上游来源"
			desc = "33 个 MRS 从 666OS 同一固定提交下载；配置和图标仍由 CoralBay 提供"
		}
		manifest.Options = append(manifest.Options, mihomoProOption{ID: source, Label: label, Revision: revision, RuleRevision: resources.Status.Commit, ConfigURL: configURL, OverwriteURL: overwriteURL, ProviderCount: count, Available: true, ConfigSHA256: routingSHA256(config), OverwriteSHA256: routingSHA256([]byte(overwrite)), ClientConfigPath: clientPath, Description: desc})
	}
	dest := s.mihomoProDir(revision)
	if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return manifest, err
	}
	candidate, err := os.MkdirTemp(filepath.Dir(dest), ".mihomopro-")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(candidate)
	for name, data := range files {
		if err = os.WriteFile(filepath.Join(candidate, name), data, 0644); err != nil {
			return manifest, err
		}
		manifest.Files[name] = legacyResourceFile{Bytes: int64(len(data)), SHA256: routingSHA256(data)}
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

func (s *server) mihomoProConfig(input []byte, manifest legacyResourceManifest, source string) ([]byte, int, error) {
	var cfg map[string]any
	if yaml.Unmarshal(input, &cfg) != nil {
		return nil, 0, fmt.Errorf("MihomoPro 配置 YAML 无效")
	}
	providers, ok := cfg["rule-providers"].(map[string]any)
	if !ok || len(providers) != len(legacyResourcePaths()) {
		return nil, 0, fmt.Errorf("MihomoPro 必须完整引用 33 个已管理 MRS；请核对上游配置")
	}
	seen := map[string]bool{}
	for name, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok {
			return nil, 0, fmt.Errorf("规则集合 %s 定义无效", name)
		}
		address, _ := provider["url"].(string)
		path := ""
		for _, known := range legacyResourcePaths() {
			if address == "https://"+manifest.Status.MirrorDomain+"/"+known || address == "https://github.com/666OS/rules/raw/release/"+known {
				path = known
				break
			}
		}
		behavior := "domain"
		if strings.Contains(path, "/ip/") {
			behavior = "ipcidr"
		}
		if path == "" || seen[path] || provider["type"] != "http" || provider["format"] != "mrs" || provider["behavior"] != behavior {
			return nil, 0, fmt.Errorf("规则集合 %s 没有等价的已验证 MRS 映射", name)
		}
		seen[path] = true
		url := "https://" + s.domain + "/_rule-resources/666os/" + manifest.Status.ReleaseID + "/" + path
		if source == "upstream" {
			url = "https://raw.githubusercontent.com/666OS/rules/" + manifest.Status.Commit + "/" + path
		}
		provider["url"] = url
	}
	if err := validateYAMLReferences(input); err != nil {
		return nil, 0, fmt.Errorf("MihomoPro 配置引用无效：%w", err)
	}
	output, err := yaml.Marshal(cfg)
	return output, len(seen), err
}

func (s *server) mihomoProSourceOptions(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	resources, err := s.retainLegacyResources()
	var manifest mihomoProManifest
	if err == nil {
		manifest, err = s.ensureMihomoProVariants(resources)
	}
	options := manifest.Options
	if err != nil {
		options = []mihomoProOption{}
		for _, source := range []string{"local", "upstream"} {
			label := "CoralBay 本机"
			if source == "upstream" {
				label = "上游来源"
			}
			options = append(options, mihomoProOption{ID: source, Label: label, Revision: resources.Status.ReleaseID, RuleRevision: resources.Status.Commit, Available: false, Reason: err.Error(), Description: "需要先同步并验证完整的本地配置与规则"})
		}
	}
	writeJSON(w, 200, map[string]any{"source_options": options, "local_error": errorText(err), "legacy": map[string]string{"config_url": "https://" + s.domain + "/_templates/MihomoPro.yaml", "overwrite_url": "https://" + s.domain + "/_templates/MihomoPro_overwrite.conf"}})
}

func (s *server) mihomoProSourceFile(w http.ResponseWriter, r *http.Request) {
	source, kind := r.PathValue("source"), strings.TrimPrefix(r.PathValue("client"), "mihomopro-")
	if (source != "local" && source != "upstream") || (kind != "config" && kind != "overwrite") {
		http.NotFound(w, r)
		return
	}
	manifest, err := s.mihomoProRead(r.PathValue("revision"))
	if err != nil {
		http.Error(w, "固定版本 MihomoPro 配置尚未就绪或已损坏", 503)
		return
	}
	name := source + "-" + kind
	data, err := s.mihomoProReadFile(manifest, name)
	if err != nil {
		http.Error(w, "固定版本文件校验失败", 503)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if kind == "config" {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	etag := `"` + manifest.Files[name].SHA256 + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("X-CoralBay-Rule-Source", source)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	w.Write(data)
}
