package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

func templateGroupingFixture(t *testing.T) (*server, string) {
	t.Helper()
	s, root := mihomoProFixture(t)
	input, err := os.ReadFile("templates/ppanel_openclash_pro_cn.gotmpl")
	if err != nil {
		t.Fatal(err)
	}
	input = bytes.ReplaceAll(input, []byte("__RULES_BASE_URL__"), []byte("https://"+s.domain+"/"))
	input = bytes.ReplaceAll(input, []byte("https://github.com/Koolson/Qure/raw/master/IconSet/Color/"), []byte("https://"+s.domain+"/_assets/icons/"))
	for _, client := range []string{"clash", "mihomo", "openclash"} {
		if err = os.WriteFile(filepath.Join(root, "_templates", "clients", client+".gotmpl"), input, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return s, root
}

func groupingJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func groupingProfileName(t *testing.T, profile templateGroupingProfile, name string) templateGroupingProfile {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(groupingJSON(t, profile), &fields); err != nil {
		t.Fatal(err)
	}
	fields["name"] = name
	if err := json.Unmarshal(groupingJSON(t, fields), &profile); err != nil {
		t.Fatal(err)
	}
	return profile
}

func TestGroupingSettingsInheritanceAndReadOnlyDefaults(t *testing.T) {
	s := &server{dataDir: t.TempDir(), domain: "rules.example.com"}
	read := func(scope string) map[string]any {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/template-grouping/profiles/"+scope, nil)
		r.SetPathValue("scope", scope)
		w := httptest.NewRecorder()
		s.templateGroupingGetProfile(w, r)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	put := func(scope string, profile *templateGroupingProfile, inherit bool) {
		t.Helper()
		r := httptest.NewRequest("PUT", "/api/template-grouping/profiles/"+scope, bytes.NewReader(groupingJSON(t, map[string]any{"inherit": inherit, "profile": profile})))
		r.SetPathValue("scope", scope)
		w := httptest.NewRecorder()
		s.templateGroupingPutProfile(w, r)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	initial := read("miaomiaowu")
	if initial["inherit"] != true {
		t.Fatal("default must inherit common")
	}
	if _, err := os.Stat(s.templateGroupingSettingsPath()); !os.IsNotExist(err) {
		t.Fatal("GET persisted settings")
	}
	common := groupingProfileName(t, defaultTemplateGroupingProfile(), "共同设置")
	put("common", &common, false)
	if read("ppanel")["profile"].(map[string]any)["name"] != "共同设置" {
		t.Fatal("inheritance lost")
	}
	independent := groupingProfileName(t, common, "独立设置")
	put("ppanel", &independent, false)
	common = groupingProfileName(t, common, "共同设置二")
	put("common", &common, false)
	if read("ppanel")["profile"].(map[string]any)["name"] != "独立设置" {
		t.Fatal("common overwrote independent settings")
	}
	if read("overwrite")["profile"].(map[string]any)["name"] != "共同设置二" {
		t.Fatal("common did not update inheriting target")
	}
	put("ppanel", nil, true)
	if read("ppanel")["profile"].(map[string]any)["name"] != "共同设置二" {
		t.Fatal("restoring inheritance failed")
	}
	if err := os.WriteFile(s.templateGroupingSettingsPath(), []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("scope", "common")
	w := httptest.NewRecorder()
	s.templateGroupingGetProfile(w, r)
	if w.Code != 503 {
		t.Fatal("invalid persisted settings silently reset")
	}
}

func TestGroupingCommonSaveAtomicallyAppliesInheritance(t *testing.T) {
	s := &server{dataDir: t.TempDir(), domain: "rules.example.com"}
	profile := groupingProfileName(t, defaultTemplateGroupingProfile(), "单次共用设置")
	r := httptest.NewRequest("PUT", "/api/template-grouping/profiles/common", bytes.NewReader(groupingJSON(t, map[string]any{"profile": profile, "inherit": false, "apply_scope": "ppanel"})))
	r.SetPathValue("scope", "common")
	w := httptest.NewRecorder()
	s.templateGroupingPutProfile(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	store, err := s.templateGroupingReadStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Scopes) != 2 || !store.Scopes["ppanel"].Inherit || store.Scopes["common"].Inherit {
		t.Fatal("common and target were not saved together")
	}
	effective, inherited := templateGroupingEffective(store, "ppanel")
	if !inherited || !reflect.DeepEqual(effective, store.Scopes["common"].Profile) {
		t.Fatal("selected target did not inherit new common settings")
	}
	before, _ := os.ReadFile(s.templateGroupingSettingsPath())
	for _, test := range [][2]string{{"common", "common"}, {"common", "invalid"}, {"ppanel", "miaomiaowu"}} {
		r = httptest.NewRequest("PUT", "/", bytes.NewReader(groupingJSON(t, map[string]any{"profile": profile, "inherit": false, "apply_scope": test[1]})))
		r.SetPathValue("scope", test[0])
		w = httptest.NewRecorder()
		s.templateGroupingPutProfile(w, r)
		if w.Code != 400 {
			t.Fatalf("accepted invalid cross-scope save: %v", test)
		}
	}
	after, _ := os.ReadFile(s.templateGroupingSettingsPath())
	if !bytes.Equal(before, after) {
		t.Fatal("invalid save changed persisted settings")
	}
}

func TestGroupingGenerationTargetsAndImmutableDelivery(t *testing.T) {
	s, root := templateGroupingFixture(t)
	profile := defaultTemplateGroupingProfile()
	before, err := os.ReadFile(filepath.Join(root, "_templates/MihomoPro.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.registerTemplateGroupingRoutes(mux)
	for _, path := range []string{"/api/template-grouping/profiles/common", "/api/template-grouping/preview", "/api/template-grouping/generate"} {
		method := "POST"
		if strings.Contains(path, "profiles") {
			method = "GET"
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader("{}")))
		if w.Code != 401 {
			t.Fatalf("unprotected management path %s", path)
		}
	}
	var built []templateGroupingGeneration
	for _, target := range [][2]string{{"miaomiaowu", "clash"}, {"ppanel", "clash"}, {"ppanel", "mihomo"}, {"ppanel", "openclash"}, {"overwrite", "mihomo"}} {
		for _, source := range []string{"local", "upstream"} {
			generated, err := s.templateGroupingGenerate(target[0], target[1], source, profile)
			if err != nil {
				t.Fatalf("%v/%s: %v", target, source, err)
			}
			if generated.ProviderCount != 33 || generated.GroupCount < 40 || generated.RuleCount < 29 || len(generated.Warnings) == 0 {
				t.Fatalf("missing generation metadata: %+v", generated)
			}
			if generated.ProfileHash != routingSHA256(groupingJSON(t, generated.Profile)) {
				t.Fatal("profile digest mismatch")
			}
			built = append(built, generated)
			again, err := s.templateGroupingGenerate(target[0], target[1], source, profile)
			if err != nil || !reflect.DeepEqual(generated, again) {
				t.Fatal("identical generation was not immutable", err)
			}
			for _, file := range generated.Artifacts {
				if routingSHA256([]byte(file.Content)) != file.SHA256 {
					t.Fatal("response content does not match digest")
				}
				if strings.HasSuffix(file.Name, ".conf") {
					if !strings.Contains(file.Content, "/_grouped-templates/"+generated.ID+"/config.yaml") || !strings.Contains(file.Content, "CoralBay-Grouped-"+generated.ID[:16]+".yaml") || !strings.Contains(file.Content, "force=true") {
						t.Fatal("overwrite not paired to configuration")
					}
				} else if target[0] != "ppanel" {
					var cfg map[string]any
					if err := yaml.Unmarshal([]byte(file.Content), &cfg); err != nil {
						t.Fatal(err)
					}
					if len(cfg["proxy-groups"].([]any)) != generated.GroupCount {
						t.Fatal("group metadata mismatch")
					}
				}
			}
		}
	}
	after, _ := os.ReadFile(filepath.Join(root, "_templates/MihomoPro.yaml"))
	if !bytes.Equal(before, after) {
		t.Fatal("source template mutated")
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	for _, generation := range built {
		for _, file := range generation.Artifacts {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("GET", file.URL, nil))
			if w.Code != 200 || w.Body.String() != file.Content || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
				t.Fatalf("lost artifact after source cleanup: %d", w.Code)
			}
			r := httptest.NewRequest("GET", file.URL, nil)
			r.Header.Set("If-None-Match", w.Header().Get("ETag"))
			cached := httptest.NewRecorder()
			mux.ServeHTTP(cached, r)
			if cached.Code != 304 {
				t.Fatal("ETag not honored")
			}
			w = httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("GET", file.DownloadURL, nil))
			if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), file.Name) {
				t.Fatal("download filename lost")
			}
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/_grouped-templates/"+built[0].ID+"/manifest.json", nil))
	if w.Code != 404 {
		t.Fatal("manifest became public")
	}
}

func TestGroupingPublicationRejectsCorruptionAndInvalidTargets(t *testing.T) {
	s, _ := templateGroupingFixture(t)
	for _, target := range [][3]string{{"miaomiaowu", "surge", "local"}, {"ppanel", "stash", "local"}, {"overwrite", "mihomo", "bad"}, {"common", "mihomo", "local"}} {
		if _, err := s.templateGroupingGenerate(target[0], target[1], target[2], defaultTemplateGroupingProfile()); err == nil {
			t.Fatal("unsupported format accepted", target)
		}
	}
	generated, err := s.templateGroupingGenerate("miaomiaowu", "clash", "local", defaultTemplateGroupingProfile())
	if err != nil {
		t.Fatal(err)
	}
	file := generated.Artifacts[0]
	if err := os.WriteFile(filepath.Join(s.templateGroupingDir(generated.ID), file.Name), []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.templateGroupingGenerate("miaomiaowu", "clash", "local", defaultTemplateGroupingProfile()); err == nil {
		t.Fatal("immutable corrupted artifact was overwritten")
	}
	r := httptest.NewRequest("GET", file.URL, nil)
	r.SetPathValue("id", generated.ID)
	r.SetPathValue("file", file.Name)
	w := httptest.NewRecorder()
	s.templateGroupingFileHandler(w, r)
	if w.Code != 503 {
		t.Fatal("corrupt file was served")
	}
}

func TestGroupingConcurrentPublicationAndProfileRevision(t *testing.T) {
	s, _ := templateGroupingFixture(t)
	profile := defaultTemplateGroupingProfile()
	var wg sync.WaitGroup
	results := make(chan templateGroupingGeneration, 4)
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.templateGroupingGenerate("miaomiaowu", "clash", "local", profile)
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for result := range results {
		if id != "" && id != result.ID {
			t.Fatal("concurrent generation diverged")
		}
		id = result.ID
	}
	changed := groupingProfileName(t, profile, "新的方案")
	newGeneration, err := s.templateGroupingGenerate("miaomiaowu", "clash", "local", changed)
	if err != nil || newGeneration.ID == id {
		t.Fatal("changed profile reused old URL", err)
	}
	if _, err := s.templateGroupingReadGeneration(id, true); err != nil {
		t.Fatal("old artifact changed")
	}
}

func TestGroupingPPanelPreservesNodeRendererAndAppliesSettings(t *testing.T) {
	s, root := templateGroupingFixture(t)
	original, err := os.ReadFile(filepath.Join(root, "_templates/clients/mihomo.gotmpl"))
	if err != nil {
		t.Fatal(err)
	}
	profile := defaultTemplateGroupingProfile()
	var settings map[string]any
	_ = json.Unmarshal(groupingJSON(t, profile), &settings)
	settings["dns_mode"] = "redir-host"
	settings["ipv6"] = "off"
	settings["sniffer"] = "off"
	_ = json.Unmarshal(groupingJSON(t, settings), &profile)
	generated, err := s.templateGroupingGenerate("ppanel", "mihomo", "local", profile)
	if err != nil {
		t.Fatal(err)
	}
	output := generated.Artifacts[0].Content
	nodeBlock := func(text string) string {
		_, tail, ok := strings.Cut(text, "\nproxies:")
		if !ok {
			t.Fatal("node section missing")
		}
		head, _, ok := strings.Cut(tail, "\nproxy-groups:")
		if !ok {
			t.Fatal("group section missing")
		}
		return head
	}
	if nodeBlock(string(original)) != nodeBlock(output) {
		t.Fatal("PPanel node renderer changed")
	}
	if !strings.Contains(output, "enhanced-mode: redir-host") || !strings.Contains(output, "\nipv6: false\n") || !strings.Contains(output, "sniffer:\n    enable: false") {
		t.Fatal("explicit PPanel advanced settings not applied")
	}
	if strings.Contains(output, "__CORALBAY_PROXY_NODES__") {
		t.Fatal("internal PPanel node marker leaked")
	}
}

func TestGroupingPPanelRenderedChoiceOrderAndLiteralSettings(t *testing.T) {
	s, _ := templateGroupingFixture(t)
	profile := defaultTemplateGroupingProfile()
	profile.TestURL = "https://example.com/test?value={{.Password}}"
	generated, err := s.templateGroupingGenerate("ppanel", "mihomo", "local", profile)
	if err != nil {
		t.Fatal(err)
	}
	_, tail, ok := strings.Cut(generated.Artifacts[0].Content, "\nproxy-groups:")
	if !ok {
		t.Fatal("no groups")
	}
	source := "{{ $supportedProxies := . }}\nproxy-groups:" + tail
	tpl, err := template.New("groups").Funcs(template.FuncMap{"quote": func(v any) string { return strconv.Quote(fmt.Sprint(v)) }}).Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, nodes := range [][]map[string]string{nil, {{"Name": "日本 01"}, {"Name": "越南: \"特殊节点\""}}} {
		var rendered bytes.Buffer
		if err = tpl.Execute(&rendered, nodes); err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		if err = yaml.Unmarshal(rendered.Bytes(), &cfg); err != nil {
			t.Fatal(err)
		}
		for _, raw := range cfg["proxy-groups"].([]any) {
			group := raw.(map[string]any)
			if group["type"] == "url-test" && group["url"] != profile.TestURL {
				t.Fatal("setting interpreted as Go template code")
			}
			if group["name"] != "人工智能" {
				continue
			}
			choices := group["proxies"].([]any)
			if !reflect.DeepEqual(choices[:3], []any{"全球自动", "全球手动", "默认出口"}) {
				t.Fatalf("incorrect first choices: %v", choices[:3])
			}
			for index, node := range nodes {
				if choices[3+index] != node["Name"] {
					t.Fatalf("direct node lost or wrong position: %v", choices)
				}
			}
			if choices[3+len(nodes)] != "香港自动" {
				t.Fatalf("regional modes must follow individual nodes: %v", choices)
			}
		}
	}
}
