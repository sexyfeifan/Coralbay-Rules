package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPresetDependencyCoverageDoesNotConfuseConfigMirror(t *testing.T) {
	s := &server{dataDir: t.TempDir(), domain: "rules.example.com"}
	path := filepath.Join(s.dataDir, "current", "_converted", "native", "list", "site")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "ok.list"), []byte("DOMAIN,example.com\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "empty.list"), []byte("# no public source\n"), 0644); err != nil {
		t.Fatal(err)
	}
	result := s.analyzeRemoteRuleDependencies("[custom]\nruleset=Proxy,https://rules.example.com/_converted/native/list/site/ok.list\nruleset=Proxy,https://rules.example.com/_converted/native/list/site/empty.list\nruleset=Proxy,clash-domain:https://example.com/rules.yaml\nruleset=Proxy,[]FINAL\n")
	if result.Status != "mixed" || result.Local != 2 || result.External != 1 || result.Missing != 1 || result.Inline != 1 {
		t.Fatalf("wrong dependency coverage: %+v", result)
	}
	if result.Items[1].Available {
		t.Fatal("empty placeholder treated as covered")
	}
	if result.Items[2].Available {
		t.Fatal("unfetched external dependency marked available")
	}
	result = s.analyzeRemoteRuleDependencies("include=https://example.com/nested.ini\nruleset=Proxy,https://example.com/rules.txt")
	if result.Status != "unknown" || result.Unknown != 1 {
		t.Fatal("nested dependency falsely certified")
	}
	result = s.analyzeRemoteRuleDependencies("ruleset=Proxy,http://external.example/fetch?url=https://rules.example.com/_converted/native/list/site/ok.list")
	if result.Status != "external" || result.External != 1 || result.Local != 0 {
		t.Fatal("external URL query parameter was mistaken for a local dependency")
	}
}
