package main

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// These profiles change routing choices only. In particular, proxies and
// proxy-providers are copied without rewriting any connection fields.
type templateGroupingProfile struct {
	Name       string                     `json:"name"`
	Regions    []string                   `json:"regions"`
	Default    string                     `json:"default"`
	ShowNodes  bool                       `json:"show_nodes"`
	Categories []templateGroupingCategory `json:"categories"`
	Media      []string                   `json:"media"`
	TestURL    string                     `json:"test_url"`
	Interval   int                        `json:"interval"`
	Tolerance  int                        `json:"tolerance"`
	Strategy   string                     `json:"strategy"`
	Icons      bool                       `json:"icons"`
	HideAuto   bool                       `json:"hide_auto"`
	DNSMode    string                     `json:"dns_mode"`
	IPv6       string                     `json:"ipv6"`
	Sniffer    string                     `json:"sniffer"`
	Overrides  []templateGroupingOverride `json:"overrides"`
}

type templateGroupingCategory struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Default string `json:"default"`
}

// Overrides promote their target bucket ahead of ordinary country matching.
// Repeated targets are combined; target priority follows first occurrence.
// The same compiled filters are used by previews and both output renderers.
type templateGroupingOverride struct {
	Pattern string `json:"pattern"`
	Region  string `json:"region"`
}

type templateGroupingCountry struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Continent string `json:"continent"`
	Flag      string `json:"flag"`
	pattern   string
}

type templateGroupingBucket struct {
	Code          string   `json:"code"`
	Name          string   `json:"name"`
	Filter        string   `json:"filter"`
	ExcludeFilter string   `json:"exclude_filter,omitempty"`
	Count         int      `json:"count"`
	Nodes         []string `json:"nodes"`
}

var templateGroupingMediaNames = []string{"YouTube", "Netflix", "Disney", "Spotify"}

var templateGroupingMacros = []struct{ Code, Name, Codes string }{
	{"asia", "亚洲其他", "af am az bh bd bt bn kh cn cy ge hk in id ir iq il jp jo kz kw kg la lb mo my mv mn mm np kp om pk ps ph qa sa sg kr lk sy tw tj th tl tr tm ae uz vn ye"},
	{"europe", "欧洲", "al ad at by be ba bg hr cz dk ee fo fi fr de gi gr gg va hu is ie im it je lv li lt lu mt md mc me nl mk no pl pt ro ru sm rs sk si es sj se ch ua gb ax"},
	{"north-america", "北美其他", "ai ag aw bs bb bz bm bq vg ca ky cr cu cw dm do sv gl gd gp gt ht hn jm mq mx ms ni pa pr bl kn lc mf pm vc sx tt tc us vi"},
	{"south-america", "南美洲", "ar bo br cl co ec fk gf gy py pe sr uy ve gs"},
	{"oceania", "大洋洲", "as au cx cc ck fj pf gu hm ki mh fm nr nc nz nu nf mp pw pg pn ws sb tk to tv um vu wf"},
	{"africa", "非洲", "dz ao bj bw bf bi cv cm cf td km cg cd ci dj eg gq er sz et ga gm gh gn gw ke ls lr ly mg mw ml mr mu yt ma mz na ne ng re rw sh st sn sc sl so za ss sd tz tg tn ug eh zm zw"},
}

func templateGroupingFlag(code string) string {
	if len(code) != 2 {
		return "🌐"
	}
	return string([]rune{rune(0x1f1e6 + int(code[0]) - 'a'), rune(0x1f1e6 + int(code[1]) - 'a')})
}

// ISO flags and two-letter codes cover all listed territories, while common
// country/city aliases also handle subscriptions without flags. Bare ISO codes
// must be uppercase; lowercase codes need a node number (jp01, no-02, etc.).
// This avoids interpreting ordinary words such as "No location" or "in" as
// countries while still accommodating common subscription naming conventions.
func templateGroupingCountries() []templateGroupingCountry {
	aliases := map[string]string{
		"hk": "香港|香港|港深|深港|沪港|广港|hong ?kong|hkg|九龙",
		"tw": "台湾|台湾|臺灣|台北|臺北|台中|台南|taiwan|taipei|tpe",
		"jp": "日本|日本|东京|東京|大阪|京都|埼玉|japan|tokyo|osaka|nrt|hnd|kix",
		"us": "美国|美国|美國|美东|美西|洛杉矶|纽约|硅谷|西雅图|united states|america|los angeles|seattle|new york|usa|lax|sfo|jfk|sjc",
		"sg": "新加坡|新加坡|狮城|獅城|singapore|sin",
		"kr": "韩国|韩国|韓國|首尔|首爾|south korea|seoul|icn",
		"ca": "加拿大|加拿大|多伦多|温哥华|canada|toronto|vancouver|yvr|yyz",
		"de": "德国|德国|德國|法兰克福|柏林|germany|frankfurt|berlin|deu",
		"gb": "英国|英国|英國|伦敦|london|united kingdom|britain|england|uk|gbr",
		"fr": "法国|法国|法國|巴黎|france|paris|fra",
		"nl": "荷兰|荷兰|荷蘭|阿姆斯特丹|netherlands|amsterdam|ams",
		"ch": "瑞士|瑞士|苏黎世|switzerland|zurich",
		"se": "瑞典|瑞典|斯德哥尔摩|sweden|stockholm",
		"no": "挪威|挪威|奥斯陆|norway|oslo",
		"fi": "芬兰|芬兰|芬蘭|赫尔辛基|finland|helsinki",
		"dk": "丹麦|丹麦|丹麥|哥本哈根|denmark|copenhagen",
		"it": "意大利|意大利|义大利|米兰|罗马|italy|milan|rome",
		"es": "西班牙|西班牙|马德里|spain|madrid",
		"pl": "波兰|波兰|波蘭|华沙|poland|warsaw",
		"at": "奥地利|奥地利|奧地利|维也纳|austria|vienna",
		"ie": "爱尔兰|爱尔兰|愛爾蘭|都柏林|ireland|dublin",
		"pt": "葡萄牙|葡萄牙|里斯本|portugal|lisbon",
		"be": "比利时|比利时|比利時|布鲁塞尔|belgium|brussels",
		"cz": "捷克|捷克|布拉格|czech|prague",
		"gr": "希腊|希腊|希臘|雅典|greece|athens",
		"ru": "俄罗斯|俄罗斯|俄羅斯|莫斯科|russia|moscow",
		"ua": "乌克兰|乌克兰|烏克蘭|基辅|ukraine|kyiv",
		"ro": "罗马尼亚|罗马尼亚|romania",
		"hu": "匈牙利|匈牙利|hungary|budapest",
		"bg": "保加利亚|保加利亚|bulgaria",
		"is": "冰岛|冰岛|冰島|iceland",
		"hr": "克罗地亚|克罗地亚|croatia",
		"rs": "塞尔维亚|塞尔维亚|serbia",
		"lt": "立陶宛|立陶宛|lithuania",
		"lv": "拉脱维亚|拉脱维亚|latvia",
		"ee": "爱沙尼亚|爱沙尼亚|estonia",
		"lu": "卢森堡|卢森堡|luxembourg",
		"mt": "马耳他|马耳他|malta",
		"sk": "斯洛伐克|斯洛伐克|slovakia",
		"si": "斯洛文尼亚|斯洛文尼亚|slovenia",
		"al": "阿尔巴尼亚|阿尔巴尼亚|albania",
		"md": "摩尔多瓦|摩尔多瓦|moldova",
		"by": "白俄罗斯|白俄罗斯|belarus",
		"mo": "澳门|澳门|澳門|macao|macau",
		"cn": "中国大陆|中国|中國|大陆|大陸|北京|上海|广州|深圳|china|beijing|shanghai",
		"vn": "越南|越南|河内|胡志明|vietnam|hanoi",
		"th": "泰国|泰国|泰國|曼谷|thailand|bangkok",
		"my": "马来西亚|马来西亚|馬來西亞|吉隆坡|malaysia|kuala lumpur",
		"ph": "菲律宾|菲律宾|菲律賓|马尼拉|philippines|manila",
		"id": "印度尼西亚|印度尼西亚|印度尼西亞|印尼|雅加达|indonesia|jakarta",
		"in": "印度|印度|孟买|新德里|india|mumbai|delhi",
		"kh": "柬埔寨|柬埔寨|金边|cambodia",
		"mm": "缅甸|缅甸|緬甸|myanmar|burma",
		"la": "老挝|老挝|老撾|laos",
		"bn": "文莱|文莱|汶莱|brunei",
		"bd": "孟加拉|孟加拉|bangladesh",
		"pk": "巴基斯坦|巴基斯坦|pakistan",
		"np": "尼泊尔|尼泊尔|尼泊爾|nepal",
		"lk": "斯里兰卡|斯里兰卡|sri lanka",
		"bt": "不丹|不丹|bhutan",
		"mv": "马尔代夫|马尔代夫|maldives",
		"mn": "蒙古|蒙古|mongolia",
		"kz": "哈萨克斯坦|哈萨克斯坦|kazakhstan",
		"uz": "乌兹别克斯坦|乌兹别克斯坦|uzbekistan",
		"kg": "吉尔吉斯斯坦|吉尔吉斯斯坦|kyrgyzstan",
		"tj": "塔吉克斯坦|塔吉克斯坦|tajikistan",
		"tm": "土库曼斯坦|土库曼斯坦|turkmenistan",
		"ae": "阿联酋|阿联酋|阿聯酋|迪拜|dubai|united arab emirates|uae",
		"tr": "土耳其|土耳其|伊斯坦布尔|turkey|türkiye|istanbul",
		"il": "以色列|以色列|特拉维夫|israel|tel aviv",
		"sa": "沙特阿拉伯|沙特|saudi arabia",
		"qa": "卡塔尔|卡塔尔|qatar",
		"bh": "巴林|巴林|bahrain",
		"kw": "科威特|科威特|kuwait",
		"om": "阿曼|阿曼|oman",
		"jo": "约旦|约旦|jordan",
		"lb": "黎巴嫩|黎巴嫩|lebanon",
		"ir": "伊朗|伊朗|iran",
		"iq": "伊拉克|伊拉克|iraq",
		"ge": "格鲁吉亚|格鲁吉亚|georgia",
		"am": "亚美尼亚|亚美尼亚|armenia",
		"az": "阿塞拜疆|阿塞拜疆|azerbaijan",
		"cy": "塞浦路斯|塞浦路斯|cyprus",
		"au": "澳大利亚|澳大利亚|澳大利亞|澳洲|悉尼|墨尔本|australia|sydney|melbourne",
		"nz": "新西兰|新西兰|紐西蘭|新西蘭|奥克兰|new zealand|auckland",
		"fj": "斐济|斐济|fiji",
		"pg": "巴布亚新几内亚|巴布亚新几内亚|papua new guinea",
		"ws": "萨摩亚|萨摩亚|samoa",
		"gu": "关岛|关岛|關島|guam",
		"br": "巴西|巴西|圣保罗|brazil|são paulo|sao paulo",
		"ar": "阿根廷|阿根廷|argentina|buenos aires",
		"cl": "智利|智利|chile|santiago",
		"co": "哥伦比亚|哥伦比亚|colombia|bogota",
		"pe": "秘鲁|秘鲁|秘魯|peru|lima",
		"ec": "厄瓜多尔|厄瓜多尔|ecuador",
		"uy": "乌拉圭|乌拉圭|uruguay",
		"py": "巴拉圭|巴拉圭|paraguay",
		"ve": "委内瑞拉|委内瑞拉|venezuela",
		"bo": "玻利维亚|玻利维亚|bolivia",
		"mx": "墨西哥|墨西哥|mexico",
		"pa": "巴拿马|巴拿马|panama",
		"cr": "哥斯达黎加|哥斯达黎加|costa rica",
		"cu": "古巴|古巴|cuba",
		"jm": "牙买加|牙买加|jamaica",
		"pr": "波多黎各|波多黎各|puerto rico",
		"za": "南非|南非|south africa|johannesburg",
		"eg": "埃及|埃及|开罗|egypt|cairo",
		"ng": "尼日利亚|尼日利亚|nigeria|lagos",
		"ke": "肯尼亚|肯尼亚|kenya|nairobi",
		"ma": "摩洛哥|摩洛哥|morocco",
		"dz": "阿尔及利亚|阿尔及利亚|algeria",
		"tn": "突尼斯|突尼斯|tunisia",
		"gh": "加纳|加纳|ghana",
		"mu": "毛里求斯|毛里求斯|mauritius",
		"sc": "塞舌尔|塞舌尔|seychelles",
		"et": "埃塞俄比亚|埃塞俄比亚|ethiopia",
		"tz": "坦桑尼亚|坦桑尼亚|tanzania",
		"ug": "乌干达|乌干达|uganda",
		"zm": "赞比亚|赞比亚|zambia",
		"zw": "津巴布韦|津巴布韦|zimbabwe",
	}
	result := []templateGroupingCountry{}
	for _, macro := range templateGroupingMacros {
		for _, code := range strings.Fields(macro.Codes) {
			name := strings.ToUpper(code)
			parts := []string{regexp.QuoteMeta(templateGroupingFlag(code)), `(?:^|[^a-zA-Z])(?:` + strings.ToUpper(code) + `|` + code + `[-_ ]?[0-9]+)(?:[^a-zA-Z]|$)`}
			if alias, ok := aliases[code]; ok {
				split := strings.Split(alias, "|")
				name = split[0]
				for _, term := range split[1:] {
					if regexp.MustCompile(`^[a-zA-Z ?]+$`).MatchString(term) {
						parts = append(parts, `(?i:(?:^|[^a-zA-Z])(?:`+term+`)(?:[^a-zA-Z]|$))`)
					} else {
						parts = append(parts, regexp.QuoteMeta(term))
					}
				}
			}
			result = append(result, templateGroupingCountry{Code: code, Name: name, Continent: macro.Code, Flag: templateGroupingFlag(code), pattern: "(?:" + strings.Join(parts, "|") + ")"})
		}
	}
	return result
}

func defaultTemplateGroupingProfile() templateGroupingProfile {
	p := templateGroupingProfile{Name: "CoralBay 全地区分组", Regions: []string{"hk", "tw", "jp", "us", "sg", "kr"}, Default: "全球自动", ShowNodes: true,
		Media: []string{}, TestURL: "https://cp.cloudflare.com/generate_204", Interval: 300, Tolerance: 50, Strategy: "consistent-hashing", Icons: true, DNSMode: "inherit", IPv6: "inherit", Sniffer: "inherit", Overrides: []templateGroupingOverride{}}
	for _, name := range miaomiaowuBusinessNames {
		p.Categories = append(p.Categories, templateGroupingCategory{Name: name, Enabled: true, Default: "auto"})
	}
	return p
}

func templateGroupingHas(items []string, item string) bool {
	for _, v := range items {
		if v == item {
			return true
		}
	}
	return false
}

func templateGroupingCatalog() map[string]any {
	p := defaultTemplateGroupingProfile()
	countries := templateGroupingCountries()
	macros := []map[string]string{}
	for _, macro := range templateGroupingMacros {
		macros = append(macros, map[string]string{"code": macro.Code, "name": macro.Name})
	}
	macros = append(macros, map[string]string{"code": "other", "name": "其他未识别"})
	options := []string{"全球自动", "全球手动", "故障转移", "DIRECT"}
	for _, country := range countries {
		for _, suffix := range []string{"自动", "均衡", "手动"} {
			options = append(options, country.Name+suffix)
		}
	}
	for _, macro := range macros {
		for _, suffix := range []string{"自动", "均衡", "手动"} {
			options = append(options, macro["name"]+suffix)
		}
	}
	return map[string]any{"regions": countries, "categories": p.Categories, "media": templateGroupingMediaNames, "macros": macros,
		"strategies": []string{"consistent-hashing", "round-robin", "sticky-sessions"}, "default_options": options,
		"matching_note": "人工匹配的目标地区优先，按目标首次出现排序；其余按独立地区顺序、大区、未识别分配。多地区名称会显示歧义，每个节点只归属一次。"}
}

func normalizeTemplateGroupingProfile(p templateGroupingProfile) (templateGroupingProfile, error) {
	// Callers may use one saved profile for concurrent exports. Normalize a
	// private copy, including the slices, rather than editing their settings.
	if p.Regions != nil {
		p.Regions = append([]string{}, p.Regions...)
	}
	if p.Categories != nil {
		p.Categories = append([]templateGroupingCategory{}, p.Categories...)
	}
	if p.Overrides != nil {
		p.Overrides = append([]templateGroupingOverride{}, p.Overrides...)
	}
	if p.Media != nil {
		p.Media = append([]string{}, p.Media...)
	}
	defaults := defaultTemplateGroupingProfile()
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		p.Name = defaults.Name
	}
	if len([]rune(p.Name)) > 80 || strings.ContainsAny(p.Name, "\r\n\x00") {
		return p, fmt.Errorf("方案名称应为 1–80 个字符，不能换行")
	}
	if p.Regions == nil {
		p.Regions = defaults.Regions
	}
	if len(p.Regions) > 40 {
		return p, fmt.Errorf("最多单列 40 个地区")
	}
	countries := map[string]templateGroupingCountry{}
	for _, c := range templateGroupingCountries() {
		countries[c.Code] = c
	}
	seen := map[string]bool{}
	for i, code := range p.Regions {
		code = strings.ToLower(strings.TrimSpace(code))
		if _, ok := countries[code]; !ok || seen[code] {
			return p, fmt.Errorf("独立地区无效或重复：%s", code)
		}
		p.Regions[i], seen[code] = code, true
	}
	for _, macro := range templateGroupingMacros {
		seen[macro.Code] = true
	}
	if len(p.Overrides) > 32 {
		return p, fmt.Errorf("最多配置 32 条地区匹配规则")
	}
	for i, override := range p.Overrides {
		override.Region = strings.ToLower(strings.TrimSpace(override.Region))
		override.Pattern = strings.TrimSpace(override.Pattern)
		if !seen[override.Region] || override.Pattern == "" || len(override.Pattern) > 512 {
			return p, fmt.Errorf("匹配规则 %d 需要有效表达式，以及已单列地区或大区（不支持未识别组）", i+1)
		}
		compiled, err := regexp.Compile(override.Pattern)
		if err != nil {
			return p, fmt.Errorf("匹配规则 %d 不兼容 RE2：%w", i+1, err)
		}
		if compiled.MatchString("") {
			return p, fmt.Errorf("匹配规则 %d 不能匹配空名称", i+1)
		}
		p.Overrides[i] = override
	}
	if p.TestURL == "" {
		p.TestURL = defaults.TestURL
	}
	u, err := url.Parse(p.TestURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(p.TestURL) > 2048 || (u.Scheme != "https" && u.Scheme != "http") {
		return p, fmt.Errorf("测速地址必须是有效的 HTTP(S) URL")
	}
	if p.Interval == 0 {
		p.Interval = defaults.Interval
	}
	if p.Interval < 60 || p.Interval > 86400 || p.Tolerance < 0 || p.Tolerance > 10000 {
		return p, fmt.Errorf("测速间隔应为 60–86400 秒，容差为 0–10000 毫秒")
	}
	if p.Strategy == "" {
		p.Strategy = defaults.Strategy
	}
	if !templateGroupingHas([]string{"consistent-hashing", "round-robin", "sticky-sessions"}, p.Strategy) {
		return p, fmt.Errorf("不支持的均衡算法：%s", p.Strategy)
	}
	if p.DNSMode == "" {
		p.DNSMode = "inherit"
	}
	if p.IPv6 == "" {
		p.IPv6 = "inherit"
	}
	if p.Sniffer == "" {
		p.Sniffer = "inherit"
	}
	if !templateGroupingHas([]string{"inherit", "fake-ip", "redir-host"}, p.DNSMode) || !templateGroupingHas([]string{"inherit", "on", "off"}, p.IPv6) || !templateGroupingHas([]string{"inherit", "on", "off"}, p.Sniffer) {
		return p, fmt.Errorf("DNS、IPv6 或嗅探选项无效")
	}
	if p.Media == nil {
		p.Media = []string{}
	}
	seenMedia := map[string]bool{}
	for _, name := range p.Media {
		if !templateGroupingHas(templateGroupingMediaNames, name) || seenMedia[name] {
			return p, fmt.Errorf("细分媒体无效或重复：%s", name)
		}
		seenMedia[name] = true
	}
	buckets := templateGroupingBuckets(p)
	choices := []string{"全球自动", "全球手动", "故障转移", "DIRECT", "REJECT", "REJECT-DROP"}
	for _, bucket := range buckets {
		for _, suffix := range []string{"自动", "均衡", "手动"} {
			choices = append(choices, bucket.Name+suffix)
		}
	}
	if p.Default == "" {
		p.Default = defaults.Default
	}
	if !templateGroupingHas(choices, p.Default) {
		return p, fmt.Errorf("默认出口没有对应分组：%s", p.Default)
	}
	categoryChoices := append(append([]string{}, choices...), "auto", "默认出口")
	knownCategories := append(append([]string{}, miaomiaowuBusinessNames...), templateGroupingMediaNames...)
	seenCategories := map[string]bool{}
	if p.Categories == nil {
		p.Categories = defaults.Categories
	}
	for i, category := range p.Categories {
		if !templateGroupingHas(knownCategories, category.Name) || seenCategories[category.Name] {
			return p, fmt.Errorf("业务分类无效或重复：%s", category.Name)
		}
		seenCategories[category.Name] = true
		if category.Default == "" {
			category.Default = "auto"
		}
		if !templateGroupingHas(categoryChoices, category.Default) {
			return p, fmt.Errorf("分类 %s 的初始出口没有对应分组：%s", category.Name, category.Default)
		}
		p.Categories[i] = category
	}
	for _, category := range defaults.Categories {
		if !seenCategories[category.Name] {
			p.Categories = append(p.Categories, category)
		}
	}
	return p, nil
}

func templateGroupingUnion(patterns []string) string {
	if len(patterns) == 0 {
		return ""
	}
	return "(?:" + strings.Join(patterns, ")|(?:") + ")"
}

func templateGroupingBuckets(p templateGroupingProfile) []templateGroupingBucket {
	countries := templateGroupingCountries()
	byCode := map[string]templateGroupingCountry{}
	for _, c := range countries {
		byCode[c.Code] = c
	}
	buckets := []templateGroupingBucket{}
	selected := map[string]bool{}
	for _, code := range p.Regions {
		c := byCode[code]
		buckets = append(buckets, templateGroupingBucket{Code: code, Name: c.Name, Filter: c.pattern, Nodes: []string{}})
		selected[code] = true
	}
	for _, macro := range templateGroupingMacros {
		patterns := []string{}
		for _, c := range countries {
			if c.Continent == macro.Code && !selected[c.Code] {
				patterns = append(patterns, c.pattern)
			}
		}
		// Generic continent labels are useful for nodes that don't name a
		// country; priority exclusions below still prevent double assignment.
		labels := map[string]string{"asia": "亚洲|亞洲|(?i:asia)", "europe": "欧洲|歐洲|(?i:europe)", "north-america": "北美|(?i:north america)", "south-america": "南美|(?i:south america)", "oceania": "大洋洲|(?i:oceania)", "africa": "非洲|(?i:africa)"}
		patterns = append(patterns, labels[macro.Code])
		buckets = append(buckets, templateGroupingBucket{Code: macro.Code, Name: macro.Name, Filter: templateGroupingUnion(patterns), Nodes: []string{}})
	}
	priorities := map[string]int{}
	overrides := map[string][]string{}
	for _, override := range p.Overrides {
		if _, ok := priorities[override.Region]; !ok {
			priorities[override.Region] = len(priorities)
		}
		overrides[override.Region] = append(overrides[override.Region], override.Pattern)
	}
	// Target buckets explicitly promoted by an override get first claim. This
	// avoids lookahead expressions that aren't supported by Go's RE2 engine.
	sort.SliceStable(buckets, func(i, j int) bool {
		pi, oi := priorities[buckets[i].Code]
		pj, oj := priorities[buckets[j].Code]
		if oi != oj {
			return oi
		}
		return oi && pi < pj
	})
	previous := []string{}
	for i := range buckets {
		if additions := overrides[buckets[i].Code]; len(additions) > 0 {
			buckets[i].Filter = templateGroupingUnion(append(additions, buckets[i].Filter))
		}
		buckets[i].ExcludeFilter = templateGroupingUnion(previous)
		previous = append(previous, buckets[i].Filter)
	}
	buckets = append(buckets, templateGroupingBucket{Code: "other", Name: "其他未识别", Filter: ".*", ExcludeFilter: templateGroupingUnion(previous), Nodes: []string{}})
	return buckets
}

func templateGroupingClone(v any) any {
	switch item := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(item))
		for k, v := range item {
			out[k] = templateGroupingClone(v)
		}
		return out
	case []any:
		out := make([]any, len(item))
		for i, v := range item {
			out[i] = templateGroupingClone(v)
		}
		return out
	case []string:
		return append([]string{}, item...)
	default:
		return v
	}
}

func templateGroupingInitial(category templateGroupingCategory) string {
	if category.Default != "auto" && category.Default != "" {
		return category.Default
	}
	switch category.Name {
	case "广告拦截":
		return "REJECT-DROP"
	case "苹果服务", "国内流量":
		return "DIRECT"
	default:
		return "全球自动"
	}
}

func templateGroupingFirst(options []any, first string) []any {
	result := []any{first}
	seen := map[string]bool{first: true}
	for _, item := range options {
		name, _ := item.(string)
		if name != "" && !seen[name] {
			result = append(result, item)
			seen[name] = true
		}
	}
	return result
}

func templateGroupingIcon(name string) string {
	icons := map[string]string{"广告拦截": "Reject", "网络测试": "Speedtest", "即时通讯": "Telegram_X", "社交平台": "Twitter", "人工智能": "AI", "开发服务": "GitHub", "EMBY": "Emby", "国际媒体": "Streaming", "游戏平台": "Game", "货币平台": "Cryptocurrency_3", "谷歌服务": "Google_Search", "脸书服务": "Facebook", "微软服务": "Microsoft", "苹果服务": "Apple_1", "国外流量": "Global", "国内流量": "China", "漏网之鱼": "Final", "全球自动": "Auto", "全球手动": "Clubhouse", "故障转移": "ULB", "默认出口": "Global", "香港": "Hong_Kong", "台湾": "Taiwan", "日本": "Japan", "美国": "United_States", "新加坡": "Singapore", "韩国": "Korea", "YouTube": "Streaming", "Netflix": "Streaming", "Disney": "Streaming", "Spotify": "Streaming"}
	if icon, ok := icons[name]; ok {
		return icon + ".png"
	}
	return "Global.png"
}

func applyTemplateGrouping(cfg map[string]any, p templateGroupingProfile, mode, domain string) (map[string]any, error) {
	p, err := normalizeTemplateGroupingProfile(p)
	if err != nil {
		return nil, err
	}
	if mode != "miaomiaowu" && mode != "mihomo" && mode != "ppanel" {
		return nil, fmt.Errorf("不支持的分组输出格式：%s", mode)
	}
	if cfg == nil {
		return nil, fmt.Errorf("缺少基础配置")
	}
	out := templateGroupingClone(cfg).(map[string]any)
	if domain == "" || strings.ContainsAny(domain, "/\\?#\r\n") {
		return nil, fmt.Errorf("图标镜像域名无效")
	}
	buckets := templateGroupingBuckets(p)
	actualNodes := []any{}
	if raw, ok := cfg["proxies"].([]any); ok {
		for _, item := range raw {
			if proxy, ok := item.(map[string]any); ok {
				if name, ok := proxy["name"].(string); ok && name != "" {
					actualNodes = append(actualNodes, name)
				}
			}
		}
	}
	nodes := func(group map[string]any, entries []any) {
		group["include-all"] = true
		if mode == "miaomiaowu" {
			group["include-all-proxies"], group["include-all-providers"] = true, true
			group["proxies"] = append(entries, "__PROXY_NODES__", "__PROXY_PROVIDERS__")
		} else if len(entries) > 0 {
			group["proxies"] = entries
		}
	}
	icon := func(group map[string]any, name string) {
		if p.Icons {
			group["icon"] = "https://" + domain + "/_assets/icons/" + templateGroupingIcon(name)
		}
	}
	groups := []any{}
	for _, spec := range []struct{ name, kind string }{{"全球自动", "url-test"}, {"全球手动", "select"}, {"故障转移", "fallback"}} {
		group := map[string]any{"name": spec.name, "type": spec.kind}
		entries := []any{}
		if spec.kind == "select" {
			entries = []any{"全球自动", "故障转移", "DIRECT"}
		} else {
			group["url"], group["interval"], group["lazy"], group["empty-fallback"] = p.TestURL, p.Interval, true, "REJECT"
			group["hidden"] = p.HideAuto
			if spec.kind == "url-test" {
				group["tolerance"] = p.Tolerance
			}
		}
		nodes(group, entries)
		icon(group, spec.name)
		groups = append(groups, group)
	}
	defaultOptions := []any{"全球自动", "全球手动", "故障转移", "DIRECT"}
	for _, suffix := range []string{"自动", "均衡", "手动"} {
		for _, bucket := range buckets {
			defaultOptions = append(defaultOptions, bucket.Name+suffix)
		}
	}
	defaultGroup := map[string]any{"name": "默认出口", "type": "select", "proxies": templateGroupingFirst(defaultOptions, p.Default)}
	icon(defaultGroup, "默认出口")
	groups = append(groups, defaultGroup)
	for _, bucket := range buckets {
		for _, spec := range []struct{ suffix, kind string }{{"自动", "url-test"}, {"均衡", "load-balance"}, {"手动", "select"}} {
			group := map[string]any{"name": bucket.Name + spec.suffix, "type": spec.kind, "filter": bucket.Filter, "empty-fallback": "REJECT"}
			if bucket.ExcludeFilter != "" {
				group["exclude-filter"] = bucket.ExcludeFilter
			}
			if spec.kind != "select" {
				group["url"], group["interval"], group["lazy"], group["hidden"] = p.TestURL, p.Interval, true, p.HideAuto
				if spec.kind == "url-test" {
					group["tolerance"] = p.Tolerance
				} else {
					group["strategy"] = p.Strategy
				}
			}
			nodes(group, nil)
			icon(group, bucket.Name)
			groups = append(groups, group)
		}
	}
	categories := append([]templateGroupingCategory{}, p.Categories...)
	for _, media := range p.Media {
		found := false
		for _, category := range categories {
			found = found || category.Name == media
		}
		if !found {
			categories = append(categories, templateGroupingCategory{Name: media, Enabled: true, Default: "auto"})
		}
	}
	remap := map[string]string{}
	businessGroups := []any{}
	for _, category := range categories {
		if templateGroupingHas(templateGroupingMediaNames, category.Name) && !templateGroupingHas(p.Media, category.Name) {
			continue
		}
		if !category.Enabled {
			if category.Name == "广告拦截" || category.Name == "国内流量" || category.Name == "苹果服务" {
				remap[category.Name] = templateGroupingInitial(category)
			} else {
				remap[category.Name] = "默认出口"
			}
			continue
		}
		options := []any{"全球自动", "全球手动", "默认出口"}
		if p.ShowNodes {
			if mode == "miaomiaowu" {
				options = append(options, "__PROXY_NODES__", "__PROXY_PROVIDERS__")
			} else if mode == "ppanel" {
				options = append(options, "__CORALBAY_PROXY_NODES__")
			} else {
				options = append(options, actualNodes...)
			}
		}
		for _, suffix := range []string{"自动", "均衡", "手动"} {
			for _, bucket := range buckets {
				options = append(options, bucket.Name+suffix)
			}
		}
		options = append(options, "DIRECT")
		if category.Name == "广告拦截" {
			options = append(options, "REJECT-DROP", "REJECT")
		}
		group := map[string]any{"name": category.Name, "type": "select", "proxies": templateGroupingFirst(options, templateGroupingInitial(category))}
		if p.ShowNodes {
			group["include-all"] = true
			if mode == "miaomiaowu" {
				group["include-all-proxies"], group["include-all-providers"] = true, true
			}
		}
		icon(group, category.Name)
		businessGroups = append(businessGroups, group)
	}
	// The countries are near the top, but business selectors still contain the
	// requested global → individual nodes → regional modes choice ordering.
	out["proxy-groups"] = append(groups, businessGroups...)
	providers, _ := out["rule-providers"].(map[string]any)
	mediaRules := []any{}
	for _, media := range p.Media {
		target := media
		if replacement, ok := remap[media]; ok {
			target = replacement
		}
		provider, exists := providers[media]
		if !exists || provider == nil {
			return nil, fmt.Errorf("细分媒体 %s 没有已验证的规则集，请先同步完整规则来源", media)
		}
		mediaRules = append(mediaRules, "RULE-SET,"+media+","+target)
		if _, exists := providers[media+"IP"]; exists {
			mediaRules = append(mediaRules, "RULE-SET,"+media+"IP,"+target+",no-resolve")
		}
	}
	rules, ok := out["rules"].([]any)
	if !ok {
		return nil, fmt.Errorf("基础配置缺少规则列表")
	}
	newRules := []any{}
	inserted := false
	for _, raw := range rules {
		rule, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("规则格式必须为字符串")
		}
		parts := strings.Split(rule, ",")
		if len(parts) < 2 {
			return nil, fmt.Errorf("无效规则：%s", rule)
		}
		if parts[0] == "RULE-SET" && len(parts) >= 3 && (templateGroupingHas(p.Media, parts[1]) || strings.HasSuffix(parts[1], "IP") && templateGroupingHas(p.Media, strings.TrimSuffix(parts[1], "IP"))) {
			continue
		}
		if !inserted && (parts[0] == "MATCH" || parts[0] == "RULE-SET" && len(parts) >= 3 && (parts[1] == "Streaming" || parts[1] == "Google" || parts[1] == "Proxy")) {
			newRules = append(newRules, mediaRules...)
			inserted = true
		}
		targetIndex := len(parts) - 1
		if parts[targetIndex] == "no-resolve" {
			targetIndex--
		}
		if replacement, ok := remap[parts[targetIndex]]; ok {
			parts[targetIndex] = replacement
		}
		newRules = append(newRules, strings.Join(parts, ","))
	}
	if len(mediaRules) > 0 && !inserted {
		return nil, fmt.Errorf("无法确定细分媒体规则的插入位置")
	}
	out["rules"] = newRules
	if p.DNSMode != "inherit" || p.IPv6 != "inherit" {
		dns, _ := out["dns"].(map[string]any)
		if dns == nil {
			dns = map[string]any{}
		}
		if p.DNSMode != "inherit" {
			dns["enhanced-mode"] = p.DNSMode
			if p.DNSMode == "fake-ip" {
				if _, ok := dns["fake-ip-range"]; !ok {
					dns["fake-ip-range"] = "198.18.0.1/16"
				}
			}
		}
		if p.IPv6 != "inherit" {
			out["ipv6"], dns["ipv6"] = p.IPv6 == "on", p.IPv6 == "on"
		}
		out["dns"] = dns
	}
	if p.Sniffer != "inherit" {
		sniffer, _ := out["sniffer"].(map[string]any)
		if sniffer == nil {
			sniffer = map[string]any{}
		}
		sniffer["enable"] = p.Sniffer == "on"
		if p.Sniffer == "on" {
			if _, exists := sniffer["sniff"]; !exists {
				sniffer["sniff"] = map[string]any{"HTTP": map[string]any{"ports": []any{80, "8080-8880"}}, "TLS": map[string]any{"ports": []any{443, 8443}}, "QUIC": map[string]any{"ports": []any{443, 8443}}}
			}
		}
		out["sniffer"] = sniffer
	}
	// Remove unused upstream anchors so stale regional groups cannot leak back
	// through other template engines; their resolved fields are already copied.
	for key := range out {
		if strings.HasPrefix(key, "x-") {
			delete(out, key)
		}
	}
	return out, nil
}

func templateGroupingPreview(p templateGroupingProfile, names []string) (any, error) {
	p, err := normalizeTemplateGroupingProfile(p)
	if err != nil {
		return nil, err
	}
	if len(names) > 5000 {
		return nil, fmt.Errorf("一次最多预览 5000 个节点名称")
	}
	buckets := templateGroupingBuckets(p)
	filters := make([]*regexp.Regexp, len(buckets))
	excludes := make([]*regexp.Regexp, len(buckets))
	for i, bucket := range buckets {
		filters[i] = regexp.MustCompile(bucket.Filter)
		if bucket.ExcludeFilter != "" {
			excludes[i] = regexp.MustCompile(bucket.ExcludeFilter)
		}
	}
	nodes := []map[string]any{}
	unknown, ambiguous := 0, 0
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if len(name) > 1024 || strings.ContainsAny(name, "\r\n\x00") {
			return nil, fmt.Errorf("节点名称过长或包含换行")
		}
		matches := []string{}
		selected := -1
		for i, bucket := range buckets {
			if !filters[i].MatchString(name) {
				continue
			}
			if bucket.Code != "other" {
				matches = append(matches, bucket.Code)
			}
			if selected == -1 && (excludes[i] == nil || !excludes[i].MatchString(name)) {
				selected = i
			}
		}
		if selected == -1 {
			return nil, fmt.Errorf("节点地区归属检查失败")
		}
		bucket := &buckets[selected]
		bucket.Count++
		bucket.Nodes = append(bucket.Nodes, name)
		if bucket.Code == "other" {
			unknown++
		}
		if len(matches) > 1 {
			ambiguous++
		}
		nodes = append(nodes, map[string]any{"name": name, "region": bucket.Code, "region_name": bucket.Name, "matches": matches, "ambiguous": len(matches) > 1})
	}
	fakeProviders := map[string]any{}
	for _, media := range p.Media {
		fakeProviders[media] = map[string]any{"behavior": "domain"}
	}
	cfg, err := applyTemplateGrouping(map[string]any{"rules": []any{"MATCH,漏网之鱼"}, "rule-providers": fakeProviders}, p, "miaomiaowu", "preview.invalid")
	if err != nil {
		return nil, err
	}
	groups := []map[string]any{}
	for _, raw := range cfg["proxy-groups"].([]any) {
		group := raw.(map[string]any)
		groups = append(groups, map[string]any{"name": group["name"], "type": group["type"], "proxies": group["proxies"]})
	}
	warnings := []string{"名称识别不能保证真实出口位置；混合地区名称按匹配优先级归属。均衡按连接分配，不会叠加单连接带宽。"}
	if len(p.Overrides) > 0 {
		warnings = append(warnings, "人工匹配会提升整个目标地区的优先级；重复目标合并，目标先后按首次出现。请用歧义提示核对中转名称。")
	}
	if len(nodes) == 0 {
		warnings = append(warnings, "尚未提供节点名称；分组结构可预览，实际覆盖率将在提供节点后显示。")
	}
	return map[string]any{"profile": p, "total": len(nodes), "covered": len(nodes), "unknown": unknown, "ambiguous": ambiguous, "regions": buckets, "nodes": nodes, "groups": groups, "warnings": warnings}, nil
}
