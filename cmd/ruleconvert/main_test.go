package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertFileProducesNativeFormats(t *testing.T) {
	geo := t.TempDir()
	out := t.TempDir()
	source := filepath.Join(geo, "site", "sample.txt")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("example.com\n+.example.org\nregex:^api[0-9]+\\.test$\n+.example.org\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	item, err := convertFile(geo, out, "site", source)
	if err != nil {
		t.Fatal(err)
	}
	if item.Entries != 3 {
		t.Fatalf("got %d entries, want 3", item.Entries)
	}
	list, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(item.List)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DOMAIN,example.com", "DOMAIN-SUFFIX,example.org", "DOMAIN-REGEX,^api[0-9]+\\.test$"} {
		if !strings.Contains(string(list), want) {
			t.Errorf("list missing %q", want)
		}
	}
	content, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(item.SingBox)))
	if err != nil {
		t.Fatal(err)
	}
	var got sourceRuleSet
	if err := json.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 3 || len(got.Rules) != 1 || len(got.Rules[0].DomainSuffix) != 1 {
		t.Fatalf("unexpected sing-box output: %+v", got)
	}
}

func TestPlaceholderDoesNotMatchEverything(t *testing.T) {
	out := t.TempDir()
	item := writePlaceholder(out, "site", "missing")
	content, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(item.SingBox)))
	if err != nil {
		t.Fatal(err)
	}
	var got sourceRuleSet
	if err := json.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rules) != 0 || item.Entries != 0 {
		t.Fatalf("placeholder must be an empty rule array: %+v", got)
	}
}
