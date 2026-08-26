// Package regions is the only source of regional name filters.
package regions

import (
	"regexp"
	"strconv"
	"strings"
)

type Region struct{ Code, Name, Pattern string }

var All = []Region{
	{"hk", "香港", `香港|港深|深港|沪港|广港|陆港|hong ?kong|🇭🇰|(?:^|[^a-zA-Z])(?:hk|hkg)(?:[^a-zA-Z]|$)`},
	{"tw", "台湾", `台湾|台北|台中|台南|台东|湾湾|taiwan|taipei|🇹🇼|🇼🇸|(?:^|[^a-zA-Z])(?:tw|tpe|khh|tsa)(?:[^a-zA-Z]|$)`},
	{"jp", "日本", `日本|东京|大阪|京都|神奈川|琦玉|japan|tokyo|🇯🇵|(?:^|[^a-zA-Z])(?:jp|nrt|hnd|kix|cts|fuk)(?:[^a-zA-Z]|$)`},
	{"sg", "狮城", `新加坡|坡县|狮城|singapore|🇸🇬|(?:^|[^a-zA-Z])(?:sg|sin|xsp)(?:[^a-zA-Z]|$)`},
	{"kr", "韩国", `韩国|韓國|首尔|首爾|南朝鲜|korea|seoul|🇰🇷|(?:^|[^a-zA-Z])(?:kr|icn|gmp|pus)(?:[^a-zA-Z]|$)`},
	{"us", "美国", `美国|美东|美西|美中|美南|美港|美日|中美|美专|america|united states|🇺🇸|(?:^|[^a-zA-Z])(?:us|usa|lax|sfo|jfk|sjc)(?:[^a-zA-Z]|$)`},
	{"eu", "欧洲", `德国|英国|法国|荷兰|瑞士|瑞典|挪威|芬兰|丹麦|意大利|西班牙|波兰|奥地利|爱尔兰|葡萄牙|比利时|捷克|希腊|germany|united kingdom|france|netherlands|europe|🇩🇪|🇬🇧|🇫🇷|🇳🇱|🇨🇭|🇸🇪|🇳🇴|🇫🇮|🇩🇰|🇮🇹|🇪🇸|🇵🇱|🇦🇹|🇮🇪|🇵🇹|🇧🇪|🇨🇿|🇬🇷|(?:^|[^a-zA-Z])(?:de|ger|uk|gb|fr|nl|ch|se|no|fi|dk|it|es|pl|at|ie|pt|be|cz|gr)(?:[^a-zA-Z]|$)`},
	{"sea", "东南亚", `越南|泰国|马来西亚|菲律宾|印度尼西亚|印尼|柬埔寨|缅甸|老挝|vietnam|thailand|malaysia|philippines|indonesia|🇻🇳|🇹🇭|🇲🇾|🇵🇭|🇮🇩|🇰🇭|🇲🇲|🇱🇦|(?:^|[^a-zA-Z])(?:vn|th|my|ph|id|kh|mm|la)(?:[^a-zA-Z]|$)`},
}

func Union() string {
	p := []string{}
	for _, r := range All {
		p = append(p, "(?:"+r.Pattern+")")
	}
	return "(?i)(?:" + strings.Join(p, "|") + ")"
}
func IsOther(name string) bool { return !regexp.MustCompile(Union()).MatchString(name) }
func Rewrite(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		for _, r := range All {
			if strings.HasPrefix(line, "x-filter-"+r.Code+":") {
				lines[i] = "x-filter-" + r.Code + ": &filter-" + r.Code + " " + strconv.Quote("(?i)(?:"+r.Pattern+")")
			}
			if strings.HasPrefix(line, "custom_proxy_group="+r.Name) {
				p := strings.Split(line, "`")
				for j := 2; j < len(p); j++ {
					if strings.HasPrefix(p[j], "(?i)") {
						p[j] = "(?i)(?:" + r.Pattern + ")"
					}
				}
				lines[i] = strings.Join(p, "`")
			}
		}
		if strings.HasPrefix(line, "x-filter-other:") {
			lines[i] = "x-filter-other: &filter-other " + strconv.Quote(Union())
		}
		if strings.Contains(line, ", filter: *filter-other") {
			lines[i] = strings.ReplaceAll(lines[i], ", filter: *filter-other", ", exclude-filter: *filter-other")
		}
	}
	return strings.Join(lines, "\n")
}
