package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyFixture(t *testing.T) (*server, string) {
	t.Helper()
	s := &server{dataDir: t.TempDir(), domain: "rules.example.com"}
	revision := strings.Repeat("a", 40) + "-" + strings.Repeat("b", 12) + "-v4.12.0-" + strings.Repeat("c", 12)
	root := filepath.Join(s.dataDir, "releases", revision)
	write := func(path, body string) {
		t.Helper()
		destination := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range legacyResourcePaths() {
		write(path, "binary:"+path)
		if source := readableSource(path); source != "" {
			write("_sources/geo/"+source, "# fixture\nexample.com\nfull:exact.example.com\n")
		}
	}
	status := mirrorStatus{OK: true, Commit: strings.Repeat("a", 40), GeoCommit: strings.Repeat("b", 40), ReleaseID: revision, MirrorDomain: s.domain, GeneratorVersion: "4.12.0"}
	content, _ := json.Marshal(status)
	write("_mirror/status.json", string(content))
	write("_templates/clients/mihomo.gotmpl", "{{ template \"AllProxies\" . }}\nrule-providers:\n  google: {url: https://"+s.domain+"/mihomo/domain/Google.mrs}\nicon: https://"+s.domain+"/_assets/icons/Google.png\n")
	if err := os.Symlink(filepath.Join("releases", revision), filepath.Join(s.dataDir, "current")); err != nil {
		t.Fatal(err)
	}
	return s, root
}

func TestLegacyFixedResourcesSurviveLegacyCleanup(t *testing.T) {
	s, root := legacyFixture(t)
	manifest, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.SetPathValue("revision", manifest.Status.ReleaseID)
	req.SetPathValue("file", "mihomo/domain/Google.mrs")
	response := httptest.NewRecorder()
	s.legacyResourceFile(response, req)
	if response.Code != 200 || response.Body.String() != "binary:mihomo/domain/Google.mrs" {
		t.Fatalf("lost fixed resource after cleanup: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("missing immutable delivery")
	}
	req.Header.Set("If-None-Match", response.Header().Get("ETag"))
	response = httptest.NewRecorder()
	s.legacyResourceFile(response, req)
	if response.Code != 304 {
		t.Fatal("ETag not honored")
	}
}

func TestLegacyResourceBoundariesAndCorruption(t *testing.T) {
	s, _ := legacyFixture(t)
	manifest, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../settings.json", "_sources/geo/site/google.txt", "_templates/clients/mihomo.gotmpl", "mihomo/domain/Google.mrs/../../manifest.json"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.SetPathValue("revision", manifest.Status.ReleaseID)
		req.SetPathValue("file", path)
		response := httptest.NewRecorder()
		s.legacyResourceFile(response, req)
		if response.Code != 404 {
			t.Fatalf("unexpected public path %s: %d", path, response.Code)
		}
	}
	if err := os.WriteFile(filepath.Join(s.legacyResourceDir(manifest.Status.ReleaseID), "mihomo/domain/Google.mrs"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.SetPathValue("revision", manifest.Status.ReleaseID)
	req.SetPathValue("file", "mihomo/domain/Google.mrs")
	response := httptest.NewRecorder()
	s.legacyResourceFile(response, req)
	if response.Code != 503 {
		t.Fatal("corrupt immutable artifact was served")
	}
}

func TestLegacyLocalDetailNeverContactsUpstream(t *testing.T) {
	s, _ := legacyFixture(t)
	s.resourceHTTPClient = &http.Client{Transport: routingRuleTestTransport(func(*http.Request) (*http.Response, error) { t.Fatal("local read contacted upstream"); return nil, nil })}
	req := httptest.NewRequest("GET", "/api/resources/666os/details?path=mihomo/domain/Google.mrs&source=local&q=exact&page=9223372036854775807&page_size=1", nil)
	response := httptest.NewRecorder()
	s.legacyResourceDetails(response, req)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var body struct {
		Total       int      `json:"total"`
		Entries     []string `json:"entries"`
		ContentKind string   `json:"content_kind"`
		GeoRevision string   `json:"geo_revision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Entries) != 1 || body.ContentKind != "associated_geo" || body.GeoRevision != strings.Repeat("b", 40) {
		t.Fatalf("bad detail: %+v", body)
	}
}

func TestLegacyUpstreamCheckAndDiffDoNotPublish(t *testing.T) {
	s, root := legacyFixture(t)
	s.resourceHTTPClient = &http.Client{Transport: routingRuleTestTransport(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/git/ref/heads/") {
			branch := filepath.Base(req.URL.Path)
			revision := strings.Repeat("c", 40)
			if branch == "geo" {
				revision = strings.Repeat("d", 40)
			}
			return routingRuleTestResponse(req, 200, fmt.Sprintf(`{"ref":"refs/heads/%s","object":{"sha":"%s","type":"commit"}}`, branch, revision)), nil
		}
		if strings.HasSuffix(req.URL.Path, ".txt") {
			return routingRuleTestResponse(req, 200, "example.com\nnew.example.com\n"), nil
		}
		return routingRuleTestResponse(req, 200, "new binary"), nil
	})}
	state, err := s.legacyCheckUpstream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != strings.Repeat("c", 40) || state.GeoRevision != strings.Repeat("d", 40) {
		t.Fatal("branch provenance lost")
	}
	req := httptest.NewRequest("GET", "/api/resources/666os/details?path=mihomo/domain/Google.mrs&source=diff", nil)
	response := httptest.NewRecorder()
	s.legacyResourceDetails(response, req)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var body struct {
		Added   int  `json:"added_count"`
		Removed int  `json:"removed_count"`
		Same    bool `json:"same"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if body.Added != 1 || body.Removed != 1 || body.Same {
		t.Fatalf("incorrect diff: %+v", body)
	}
	actual, _, err := s.legacyCurrent()
	expectedRoot, _ := filepath.EvalSymlinks(root)
	if err != nil || actual != expectedRoot {
		t.Fatal("upstream read changed local publication")
	}
	content, _ := os.ReadFile(filepath.Join(root, "mihomo/domain/Google.mrs"))
	if string(content) != "binary:mihomo/domain/Google.mrs" {
		t.Fatal("upstream read overwrote local artifact")
	}
}

func TestLegacyTemplateSourceOnlyChangesMappedRuleURLs(t *testing.T) {
	s, root := legacyFixture(t)
	manifest, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "_templates/clients/mihomo.gotmpl"))
	for _, source := range []string{"local", "upstream"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.SetPathValue("revision", manifest.Status.ReleaseID)
		req.SetPathValue("source", source)
		req.SetPathValue("client", "mihomo")
		response := httptest.NewRecorder()
		s.legacyResourceTemplate(response, req)
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		want := "https://" + s.domain + "/_rule-resources/666os/" + manifest.Status.ReleaseID + "/mihomo/domain/Google.mrs"
		if source == "upstream" {
			want = "https://raw.githubusercontent.com/666OS/rules/" + manifest.Status.Commit + "/mihomo/domain/Google.mrs"
		}
		if !strings.Contains(response.Body.String(), want) || !strings.Contains(response.Body.String(), "icon: https://"+s.domain+"/_assets/icons/Google.png") {
			t.Fatalf("source mapping wrong: %s", response.Body.String())
		}
	}
	after, _ := os.ReadFile(filepath.Join(root, "_templates/clients/mihomo.gotmpl"))
	if string(before) != string(after) {
		t.Fatal("shared legacy template mutated")
	}
	if len(s.legacyTemplateOptions("surge", manifest)) != 0 {
		t.Fatal("derived template falsely offers upstream equivalent")
	}
}

func TestLegacyDetailValidatesServedBytesAndAssociatedGeo(t *testing.T) {
	for _, part := range []string{"binary", "geo"} {
		t.Run(part, func(t *testing.T) {
			s, _ := legacyFixture(t)
			manifest, err := s.retainLegacyResources()
			if err != nil {
				t.Fatal(err)
			}
			path := "mihomo/domain/Google.mrs"
			target := path
			if part == "geo" {
				target = "_sources/geo/" + readableSource(path)
			}
			filename := filepath.Join(s.legacyResourceDir(manifest.Status.ReleaseID), filepath.FromSlash(target))
			before, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			corrupt := []byte(strings.Repeat("x", len(before))) // Equal size must still fail SHA verification.
			if err = os.WriteFile(filename, corrupt, 0644); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "/api/resources/666os/details?path="+path+"&source=local", nil)
			out := httptest.NewRecorder()
			s.legacyResourceDetails(out, req)
			var result map[string]any
			if err = json.Unmarshal(out.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if part == "binary" && (result["cached"] != false || result["local_url"] != nil || result["local_error"] == nil) {
				t.Fatalf("corrupt binary shown ready: %v", result)
			}
			if part == "geo" && (result["cached"] != true || result["readable"] != false || result["readable_error"] == nil) {
				t.Fatalf("corrupt associated geo shown readable: %v", result)
			}
		})
	}
}
