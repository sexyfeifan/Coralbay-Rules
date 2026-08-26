package regions

import (
	"regexp"
	"testing"
)

func TestCoverage(t *testing.T) {
	for name, code := range map[string]string{"JP": "jp", "JP1": "jp", "日本2": "jp", "香港": "hk", "HK-01": "hk", "US_1": "us", "🇻🇳 越南1": "sea", "德国01": "eu", "韩国": "kr", "CA1": "", "火星": "", "Canada": "", "Nepal": ""} {
		if IsOther(name) != (code == "") {
			t.Errorf("other mismatch %s", name)
		}
		if code != "" {
			found := false
			for _, r := range All {
				if r.Code == code {
					found = regexp.MustCompile("(?i)(?:" + r.Pattern + ")").MatchString(name)
				}
			}
			if !found {
				t.Errorf("missing region %s", name)
			}
		}
	}
}
func TestRewriteIdempotent(t *testing.T) {
	original := "x-filter-other: &filter-other old\n- { name: 其他自动, filter: *filter-other }\nx-filter-jp: &filter-jp old"
	once := Rewrite(original)
	if Rewrite(once) != once {
		t.Fatal("rewrite changed generated content")
	}
}
