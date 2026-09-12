package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMiaomiaowuConfigPreservesRulesAndAdaptsNodes(t *testing.T) {
	s, root := mihomoProFixture(t)
	input, err := os.ReadFile(filepath.Join(root, "_templates/MihomoPro.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	resources, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	if err = yaml.Unmarshal(input, &original); err != nil {
		t.Fatal(err)
	}
	var variants []map[string]any
	for _, source := range []string{"local", "upstream"} {
		data, providers, groups, rules, err := s.miaomiaowuConfig(input, resources, source)
		if err != nil {
			t.Fatal(err)
		}
		if providers != 33 || groups != 20 || rules != 29 {
			t.Fatalf("unexpected template counts: %d/%d/%d", providers, groups, rules)
		}
		var cfg map[string]any
		if err = yaml.Unmarshal(data, &cfg); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg["rules"], original["rules"]) {
			t.Fatal("YYDS rule order or targets changed")
		}
		for _, key := range []string{"version", "$schema", "proxy-providers", "authentication", "secret", "external-controller", "external-ui", "external-ui-url", "port", "socks-port", "redir-port", "mixed-port", "tproxy-port", "bind-address", "tun"} {
			if _, exists := cfg[key]; exists {
				t.Fatalf("deployment setting leaked: %s", key)
			}
		}
		if cfg["proxies"] != nil || cfg["mode"] != "rule" {
			t.Fatal("template contains real nodes or wrong mode")
		}
		allGroups := map[string]map[string]any{}
		for _, raw := range cfg["proxy-groups"].([]any) {
			group := raw.(map[string]any)
			allGroups[group["name"].(string)] = group
		}
		for _, name := range miaomiaowuBusinessNames {
			if allGroups[name] == nil {
				t.Fatalf("lost business group: %s", name)
			}
		}
		for _, name := range []string{"苹果服务", "国内流量"} {
			if !reflect.DeepEqual(allGroups[name]["proxies"], []any{"DIRECT", "故障转移", "全球手动", "全球自动"}) {
				t.Fatal("direct default was changed")
			}
		}
		if !reflect.DeepEqual(allGroups["广告拦截"]["proxies"], []any{"REJECT-DROP", "REJECT", "DIRECT"}) {
			t.Fatal("advertisement defaults were changed")
		}
		if !reflect.DeepEqual(allGroups["人工智能"]["proxies"], []any{"故障转移", "全球手动", "全球自动", "DIRECT"}) {
			t.Fatal("proxy default was changed")
		}
		for _, name := range []string{"全球手动", "全球自动", "故障转移"} {
			group := allGroups[name]
			for _, flag := range []string{"include-all", "include-all-proxies", "include-all-providers"} {
				if group[flag] != true {
					t.Fatalf("%s lacks %s", name, flag)
				}
			}
			refs := group["proxies"].([]any)
			if refs[len(refs)-2] != "__PROXY_PROVIDERS__" || refs[len(refs)-1] != "__PROXY_NODES__" {
				t.Fatal("renderer placeholders missing")
			}
			if name != "全球手动" && (group["url"] != "https://cp.cloudflare.com/generate_204" || group["interval"] != 300) {
				t.Fatal("node health check was not configured")
			}
		}
		for _, raw := range cfg["rule-providers"].(map[string]any) {
			provider := raw.(map[string]any)
			url := provider["url"].(string)
			prefix := "https://" + s.domain + "/_rule-resources/666os/" + resources.Status.ReleaseID + "/"
			if source == "upstream" {
				prefix = "https://raw.githubusercontent.com/666OS/rules/" + resources.Status.Commit + "/"
			}
			if !strings.HasPrefix(url, prefix) || !legacyKnownPath(strings.TrimPrefix(url, prefix)) {
				t.Fatalf("provider is not pinned: %s", url)
			}
			provider["url"] = strings.TrimPrefix(url, prefix)
		}
		var node yaml.Node
		if err = yaml.Unmarshal(data, &node); err != nil {
			t.Fatal(err)
		}
		var explicit func(*yaml.Node)
		explicit = func(n *yaml.Node) {
			if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag == "!!merge" {
				t.Fatal("renderer-incompatible alias or merge key remains")
			}
			for _, child := range n.Content {
				explicit(child)
			}
		}
		explicit(&node)
		variants = append(variants, cfg)
	}
	if !reflect.DeepEqual(variants[0], variants[1]) {
		t.Fatal("source switch changed something besides provider URLs")
	}
}

func TestMiaomiaowuRejectsUnsupportedSourceRules(t *testing.T) {
	s, root := mihomoProFixture(t)
	input, _ := os.ReadFile(filepath.Join(root, "_templates/MihomoPro.yaml"))
	resources, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutation := range map[string]func(map[string]any){
		"rule-format":         func(cfg map[string]any) { cfg["rules"].([]any)[0] = "DOMAIN-SUFFIX,example.com,广告拦截" },
		"rule-count":          func(cfg map[string]any) { cfg["rules"] = cfg["rules"].([]any)[1:] },
		"unknown-target":      func(cfg map[string]any) { cfg["rules"].([]any)[0] = "RULE-SET,Tracking,全球手动" },
		"missing-business":    func(cfg map[string]any) { cfg["proxy-groups"] = cfg["proxy-groups"].([]any)[1:] },
		"non-select-business": func(cfg map[string]any) { cfg["proxy-groups"].([]any)[0].(map[string]any)["type"] = "url-test" },
	} {
		t.Run(name, func(t *testing.T) {
			var cfg map[string]any
			yaml.Unmarshal(input, &cfg)
			mutation(cfg)
			data, _ := yaml.Marshal(cfg)
			if _, _, _, _, err := s.miaomiaowuConfig(data, resources, "local"); err == nil {
				t.Fatal("unsupported source was silently accepted")
			}
		})
	}
	if _, _, _, _, err := s.miaomiaowuConfig(input, resources, "other"); err == nil {
		t.Fatal("unknown source was accepted")
	}
}

func TestMiaomiaowuPublicationIsIndependentAndImmutable(t *testing.T) {
	s, root := mihomoProFixture(t)
	legacyBefore := map[string][]byte{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			legacyBefore[path], err = os.ReadFile(path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// These existing storage files must remain completely untouched. The new
	// module does not need to open or migrate either database or signing data.
	for _, name := range []string{"routing.db", "usage.db", "subscription-signing.key"} {
		path := filepath.Join(s.dataDir, name)
		legacyBefore[path] = []byte("existing-" + name)
		if err := os.WriteFile(path, legacyBefore[path], 0600); err != nil {
			t.Fatal(err)
		}
	}
	resources, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := s.ensureMiaomiaowuTemplates(resources)
	if err != nil {
		t.Fatal(err)
	}
	for path, before := range legacyBefore {
		after, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("legacy file changed: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(s.mihomoProDir(resources.Status.ReleaseID)); !os.IsNotExist(err) {
		t.Fatal("legacy MihomoPro variant namespace was modified")
	}
	mux := http.NewServeMux()
	s.registerMiaomiaowuRoutes(mux)
	unauth := httptest.NewRecorder()
	mux.ServeHTTP(unauth, httptest.NewRequest("GET", "/api/templates/miaomiaowu", nil))
	if unauth.Code != 401 {
		t.Fatal("management metadata is publicly accessible")
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	for _, option := range manifest.Options {
		req := httptest.NewRequest("GET", option.TemplateURL, nil)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, req)
		if out.Code != 200 || routingSHA256(out.Body.Bytes()) != option.SHA256 || !strings.Contains(out.Header().Get("Cache-Control"), "immutable") || out.Header().Get("X-Content-Type-Options") != "nosniff" || out.Header().Get("Content-Disposition") != "" {
			t.Fatalf("public artifact cannot be previewed after cleanup: %d %s", out.Code, out.Body.String())
		}
		req.Header.Set("If-None-Match", out.Header().Get("ETag"))
		cached := httptest.NewRecorder()
		mux.ServeHTTP(cached, req)
		if cached.Code != 304 || cached.Body.Len() != 0 {
			t.Fatal("immutable ETag was not honored")
		}
		download := httptest.NewRecorder()
		mux.ServeHTTP(download, httptest.NewRequest("GET", option.TemplateURL+"?download=1", nil))
		if download.Code != 200 || !strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment;") {
			t.Fatal("download disposition missing")
		}
	}
}

func TestMiaomiaowuCorruptionFailsClosed(t *testing.T) {
	for _, part := range []string{"input", "mrs", "published-sha", "published-symlink", "manifest"} {
		t.Run(part, func(t *testing.T) {
			s, _ := mihomoProFixture(t)
			resources, err := s.retainLegacyResources()
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := s.ensureMiaomiaowuTemplates(resources)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.legacyResourceDir(resources.Status.ReleaseID), "_templates/MihomoPro.yaml")
			switch part {
			case "mrs":
				path = filepath.Join(s.legacyResourceDir(resources.Status.ReleaseID), legacyResourcePaths()[0])
			case "published-sha", "published-symlink":
				path = filepath.Join(s.miaomiaowuDir(manifest.Revision), "local.yaml")
			case "manifest":
				path = filepath.Join(s.miaomiaowuDir(manifest.Revision), "manifest.json")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if part == "published-symlink" {
				outside := filepath.Join(s.dataDir, "outside.yaml")
				if err = os.WriteFile(outside, data, 0644); err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			} else {
				data[0] ^= 1 // Same byte count: checks must reject a SHA mismatch too.
				if err = os.WriteFile(path, data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			out := httptest.NewRecorder()
			s.miaomiaowuTemplateOptions(out, httptest.NewRequest("GET", "/", nil))
			var body struct {
				Options []miaomiaowuTemplateOption `json:"source_options"`
			}
			if err = json.Unmarshal(out.Body.Bytes(), &body); err != nil || len(body.Options) != 2 {
				t.Fatalf("missing unavailable choices: %v", err)
			}
			for _, option := range body.Options {
				if option.Available || option.Reason == "" || option.TemplateURL != "" {
					t.Fatalf("corruption implicitly fell back: %+v", option)
				}
			}
			if strings.HasPrefix(part, "published") || part == "manifest" {
				req := httptest.NewRequest("GET", "/", nil)
				req.SetPathValue("revision", manifest.Revision)
				req.SetPathValue("source", "upstream")
				out = httptest.NewRecorder()
				s.miaomiaowuTemplateFile(out, req)
				if out.Code != 503 {
					t.Fatal("incomplete/corrupt pair was publicly served")
				}
			}
		})
	}
}

func TestMiaomiaowuPublicPathBoundaries(t *testing.T) {
	s, _ := mihomoProFixture(t)
	for _, test := range []struct{ revision, source string }{{"../settings", "local"}, {"..", "local"}, {"/etc", "upstream"}, {"abc/def", "local"}, {"unpublished", "local"}, {"valid", "other"}, {"valid", "../local"}} {
		req := httptest.NewRequest("GET", "/", nil)
		req.SetPathValue("revision", test.revision)
		req.SetPathValue("source", test.source)
		out := httptest.NewRecorder()
		s.miaomiaowuTemplateFile(out, req)
		if out.Code != 404 {
			t.Fatalf("unexpected public path %s/%s: %d", test.revision, test.source, out.Code)
		}
	}
	if _, err := os.Stat(filepath.Join(s.dataDir, "rule-templates")); !os.IsNotExist(err) {
		t.Fatal("public request generated artifacts")
	}
}

func TestMiaomiaowuConcurrentPublish(t *testing.T) {
	s, _ := mihomoProFixture(t)
	resources, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan miaomiaowuTemplateManifest, 12)
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manifest, err := s.ensureMiaomiaowuTemplates(resources)
			if err != nil {
				errors <- err
			} else {
				results <- manifest
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	var first *miaomiaowuTemplateManifest
	for manifest := range results {
		if first == nil {
			first = &manifest
		} else if !reflect.DeepEqual(*first, manifest) {
			t.Fatal("concurrent callers saw different publications")
		}
	}
	entries, err := os.ReadDir(s.miaomiaowuDir(resources.Status.ReleaseID))
	if err != nil || len(entries) != 3 {
		t.Fatalf("publication was incomplete: %v", err)
	}
	parent, err := os.ReadDir(filepath.Dir(s.miaomiaowuDir(resources.Status.ReleaseID)))
	if err != nil || len(parent) != 1 {
		t.Fatal("temporary publication was left behind")
	}
}
