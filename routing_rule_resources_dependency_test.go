package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutingResourceDependencyRequiresPublishedVerifiedOriginal(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	path := "/_rule-resources/metacubex/" + fixture.revision + "/openai.yaml"
	if known, available := s.managedRuleDependency(path); !known || available {
		t.Fatal("known unsynchronized MetaCubeX resource was not identified as missing")
	}
	for _, invalid := range []string{
		"/_rule-resources/metacubex/meta/openai.yaml",
		"/_rule-resources/metacubex/" + fixture.revision + "/unknown.yaml",
		"/_rule-resources/metacubex/" + fixture.revision + "/openai.json",
		"/_rule-resources/metacubex/" + fixture.revision + "/raw/openai.yaml",
	} {
		if known, available := s.managedRuleDependency(invalid); known || available {
			t.Fatalf("unsupported dependency recognized: %s", invalid)
		}
	}
	if _, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai"}, false); err != nil {
		t.Fatal(err)
	}
	first := fixture.revision
	fixture.revision = strings.Repeat("b", 40)
	if _, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai"}, true); err != nil {
		t.Fatal(err)
	}
	requests := len(fixture.requests)
	if known, available := s.managedRuleDependency(path); !known || !available {
		t.Fatal("historical fixed MetaCubeX dependency lost availability after a newer publication")
	}
	analysis := s.analyzeRemoteRuleDependencies("ruleset=DIRECT,clash-classical:https://" + s.domain + path)
	if analysis.Status != "local" || analysis.Local != 1 || analysis.Missing != 0 {
		t.Fatalf("valid original dependency misclassified: %+v", analysis)
	}
	if err := os.WriteFile(s.routingRawPath(first, "openai"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if known, available := s.managedRuleDependency(path); !known || available {
		t.Fatal("dependency trusted a present file without its checksum")
	}
	analysis = s.analyzeRemoteRuleDependencies("ruleset=DIRECT,clash-classical:https://" + s.domain + path)
	if analysis.Missing != 1 || analysis.Items[0].Available {
		t.Fatalf("corrupt dependency counted as local coverage: %+v", analysis)
	}
	if err := os.Remove(s.routingRawPath(first, "openai")); err != nil {
		t.Fatal(err)
	}
	if known, available := s.managedRuleDependency(path); !known || available {
		t.Fatal("missing original dependency remained available")
	}
	if err := os.WriteFile(filepath.Join(s.routingRulesDir(), "releases", first, "resources.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if known, available := s.managedRuleDependency(path); !known || available {
		t.Fatal("damaged manifest dependency remained available")
	}
	if len(fixture.requests) != requests {
		t.Fatal("dependency inspection fetched upstream or tried to repair local data")
	}
}
