package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGroupingHistoryLifecyclePairedFilesAndLegacy(t *testing.T) {
	s, _ := templateGroupingFixture(t)
	generate := func() templateGroupingGeneration {
		t.Helper()
		r := httptest.NewRequest("POST", "/", bytes.NewReader(groupingJSON(t, map[string]any{"scope": "overwrite", "client": "mihomo", "source": "local"})))
		w := httptest.NewRecorder()
		s.templateGroupingGenerateHandler(w, r)
		if w.Code != 200 {
			t.Fatalf("generate: %d %s", w.Code, w.Body.String())
		}
		var result templateGroupingGeneration
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first, second := generate(), generate()
	if first.ID != second.ID || first.CreatedAt != second.CreatedAt || len(first.Artifacts) != 2 {
		t.Fatal("same input must preserve paired immutable version and first date")
	}
	list := func(archived bool) []templateGroupingHistoryItem {
		t.Helper()
		url := "/?scope=overwrite"
		if archived {
			url += "&archived=true"
		}
		w := httptest.NewRecorder()
		s.groupingHistory(w, httptest.NewRequest("GET", url, nil))
		if w.Code != 200 {
			t.Fatalf("history: %d %s", w.Code, w.Body.String())
		}
		var result struct {
			Items []templateGroupingHistoryItem `json:"items"`
			Total int                           `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Items
	}
	items := list(false)
	if len(items) != 1 || items[0].GenerationCount != 2 || items[0].LastGeneratedAt == "" || items[0].Artifacts[0].Content != "" {
		t.Fatalf("history metadata: %+v", items)
	}
	change := func(method string) {
		t.Helper()
		r := httptest.NewRequest(method, "/", nil)
		r.SetPathValue("id", first.ID)
		w := httptest.NewRecorder()
		s.groupingHistoryRecycle(w, r)
		if w.Code != 200 {
			t.Fatalf("recycle: %d %s", w.Code, w.Body.String())
		}
	}
	change("DELETE")
	if len(list(false)) != 0 || len(list(true)) != 1 {
		t.Fatal("recycle list separation failed")
	}
	for _, file := range first.Artifacts {
		r := httptest.NewRequest("GET", file.URL, nil)
		r.SetPathValue("id", first.ID)
		r.SetPathValue("file", file.Name)
		r.Header.Set("If-None-Match", `"`+file.SHA256+`"`)
		w := httptest.NewRecorder()
		s.templateGroupingFileHandler(w, r)
		if w.Code != 410 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("recycled file remained available: %d", w.Code)
		}
	}
	if _, err := s.templateGroupingGenerate("overwrite", "mihomo", "local", first.Profile); err == nil {
		t.Fatal("regeneration resurrected recycled version without restore")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("id", first.ID)
	w := httptest.NewRecorder()
	s.groupingHistoryDetail(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "config.yaml") || !strings.Contains(w.Body.String(), "proxy-groups") {
		t.Fatal("admin cannot inspect recycled paired version")
	}
	change("POST")
	for _, file := range first.Artifacts {
		r := httptest.NewRequest("GET", file.URL, nil)
		r.SetPathValue("id", first.ID)
		r.SetPathValue("file", file.Name)
		w := httptest.NewRecorder()
		s.templateGroupingFileHandler(w, r)
		if w.Code != 200 || w.Body.String() != file.Content {
			t.Fatal("restore changed bytes or original URL")
		}
	}
	// Previously released grouping-v1 manifests have no lifecycle sidecar.
	manifestPath := filepath.Join(s.templateGroupingDir(first.ID), "manifest.json")
	old := first
	old.Generator = "grouping-v1"
	old.Artifacts = append([]templateGroupingArtifact{}, first.Artifacts...)
	for i := range old.Artifacts {
		old.Artifacts[i].Content = ""
	}
	if err := os.WriteFile(manifestPath, groupingJSON(t, old), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.groupingLifecyclePath(first.ID)); err != nil {
		t.Fatal(err)
	}
	items = list(false)
	if len(items) != 1 || items[0].GenerationCount != 1 || items[0].CreatedAt != first.CreatedAt {
		t.Fatal("legacy history not adopted without migration")
	}
	if _, err := s.templateGroupingReadGeneration(first.ID, true); err != nil {
		t.Fatal("legacy download rejected", err)
	}
}

func TestGroupingAdvancedBaselineMatchesActualInputs(t *testing.T) {
	s, root := templateGroupingFixture(t)
	for _, target := range [][2]string{{"miaomiaowu", "clash"}, {"overwrite", "mihomo"}, {"ppanel", "mihomo"}} {
		w := httptest.NewRecorder()
		s.groupingAdvancedBaseline(w, httptest.NewRequest("GET", "/?scope="+target[0]+"&client="+target[1], nil))
		if w.Code != 200 {
			t.Fatalf("baseline %v: %d %s", target, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result["scope"] != target[0] || result["client"] != target[1] || result["revision"] == "" {
			t.Fatal("baseline provenance absent")
		}
		if target[0] == "ppanel" && result["dns"].(map[string]any)["enable"] != true {
			t.Fatal("actual PPanel DNS was misrepresented")
		}
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.groupingAdvancedBaseline(w, httptest.NewRequest("GET", "/?scope=ppanel&client=stash", nil))
	if w.Code != 400 {
		t.Fatal("unsupported advanced client accepted")
	}
	base := groupingBaseConfig(t)
	p := defaultTemplateGroupingProfile()
	out, err := applyTemplateGrouping(base, p, "mihomo", "rules.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"dns", "ipv6", "sniffer"} {
		if !reflect.DeepEqual(base[key], out[key]) {
			t.Fatalf("inherit rewrote %s", key)
		}
	}
}

func TestGroupingHistoryManagementRoutesRequireAuth(t *testing.T) {
	s := &server{dataDir: t.TempDir(), domain: "rules.example.com"}
	mux := http.NewServeMux()
	s.registerTemplateGroupingRoutes(mux)
	for _, target := range [][2]string{{"GET", "/api/template-grouping/history?scope=miaomiaowu"}, {"GET", "/api/template-grouping/history/" + strings.Repeat("a", 64)}, {"DELETE", "/api/template-grouping/history/" + strings.Repeat("a", 64)}, {"POST", "/api/template-grouping/history/" + strings.Repeat("a", 64) + "/restore"}, {"GET", "/api/template-grouping/advanced?scope=ppanel&client=mihomo"}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(target[0], target[1], nil))
		if w.Code != 401 {
			t.Fatalf("unprotected history/baseline: %v %d", target, w.Code)
		}
	}
}
