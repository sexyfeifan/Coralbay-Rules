package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func mihomoProFixture(t *testing.T) (*server, string) {
	t.Helper()
	s, root := legacyFixture(t)
	input, err := os.ReadFile("templates/openclash/Pro_cn.upstream.yaml")
	if err != nil {
		t.Fatal(err)
	}
	input = []byte(strings.ReplaceAll(string(input), "https://github.com/666OS/rules/raw/release/", "https://"+s.domain+"/"))
	if err = os.WriteFile(filepath.Join(root, "_templates/MihomoPro.yaml"), input, 0644); err != nil {
		t.Fatal(err)
	}
	return s, root
}

func TestMihomoProSourcesPinAll33AndSurviveCleanup(t *testing.T) {
	s, root := mihomoProFixture(t)
	before, _ := os.ReadFile(filepath.Join(root, "_templates/MihomoPro.yaml"))
	resources, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	variants, err := s.ensureMihomoProVariants(resources)
	if err != nil {
		t.Fatal(err)
	}
	var configs []map[string]any
	for _, option := range variants.Options {
		if !option.Available || option.ProviderCount != 33 || option.RuleRevision != resources.Status.Commit || strings.Contains(option.ClientConfigPath, "/MihomoPro.yaml") {
			t.Fatalf("incorrect option: %+v", option)
		}
		for _, kind := range []string{"config", "overwrite"} {
			req := httptest.NewRequest("GET", "/", nil)
			req.SetPathValue("revision", variants.Revision)
			req.SetPathValue("source", option.ID)
			req.SetPathValue("client", "mihomopro-"+kind)
			out := httptest.NewRecorder()
			s.legacyResourceTemplate(out, req)
			if out.Code != 200 {
				t.Fatal(out.Body.String())
			}
			if kind == "config" {
				var cfg map[string]any
				if err = yaml.Unmarshal(out.Body.Bytes(), &cfg); err != nil {
					t.Fatal(err)
				}
				providers := cfg["rule-providers"].(map[string]any)
				for _, raw := range providers {
					p := raw.(map[string]any)
					url := p["url"].(string)
					prefix := "https://" + s.domain + "/_rule-resources/666os/" + resources.Status.ReleaseID + "/"
					if option.ID == "upstream" {
						prefix = "https://raw.githubusercontent.com/666OS/rules/" + resources.Status.Commit + "/"
					}
					if !strings.HasPrefix(url, prefix) {
						t.Fatal("unmapped provider: " + url)
					}
					p["url"] = strings.TrimPrefix(url, prefix)
				}
				configs = append(configs, cfg)
			} else if !strings.Contains(out.Body.String(), "url="+option.ConfigURL+", path="+option.ClientConfigPath) || !strings.Contains(out.Body.String(), "force=true") || strings.Contains(out.Body.String(), "ruby_map_edit") {
				t.Fatal("overwrite pair does not safely refresh its dedicated file")
			}
			req.Header.Set("If-None-Match", out.Header().Get("ETag"))
			again := httptest.NewRecorder()
			s.legacyResourceTemplate(again, req)
			if again.Code != 304 {
				t.Fatal("immutable ETag failed")
			}
		}
	}
	if !reflect.DeepEqual(configs[0], configs[1]) {
		t.Fatal("source switch changed more than rule URLs")
	}
	if variants.Options[0].ClientConfigPath == variants.Options[1].ClientConfigPath {
		t.Fatal("source variants overwrite one another")
	}
	after, _ := os.ReadFile(filepath.Join(root, "_templates/MihomoPro.yaml"))
	if string(before) != string(after) {
		t.Fatal("legacy config mutated")
	}
	if err = os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.mihomoProRead(variants.Revision)
	if err != nil || !reflect.DeepEqual(variants, reloaded) {
		t.Fatalf("immutable variant lost after legacy cleanup: %v", err)
	}
}

func TestMihomoProMissingOrCorruptLocalNeverEnablesUpstream(t *testing.T) {
	for _, part := range []string{"config", "mrs"} {
		t.Run(part, func(t *testing.T) {
			s, _ := mihomoProFixture(t)
			resources, err := s.retainLegacyResources()
			if err != nil {
				t.Fatal(err)
			}
			path := "_templates/MihomoPro.yaml"
			if part == "mrs" {
				path = "mihomo/domain/Google.mrs"
			}
			if err = os.WriteFile(filepath.Join(s.legacyResourceDir(resources.Status.ReleaseID), filepath.FromSlash(path)), []byte("corrupt"), 0644); err != nil {
				t.Fatal(err)
			}
			out := httptest.NewRecorder()
			s.mihomoProSourceOptions(out, httptest.NewRequest("GET", "/", nil))
			var body struct {
				Options []mihomoProOption `json:"source_options"`
			}
			if err = json.Unmarshal(out.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Options) != 2 {
				t.Fatal("missing unavailable choices")
			}
			for _, o := range body.Options {
				if o.Available || o.Reason == "" || o.ConfigURL != "" {
					t.Fatalf("implicit fallback: %+v", o)
				}
			}
		})
	}
}

func TestMihomoProUpgradeRetainsInputsWithoutChangingOldURLs(t *testing.T) {
	s, root := legacyFixture(t)
	first, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.ReadFile("templates/openclash/Pro_cn.upstream.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "_templates/MihomoPro.yaml"), input, 0644); err != nil {
		t.Fatal(err)
	}
	second, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	if second.Status.ReleaseID != first.Status.ReleaseID {
		t.Fatal("legacy release id was rewritten")
	}
	for path, meta := range first.Files {
		if second.Files[path] != meta {
			t.Fatal("existing resource changed")
		}
	}
	if _, err = s.ensureMihomoProVariants(second); err != nil {
		t.Fatal(err)
	}
}

func TestMihomoProOverwriteInjectsCompleteProviders(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("requires Ruby as used by OpenClash")
	}
	input, err := os.ReadFile("templates/openclash/Pro_cn.upstream.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Source variants are emitted by yaml.v3 (expanded mappings/block scalars).
	// Exercise those delivered bytes, including on macOS's older Ruby/Psych.
	var normalized map[string]any
	if err = yaml.Unmarshal(input, &normalized); err != nil {
		t.Fatal(err)
	}
	input, err = yaml.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.SplitN(mihomoProOverwrite, "[Overwrite]", 2)[1]
	// Model the OpenClash helper's deferred Value mutation, not an edit to a
	// parallel file that the loaded YAML object would later overwrite.
	helper := `ruby_edit() {
 ruby -ryaml -e 'file,path,expression=ARGV; Value=YAML.respond_to?(:unsafe_load_file) ? YAML.unsafe_load_file(file) : YAML.load_file(file); eval("Value"+path+"="+expression); File.write(file,YAML.dump(Value))' "$1" "$2" "$3"
}
`
	for _, scenario := range []struct {
		name, main, backup string
		count              int
	}{{"primary", "https://subscription.example.com/?token=quote'&literal=$()", "", 1}, {"backup", "", "https://backup.example.com/sub", 1}, {"both", "https://main.example.com/sub", "https://backup.example.com/sub", 2}, {"empty", "", "", 0}} {
		t.Run(scenario.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config.yaml")
			script := filepath.Join(dir, "overwrite.sh")
			if err = os.WriteFile(config, input, 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(script, []byte("set -eu\n"+helper+body), 0600); err != nil {
				t.Fatal(err)
			}
			run := func(main, backup string) {
				t.Helper()
				cmd := exec.Command("sh", script)
				cmd.Env = append(os.Environ(), "CONFIG_FILE="+config, "EN_KEY1="+main, "EN_KEY2="+backup)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("overwrite: %v %s", err, out)
				}
			}
			run("https://stale.example.com/primary", "https://stale.example.com/backup")
			run(scenario.main, scenario.backup)
			data, _ := os.ReadFile(config)
			var cfg map[string]any
			if err = yaml.Unmarshal(data, &cfg); err != nil {
				t.Fatal(err)
			}
			providers := cfg["proxy-providers"].(map[string]any)
			if len(providers) != scenario.count {
				t.Fatalf("blank/stale provider retained: count=%d", len(providers))
			}
			for name, raw := range providers {
				p := raw.(map[string]any)
				if p["type"] != "http" || p["path"] == "" || p["interval"] != 86400 || p["health-check"] == nil || p["filter"] == nil {
					t.Fatalf("incomplete %s provider", name)
				}
				expected := scenario.main
				if name == "备用服务商" {
					expected = scenario.backup
				}
				if p["url"] != expected {
					t.Fatal("URL was modified or interpreted")
				}
			}
			if err = validateYAMLReferences(data); err != nil {
				t.Fatal(err)
			}
		})
	}
}
