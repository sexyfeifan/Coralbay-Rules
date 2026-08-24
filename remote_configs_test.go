package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteConfigCatalog(t *testing.T) {
	sources := remoteConfigSources()
	if len(sources) != 88 {
		t.Fatalf("got %d remote configs, want 88", len(sources))
	}
	groups := map[string]int{}
	ids := map[string]bool{}
	for _, source := range sources {
		if source.Group == "" || source.Name == "" || source.URL == "" {
			t.Fatalf("incomplete remote config: %+v", source)
		}
		groups[source.Group]++
		id := remoteConfigID(source.URL)
		if ids[id] {
			t.Fatalf("duplicate remote config id: %s", id)
		}
		ids[id] = true
	}
	if groups["全网搜集规则"] != 32 {
		t.Fatalf("got %d collected configs, want 32", groups["全网搜集规则"])
	}
}

func TestRuleResourcesAppearBeforeConvertedCatalog(t *testing.T) {
	content, err := os.ReadFile("web/admin.html")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	rules := strings.Index(text, `id="ruleSection"`)
	converted := strings.Index(text, `id="conversionRows"`)
	if rules < 0 || converted < 0 || rules >= converted {
		t.Fatal("rule resources must appear before converted and native catalogs")
	}
	if !strings.Contains(text, "🤌") {
		t.Fatal("project icon is missing")
	}
}

func TestRemoteConfigPresetShowsOriginalAndLocalSources(t *testing.T) {
	dataDir := t.TempDir()
	s := &server{dataDir: dataDir, domain: "rules.example.com"}
	source := remoteConfigSources()[0]
	id := remoteConfigID(source.URL)
	if err := os.MkdirAll(filepath.Dir(s.remoteConfigPath(id)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.remoteConfigPath(id), []byte("[custom]\nenable_rule_generator=true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	items := s.subscriptionPresetItems()
	if len(items) != 89 {
		t.Fatalf("got %d preset items, want 89 including none", len(items))
	}
	item := items[1]
	if !item.Cached || item.OriginalURL != source.URL || item.LocalURL != "https://rules.example.com/_configs/"+id+".ini" {
		t.Fatalf("unexpected preset: %+v", item)
	}
}
