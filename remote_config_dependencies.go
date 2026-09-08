package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type remoteRuleDependency struct {
	URL       string `json:"url"`
	Kind      string `json:"kind"`
	Available bool   `json:"available"`
}

type remoteRuleDependencies struct {
	Status   string                 `json:"status"`
	Local    int                    `json:"local"`
	External int                    `json:"external"`
	Missing  int                    `json:"missing"`
	Inline   int                    `json:"inline"`
	Unknown  int                    `json:"unknown"`
	Note     string                 `json:"note"`
	Items    []remoteRuleDependency `json:"items"`
}

func (s *server) remotePresetDependencies(preset remoteConfigPreset) remoteRuleDependencies {
	if preset.ID == "none" {
		return remoteRuleDependencies{Status: "none", Note: "不使用远程配置；规则由所选转换方式决定。", Items: []remoteRuleDependency{}}
	}
	path := s.remoteConfigPath(preset.ID)
	if preset.BuiltIn {
		path = "/app/templates/subconverter/mihomopro.ini"
	}
	content, err := os.ReadFile(path)
	if err != nil && preset.BuiltIn {
		content, err = []byte(builtinConversionINI), nil
	}
	if err != nil {
		return remoteRuleDependencies{Status: "unknown", Unknown: 1, Note: "配置尚未读取，无法确认规则依赖；配置文件本机镜像不代表规则全部本地化。", Items: []remoteRuleDependency{}}
	}
	content = []byte(strings.ReplaceAll(string(content), "__RULES_BASE_URL__", "https://"+s.domain+"/"))
	return s.analyzeRemoteRuleDependencies(string(content))
}

// Inspect cached INI declarations only. Do not chase arbitrary nested URLs or
// interpret a configuration mirror as proof of locally available rules.
func (s *server) analyzeRemoteRuleDependencies(content string) remoteRuleDependencies {
	result := remoteRuleDependencies{Status: "unknown", Items: []remoteRuleDependency{}}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "include" || key == "ruleset_import" {
			result.Unknown++
			continue
		}
		if key != "ruleset" {
			continue
		}
		_, raw, ok := strings.Cut(value, ",")
		raw = strings.TrimSpace(raw)
		if !ok || raw == "" {
			result.Unknown++
			continue
		}
		if strings.HasPrefix(raw, "[]") {
			result.Inline++
			result.Items = append(result.Items, remoteRuleDependency{Kind: "inline", Available: true})
			continue
		}
		address, _, _ := strings.Cut(raw, ",")
		// subconverter permits format prefixes such as clash-domain:https://...
		address = strings.TrimSpace(address)
		if prefix, rest, ok := strings.Cut(address, ":"); ok {
			switch prefix {
			case "clash-domain", "clash-ipcidr", "clash-classic", "clash-classical", "surge", "quanx":
				address = rest
			}
		}
		parsed, err := url.Parse(strings.TrimSpace(address))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Host == "" {
			result.Unknown++
			result.Items = append(result.Items, remoteRuleDependency{Kind: "unknown"})
			continue
		}
		item := remoteRuleDependency{URL: parsed.String(), Kind: "external"}
		if parsed.Scheme == "https" && parsed.Host == s.domain && parsed.RawQuery == "" && parsed.Fragment == "" {
			known, available := s.managedRuleDependency(parsed.Path)
			if !known {
				item.Kind = "unknown"
				result.Unknown++
				result.Items = append(result.Items, item)
				continue
			}
			item.Kind = "local"
			result.Local++
			item.Available = available
			if !item.Available {
				result.Missing++
			}
		} else {
			result.External++
		}
		result.Items = append(result.Items, item)
	}
	switch {
	case result.Unknown > 0 || len(result.Items) == 0:
		result.Status = "unknown"
	case result.Local > 0 && result.External > 0:
		result.Status = "mixed"
	case result.External > 0:
		result.Status = "external"
	case result.Local > 0:
		result.Status = "local"
	default:
		result.Status = "none"
	}
	result.Note = "仅分析已缓存配置声明的规则依赖；外部与嵌套依赖未自动镜像。"
	if result.Status == "local" {
		result.Note = "声明的规则列表由本机提供；GEOIP 等客户端内置数据库仍由客户端管理。转换产物没有等价上游文件。"
	}
	if result.Missing > 0 {
		result.Note += " 存在未同步或零条目规则，不可视为完整本机覆盖。"
	}
	return result
}

func (s *server) managedRuleDependency(path string) (bool, bool) {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") || strings.Contains(path, "\\") || filepath.ToSlash(filepath.Clean(path)) != path {
		return false, false
	}
	resource := strings.TrimPrefix(path, "/")
	if strings.HasPrefix(resource, "_rule-resources/metacubex/") {
		revision, file, ok := strings.Cut(strings.TrimPrefix(resource, "_rule-resources/metacubex/"), "/")
		id := strings.TrimSuffix(file, ".yaml")
		if !ok || !routingRevisionPattern.MatchString(revision) || file != id+".yaml" {
			return false, false
		}
		if _, exists := routingRuleIndex()[id]; !exists {
			return false, false
		}
		manifest, err := s.readRoutingResourceManifest(revision)
		if err != nil {
			return true, false
		}
		meta, exists := manifest.Resources[id]
		if !exists {
			return true, false
		}
		_, err = s.readRoutingRawDocument(revision, id, routingRuleDocument{SourceURL: meta.SourceURL, SHA256: meta.SHA256, RawBytes: meta.Bytes})
		return true, err == nil
	}
	if strings.HasPrefix(resource, "_rule-resources/666os/") {
		revision, file, ok := strings.Cut(strings.TrimPrefix(resource, "_rule-resources/666os/"), "/")
		if !ok || !legacyVersionPattern.MatchString(revision) || !legacyKnownPath(file) {
			return false, false
		}
		data, err := routingReadBoundedFile(filepath.Join(s.legacyResourceDir(revision), "manifest.json"), 2<<20)
		var manifest legacyResourceManifest
		if err != nil || json.Unmarshal(data, &manifest) != nil || manifest.Status.ReleaseID != revision {
			return true, false
		}
		_, err = s.legacyVerifiedResource(manifest, file)
		return true, err == nil
	}
	if legacyKnownPath(resource) {
		manifest, err := s.retainLegacyResources()
		if err != nil {
			return true, false
		}
		_, err = s.legacyVerifiedResource(manifest, resource)
		return true, err == nil
	}
	if strings.HasPrefix(resource, "_converted/") {
		data, err := routingReadBoundedFile(filepath.Join(s.dataDir, "current", filepath.FromSlash(resource)), legacyResourceMaxBytes)
		return true, err == nil && len(legacyEntries(data)) > 0
	}
	// These are published native resources too, not third-party dependencies.
	if (strings.HasPrefix(resource, "surge/") && strings.HasSuffix(resource, ".txt")) || (strings.HasPrefix(resource, "singbox/") && (strings.HasSuffix(resource, ".json") || strings.HasSuffix(resource, ".srs"))) {
		data, err := routingReadBoundedFile(filepath.Join(s.dataDir, "current", filepath.FromSlash(resource)), legacyResourceMaxBytes)
		available := err == nil && len(data) > 0
		if strings.HasSuffix(resource, ".txt") {
			available = available && len(legacyEntries(data)) > 0
		}
		return true, available
	}
	return false, false
}
