package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func restoreStashVLESSFields(ctx context.Context, sources string, content []byte) []byte {
	return restoreStashVLESSWithClient(ctx, sources, content, safeHTTPClient(20*time.Second))
}
func restoreStashVLESSWithClient(ctx context.Context, sources string, content []byte, client *http.Client) []byte {
	type fields struct{ spiderX, encryption string }
	original := map[string]fields{}
	for _, source := range strings.Split(sources, "|") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(source), nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Stash")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		for _, token := range strings.Fields(string(body)) {
			u, err := url.Parse(token)
			if err != nil || !strings.EqualFold(u.Scheme, "vless") || u.User == nil {
				continue
			}
			values := fields{spiderX: u.Query().Get("spx"), encryption: u.Query().Get("encryption")}
			if values.spiderX != "" || values.encryption != "" {
				original[u.User.Username()] = values
			}
		}
	}
	if len(original) == 0 {
		return content
	}
	lines := strings.Split(string(content), "\n")
	for start := 0; start < len(lines); {
		if !strings.HasPrefix(lines[start], "    - ") {
			start++
			continue
		}
		end := start + 1
		for end < len(lines) && !strings.HasPrefix(lines[end], "    - ") && lines[end] != "proxy-groups:" {
			end++
		}
		uuid, realityIndex := "", -1
		for i := start; i < end; i++ {
			trimmed := strings.TrimSpace(lines[i])
			if strings.HasPrefix(trimmed, "uuid:") {
				uuid = parseYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "uuid:")))
			}
			if trimmed == "reality-opts:" {
				realityIndex = i
			}
		}
		if value := original[uuid]; value.spiderX != "" && realityIndex >= 0 {
			insertAt := realityIndex + 1
			for insertAt < end && strings.HasPrefix(lines[insertAt], "        ") {
				insertAt++
			}
			lines = append(lines[:insertAt], append([]string{"        spider-x: " + strconv.QuoteToGraphic(value.spiderX)}, lines[insertAt:]...)...)
			end++
		}
		if value := original[uuid].encryption; value != "" {
			insertAt := start + 1
			lines = append(lines[:insertAt], append([]string{"      encryption: " + strconv.QuoteToGraphic(value)}, lines[insertAt:]...)...)
			end++
		}
		start = end
	}
	return []byte(strings.Join(lines, "\n"))
}

var stashCountryFlags = []struct {
	flag    string
	pattern *regexp.Regexp
}{
	{"🇭🇰", regexp.MustCompile(`(?i)(香港|hong\s*kong|\bHK(?:G)?(?:[-_ ]?\d|\b))`)},
	{"🇹🇼", regexp.MustCompile(`(?i)(台湾|台北|taiwan|taipei|\bTW(?:[-_ ]?\d|\b))`)},
	{"🇯🇵", regexp.MustCompile(`(?i)(日本|东京|大阪|japan|tokyo|\bJP(?:[-_ ]?\d|\b))`)},
	{"🇸🇬", regexp.MustCompile(`(?i)(新加坡|狮城|singapore|\bSG(?:[-_ ]?\d|\b))`)},
	{"🇰🇷", regexp.MustCompile(`(?i)(韩国|首尔|korea|seoul|\bKR(?:[-_ ]?\d|\b))`)},
	{"🇺🇸", regexp.MustCompile(`(?i)(美国|america|united\s*states|\bUS(?:A)?(?:[-_ ]?\d|\b))`)},
	{"🇩🇪", regexp.MustCompile(`(?i)(德国|germany|\bDE(?:U)?(?:[-_ ]?\d|\b))`)},
	{"🇻🇳", regexp.MustCompile(`(?i)(越南|vietnam|\bVN(?:[-_ ]?\d|\b))`)},
	{"🇬🇧", regexp.MustCompile(`(?i)(英国|united\s*kingdom|britain|\b(?:UK|GB)(?:[-_ ]?\d|\b))`)},
	{"🇫🇷", regexp.MustCompile(`(?i)(法国|france|\bFR(?:[-_ ]?\d|\b))`)},
	{"🇳🇱", regexp.MustCompile(`(?i)(荷兰|netherlands|\bNL(?:[-_ ]?\d|\b))`)},
	{"🇹🇭", regexp.MustCompile(`(?i)(泰国|thailand|\bTH(?:[-_ ]?\d|\b))`)},
	{"🇲🇾", regexp.MustCompile(`(?i)(马来西亚|malaysia|\bMY(?:[-_ ]?\d|\b))`)},
	{"🇵🇭", regexp.MustCompile(`(?i)(菲律宾|philippines|\bPH(?:[-_ ]?\d|\b))`)},
	{"🇮🇩", regexp.MustCompile(`(?i)(印度尼西亚|印尼|indonesia|\bID(?:[-_ ]?\d|\b))`)},
	{"🇨🇦", regexp.MustCompile(`(?i)(加拿大|canada|\bCA(?:[-_ ]?\d|\b))`)},
	{"🇦🇺", regexp.MustCompile(`(?i)(澳大利亚|澳洲|australia|\bAU(?:[-_ ]?\d|\b))`)},
	{"🇮🇳", regexp.MustCompile(`(?i)(印度|india|\bIN(?:[-_ ]?\d|\b))`)},
}

var stashGroupIcons = map[string]string{
	"全球手动": "Clubhouse.png", "全球自动": "Auto.png", "故障转移": "ULB.png",
	"香港自动": "Hong_Kong.png", "台湾自动": "Taiwan.png", "日本自动": "Japan.png", "狮城自动": "Singapore.png", "韩国自动": "Korea.png", "美国自动": "United_States.png", "欧洲自动": "Global.png", "东南亚自动": "Global.png",
	"香港策略": "Hong_Kong.png", "台湾策略": "Taiwan.png", "日本策略": "Japan.png", "狮城策略": "Singapore.png", "韩国策略": "Korea.png", "美国策略": "United_States.png", "欧洲策略": "Global.png", "东南亚策略": "Global.png",
	"香港均衡": "Round_Robin_1.png", "台湾均衡": "Round_Robin_1.png", "日本均衡": "Round_Robin_1.png", "狮城均衡": "Round_Robin_1.png", "韩国均衡": "Round_Robin_1.png", "美国均衡": "Round_Robin_1.png", "欧洲均衡": "Round_Robin_1.png", "东南亚均衡": "Round_Robin_1.png",
	"广告拦截": "Reject.png", "网络测试": "Speedtest.png", "即时通讯": "Telegram_X.png", "社交平台": "Twitter.png", "人工智能": "AI.png", "开发服务": "GitHub.png", "EMBY": "Emby.png", "国际媒体": "Streaming.png", "游戏平台": "Game.png", "货币平台": "Cryptocurrency_3.png", "谷歌服务": "Google_Search.png", "脸书服务": "Facebook.png", "微软服务": "Microsoft.png", "苹果服务": "Apple_1.png", "国外流量": "Global.png", "国内流量": "China.png", "漏网之鱼": "Final.png",
}

func transformStashSubscription(content []byte, iconsBase string) []byte {
	// Stash's VLESS Reality YAML schema follows Clash.Meta and uses
	// `servername`; keep the converter's original field instead of rewriting
	// it to the generic TLS alias `sni`.
	text := strings.ReplaceAll(string(content), "https://www.gstatic.com/generate_204", "http://www.apple.com/")
	parts := strings.SplitN(text, "proxy-groups:\n", 2)
	if len(parts) != 2 {
		return []byte(text)
	}
	names := stashProxyRenames(parts[0])
	parts[0] = applyStashRenames(parts[0], names)
	rest := applyStashRenames(parts[1], names)
	groups, suffix := splitStashGroups(rest)
	for i := range groups {
		name := stashGroupName(groups[i])
		if icon := stashGroupIcons[name]; icon != "" && !strings.Contains(groups[i], "\n      icon:") {
			lines := strings.Split(groups[i], "\n")
			lines = append(lines[:1], append([]string{"      icon: " + iconsBase + icon}, lines[1:]...)...)
			groups[i] = strings.Join(lines, "\n")
		}
	}
	result := parts[0] + "proxy-groups:\n" + strings.Join(orderStashGroups(groups), "") + suffix
	for code, flag := range map[string]string{"VN": "🇻🇳", "TH": "🇹🇭", "MY": "🇲🇾", "PH": "🇵🇭", "ID": "🇮🇩", "CA": "🇨🇦", "AU": "🇦🇺", "IN": "🇮🇳", "GB": "🇬🇧", "UK": "🇬🇧", "FR": "🇫🇷", "NL": "🇳🇱"} {
		pattern := regexp.MustCompile(`(?m)^(\s*- (?:name: )?)(` + code + `[-_ ][^\r\n"']+)$`)
		result = pattern.ReplaceAllString(result, `${1}"`+flag+` ${2}"`)
	}
	return []byte(installStashRuleProviders(result, strings.TrimSuffix(iconsBase, "_assets/icons/")))
}

func installStashRuleProviders(content, baseURL string) string {
	boundary := len(content)
	for _, marker := range []string{"\nrule-providers:\n", "\nrules:\n"} {
		if index := strings.Index(content, marker); index >= 0 && index < boundary {
			boundary = index
		}
	}
	type provider struct{ name, policy, path string }
	providers := []provider{
		{"advertising", "广告拦截", "site/advertising.list"}, {"connectivity", "网络测试", "site/connectivity-check.list"},
		{"telegram", "即时通讯", "site/telegram.list"}, {"social", "社交平台", "site/category-social-media-!cn.list"},
		{"ai", "人工智能", "site/category-ai-!cn.list"}, {"development", "开发服务", "site/category-dev.list"},
		{"emby", "EMBY", "site/emby.list"}, {"media", "国际媒体", "site/category-media.list"},
		{"games", "游戏平台", "site/category-games.list"}, {"cryptocurrency", "货币平台", "site/category-cryptocurrency.list"},
		{"google-domain", "谷歌服务", "site/google.list"}, {"google-ip", "谷歌服务", "ip/google.list"},
		{"facebook", "脸书服务", "site/facebook.list"}, {"microsoft", "微软服务", "site/microsoft.list"},
		{"apple", "苹果服务", "site/apple.list"}, {"global", "国外流量", "site/geolocation-!cn.list"},
		{"cn-domain", "国内流量", "site/cn.list"}, {"cn-ip", "国内流量", "ip/cn.list"},
	}
	var out strings.Builder
	out.WriteString(strings.TrimRight(content[:boundary], "\n"))
	out.WriteString("\nrule-providers:\n")
	for _, item := range providers {
		out.WriteString("    " + item.name + ":\n")
		out.WriteString("      type: http\n      behavior: classical\n      format: text\n")
		out.WriteString("      url: " + baseURL + "_converted/native/list/" + item.path + "\n")
		out.WriteString("      interval: 86400\n")
	}
	out.WriteString("rules:\n")
	for _, item := range providers {
		out.WriteString("    - RULE-SET," + item.name + "," + item.policy + "\n")
	}
	out.WriteString("    - GEOIP,CN,国内流量\n    - MATCH,漏网之鱼\n")
	return out.String()
}

func stashProxyRenames(section string) map[string]string {
	renamed := map[string]string{}
	codeFlags := map[string]string{"HK": "🇭🇰", "TW": "🇹🇼", "JP": "🇯🇵", "SG": "🇸🇬", "KR": "🇰🇷", "US": "🇺🇸", "DE": "🇩🇪", "VN": "🇻🇳", "GB": "🇬🇧", "UK": "🇬🇧", "FR": "🇫🇷", "NL": "🇳🇱", "TH": "🇹🇭", "MY": "🇲🇾", "PH": "🇵🇭", "ID": "🇮🇩", "CA": "🇨🇦", "AU": "🇦🇺", "IN": "🇮🇳"}
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- name:") && !strings.HasPrefix(trimmed, "name:") {
			continue
		}
		name := parseYAMLScalar(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "name:")))
		if name == "" || hasLeadingFlag(name) {
			continue
		}
		upper := strings.ToUpper(name)
		if len(upper) >= 3 {
			if flag := codeFlags[upper[:2]]; flag != "" && strings.Contains("-_ ", upper[2:3]) {
				renamed[name] = flag + " " + name
				continue
			}
		}
		for _, country := range stashCountryFlags {
			if country.pattern.MatchString(name) {
				renamed[name] = country.flag + " " + name
				break
			}
		}
	}
	return renamed
}

func applyStashRenames(text string, renamed map[string]string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed, prefix, value := strings.TrimSpace(line), "", ""
		if strings.HasPrefix(trimmed, "- name:") {
			prefix, value = line[:strings.Index(line, "- name:")+len("- name:")]+" ", strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:"))
		} else if strings.HasPrefix(trimmed, "name:") {
			prefix, value = line[:strings.Index(line, "name:")+len("name:")]+" ", strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
		} else if strings.HasPrefix(trimmed, "- ") {
			prefix, value = line[:strings.Index(line, "- ")+2], strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		}
		if replacement := renamed[parseYAMLScalar(value)]; replacement != "" {
			lines[i] = prefix + strconv.QuoteToGraphic(replacement)
		}
	}
	return strings.Join(lines, "\n")
}

func parseYAMLScalar(value string) string {
	if strings.HasPrefix(value, `"`) {
		if decoded, err := strconv.Unquote(value); err == nil {
			return decoded
		}
	}
	return strings.Trim(value, "'\"")
}

func hasLeadingFlag(name string) bool {
	for _, country := range stashCountryFlags {
		if strings.HasPrefix(name, country.flag) {
			return true
		}
	}
	return false
}

func splitStashGroups(rest string) ([]string, string) {
	boundary, offset := len(rest), 0
	for _, line := range strings.SplitAfter(rest, "\n") {
		if len(line) > 0 && line[0] != ' ' {
			boundary = offset
			break
		}
		offset += len(line)
	}
	groupText, suffix := rest[:boundary], rest[boundary:]
	lines, groups, current := strings.SplitAfter(groupText, "\n"), []string{}, ""
	for _, line := range lines {
		if strings.HasPrefix(line, "    - name:") {
			if current != "" {
				groups = append(groups, current)
			}
			current = line
		} else if current != "" {
			current += line
		}
	}
	if current != "" {
		groups = append(groups, current)
	}
	return groups, suffix
}

func stashGroupName(group string) string {
	first := strings.SplitN(group, "\n", 2)[0]
	return parseYAMLScalar(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(first), "- name:")))
}

func orderStashGroups(groups []string) []string {
	order := []string{"香港策略", "台湾策略", "日本策略", "狮城策略", "韩国策略", "美国策略", "欧洲策略", "东南亚策略", "香港自动", "台湾自动", "日本自动", "狮城自动", "韩国自动", "美国自动", "欧洲自动", "东南亚自动", "香港均衡", "台湾均衡", "日本均衡", "狮城均衡", "韩国均衡", "美国均衡", "欧洲均衡", "东南亚均衡", "全球手动", "全球自动", "故障转移"}
	result, used := make([]string, 0, len(groups)), make([]bool, len(groups))
	for _, name := range order {
		for i, group := range groups {
			if !used[i] && stashGroupName(group) == name {
				result = append(result, group)
				used[i] = true
			}
		}
	}
	for i, group := range groups {
		if !used[i] {
			result = append(result, group)
		}
	}
	return result
}
