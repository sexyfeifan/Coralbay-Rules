package main

import (
	"regexp"
	"strconv"
	"strings"
)

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
	"广告拦截": "Reject.png", "网络测试": "Speedtest.png", "即时通讯": "Telegram_X.png", "社交平台": "Twitter.png", "人工智能": "AI.png", "开发服务": "GitHub.png", "EMBY": "Emby.png", "国际媒体": "Streaming.png", "游戏平台": "Game.png", "货币平台": "Cryptocurrency_3.png", "谷歌服务": "Google_Search.png", "脸书服务": "Facebook.png", "微软服务": "Microsoft.png", "苹果服务": "Apple_1.png", "国外流量": "Global.png", "国内流量": "China.png", "漏网之鱼": "Final.png",
}

func transformStashSubscription(content []byte, iconsBase string) []byte {
	text := strings.ReplaceAll(string(content), "      servername:", "      sni:")
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
	return []byte(parts[0] + "proxy-groups:\n" + strings.Join(orderStashGroups(groups), "") + suffix)
}

func stashProxyRenames(section string) map[string]string {
	renamed := map[string]string{}
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- name:") {
			continue
		}
		name := parseYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:")))
		if name == "" || hasLeadingFlag(name) {
			continue
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
	lines, groups, current, suffix := strings.SplitAfter(rest, "\n"), []string{}, "", ""
	for _, line := range lines {
		if len(line) > 0 && line[0] != ' ' {
			if current != "" {
				groups = append(groups, current)
				current = ""
			}
			suffix += line
			continue
		}
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
	order := []string{"香港策略", "台湾策略", "日本策略", "狮城策略", "韩国策略", "美国策略", "欧洲策略", "东南亚策略", "香港自动", "台湾自动", "日本自动", "狮城自动", "韩国自动", "美国自动", "欧洲自动", "东南亚自动", "全球手动", "全球自动", "故障转移"}
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
