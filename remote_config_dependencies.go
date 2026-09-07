package main

import (
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
		if parsed.Scheme == "https" && parsed.Host == s.domain && parsed.RawQuery == "" && parsed.Fragment == "" && strings.HasPrefix(parsed.Path, "/_converted/") && !strings.Contains(parsed.Path, "..") {
			item.Kind = "local"
			result.Local++
			data, err := os.ReadFile(filepath.Join(s.dataDir, "current", filepath.FromSlash(strings.TrimPrefix(parsed.Path, "/"))))
			item.Available = err == nil && len(legacyEntries(data)) > 0
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
