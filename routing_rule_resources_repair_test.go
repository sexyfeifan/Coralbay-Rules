package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func routingRepairSnapshotFiles(t *testing.T, s *server, revision string) []string {
	t.Helper()
	files, err := os.ReadDir(filepath.Join(s.routingRulesDir(), "releases", revision))
	if err != nil {
		t.Fatal(err)
	}
	var result []string
	for _, file := range files {
		if routingHashPattern.MatchString(strings.TrimSuffix(file.Name(), ".json")) {
			result = append(result, file.Name())
		}
	}
	return result
}

func routingRepairCatalogItem(t *testing.T, s *server, id string) map[string]any {
	t.Helper()
	for _, item := range s.routingCatalogResponse()["rules"].([]map[string]any) {
		if item["id"] == id {
			return item
		}
	}
	t.Fatal("rule missing from catalog")
	return nil
}

func TestRoutingResourceRepairRebuildsManifestWithoutChangingFixedContent(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	if _, err := s.syncRoutingRules(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, _, _ := s.readRoutingRuleState()
	files := routingRepairSnapshotFiles(t, s, before.Revision)
	want := append([]byte(nil), routingResourceTestGet(s, before.Revision, "openai").Body.Bytes()...)
	manifestPath := filepath.Join(s.routingRulesDir(), "releases", before.Revision, "resources.json")
	if err := os.WriteFile(manifestPath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := routingRepairCatalogItem(t, s, "openai"); result["verified"] != false || result["health"] != "corrupt" {
		t.Fatalf("corrupt manifest reported ready: %+v", result)
	}
	if result, err := s.syncRoutingRules(context.Background()); err != nil || result.Stale {
		t.Fatalf("sync cannot safely rebuild manifest: %+v %v", result, err)
	}
	after, _, _ := s.readRoutingRuleState()
	if after.Revision != before.Revision || after.Snapshot != before.Snapshot || !reflect.DeepEqual(files, routingRepairSnapshotFiles(t, s, before.Revision)) {
		t.Fatal("unchanged manifest recovery replaced snapshot or accumulated candidates")
	}
	if got := routingResourceTestGet(s, before.Revision, "openai"); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), want) {
		t.Fatal("fixed content changed after manifest reconstruction")
	}
	// The explicit repair endpoint also works with both manifest and raw damage,
	// and performs no branch check even when the branch API is unavailable.
	if err := os.WriteFile(manifestPath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.routingRawPath(before.Revision, "openai")); err != nil {
		t.Fatal(err)
	}
	fixture.failedBranch = true
	requests := len(fixture.requests)
	w := httptest.NewRecorder()
	s.routingRuleRepairHandler(w, httptest.NewRequest(http.MethodPost, "/api/routing/rules/repair", strings.NewReader("{}")))
	if w.Code != 200 {
		t.Fatalf("repair: %d %s", w.Code, w.Body.String())
	}
	if len(fixture.requests) != requests+1 || fixture.requests[requests] == routingBranchURL {
		t.Fatalf("repair contacted branch API or redownloaded intact resources: %v", fixture.requests[requests:])
	}
	if got := routingResourceTestGet(s, before.Revision, "openai"); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), want) {
		t.Fatal("explicit repair did not restore exact fixed bytes")
	}
}

func TestRoutingResourceSyncRepairsKnownRawWithBranchAPIUnavailable(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	if _, err := s.syncRoutingRules(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, _, _ := s.readRoutingRuleState()
	if err := os.Remove(s.routingRawPath(before.Revision, "openai")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.routingRawPath(before.Revision, "netflix"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	fixture.failedBranch = true
	requests := len(fixture.requests)
	result, err := s.syncRoutingRules(context.Background())
	if err != nil || !result.Stale || !strings.Contains(result.LastError, "版本检查失败") {
		t.Fatalf("version API warning lost: %+v %v", result, err)
	}
	if len(fixture.requests) != requests+3 {
		t.Fatalf("wanted branch + two known raw repairs: %v", fixture.requests[requests:])
	}
	local, err := s.loadPublishedRoutingRuleSnapshot(context.Background(), []string{"openai", "netflix"})
	if err != nil || local.Revision != before.Revision {
		t.Fatalf("known commit raw repair blocked by branch failure: %+v %v", local, err)
	}
	if row := routingRepairCatalogItem(t, s, "openai"); row["verified"] != true || row["health"] != "verified" {
		t.Fatalf("repaired local health inherited branch availability: %+v", row)
	}
}

func TestRoutingResourceHistoricalRepairPreservesActivePointerAndHashes(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	first, err := s.syncRoutingRules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), routingResourceTestGet(s, first.Revision, "openai").Body.Bytes()...)
	fixture.revision = strings.Repeat("b", 40)
	if _, err := s.syncRoutingRules(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, _, _ := s.readRoutingRuleState()
	if err := os.Remove(s.routingRawPath(first.Revision, "openai")); err != nil {
		t.Fatal(err)
	}
	// The fixture now serves B bytes even for A's URL. A fixed resource must not
	// be "repaired" with a different commit's contents.
	if _, err := s.repairRoutingRuleResources(context.Background(), first.Revision); err == nil {
		t.Fatal("historical fixed SHA changed during repair")
	}
	after, _, _ := s.readRoutingRuleState()
	if after != before {
		t.Fatal("failed historical repair changed active pointer")
	}
	base := fixture.client()
	s.routingHTTPClient = &http.Client{Transport: routingRuleTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == routingBranchURL {
			t.Error("historical repair checked moving branch")
		}
		if strings.Contains(req.URL.Path, "/"+first.Revision+"/") {
			return routingRuleTestResponse(req, 200, string(want)), nil
		}
		return base.Transport.RoundTrip(req)
	})}
	files := routingRepairSnapshotFiles(t, s, first.Revision)
	if _, err := s.repairRoutingRuleResources(context.Background(), first.Revision); err != nil {
		t.Fatal(err)
	}
	after, _, _ = s.readRoutingRuleState()
	if after != before {
		t.Fatal("successful historical repair switched the active version")
	}
	if got := routingResourceTestGet(s, first.Revision, "openai"); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), want) {
		t.Fatal("historical URL not restored exactly")
	}
	if !reflect.DeepEqual(files, routingRepairSnapshotFiles(t, s, first.Revision)) {
		t.Fatal("unchanged historical repair accumulated a duplicate snapshot")
	}
}

func TestRoutingResourceFailedPublicationCleansCandidateSnapshots(t *testing.T) {
	s, _ := routingResourceTestServer(t)
	first, err := s.syncRoutingRules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := s.readRoutingRuleState()
	files := routingRepairSnapshotFiles(t, s, first.Revision)
	manifest := filepath.Join(s.routingRulesDir(), "releases", first.Revision, "resources.json")
	// A directory where the manifest must be written creates a deterministic
	// publication failure on both macOS and Linux, after candidates validate.
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(manifest, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.routingRawPath(first.Revision, "openai")); err != nil {
		t.Fatal(err)
	}
	result, err := s.syncRoutingRules(context.Background())
	if err != nil || !result.Stale {
		t.Fatalf("failure should retain previous inline snapshot: %+v %v", result, err)
	}
	after, _, _ := s.readRoutingRuleState()
	if after.Revision != before.Revision || after.Snapshot != before.Snapshot {
		t.Fatal("failed publication switched active pointer")
	}
	if !reflect.DeepEqual(files, routingRepairSnapshotFiles(t, s, first.Revision)) {
		t.Fatal("failed publication leaked an unused normalized candidate")
	}
	if _, err := s.repairRoutingRuleResources(context.Background(), first.Revision); err == nil {
		t.Fatal("repair silently accepted manifest publication failure")
	}
	if !reflect.DeepEqual(files, routingRepairSnapshotFiles(t, s, first.Revision)) {
		t.Fatal("failed explicit repair leaked an unused normalized candidate")
	}
}

func TestRoutingResourceHealthRequiresBothActualOriginalFiles(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	if _, err := s.syncRoutingRules(context.Background()); err != nil {
		t.Fatal(err)
	}
	local := routingResourceTestDetails(t, s, "id=openai&source=local", 200)
	if local["verified"] != true || local["health"] != "verified" || local["can_compare"] != false {
		t.Fatalf("initial verified local state: %+v", local)
	}
	routingResourceTestDetails(t, s, "id=openai&source=upstream", 200)
	local = routingResourceTestDetails(t, s, "id=openai&source=local", 200)
	if local["can_compare"] != true {
		t.Fatal("verified original files cannot compare")
	}
	requests := len(fixture.requests)
	if err := os.WriteFile(s.routingUpstreamPath(fixture.revision, "openai.yaml"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	local = routingResourceTestDetails(t, s, "id=openai&source=local", 200)
	row := routingRepairCatalogItem(t, s, "openai")
	if local["verified"] != true || local["upstream_verified"] != false || local["can_compare"] != false || row["can_compare"] != false || row["upstream_health"] != "corrupt" || len(fixture.requests) != requests {
		t.Fatalf("metadata-only upstream cache was trusted: details=%+v catalog=%+v", local, row)
	}
	if err := os.WriteFile(s.routingRawPath(fixture.revision, "openai"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	local = routingResourceTestDetails(t, s, "id=openai&source=local", 200)
	row = routingRepairCatalogItem(t, s, "openai")
	if local["verified"] != false || local["health"] != "corrupt" || row["verified"] != false || row["health"] != "corrupt" || row["local_url"] == "" {
		t.Fatalf("corrupt local states disagree or hide the resource address: details=%+v catalog=%+v", local, row)
	}
	if err := os.Remove(s.routingRawPath(fixture.revision, "openai")); err != nil {
		t.Fatal(err)
	}
	local = routingResourceTestDetails(t, s, "id=openai&source=local", 200)
	row = routingRepairCatalogItem(t, s, "openai")
	if local["health"] != "missing" || row["health"] != "missing" || local["can_compare"] != false {
		t.Fatal("missing original was not distinguished from corrupt data")
	}
}

func TestRoutingResourceRepairEndpointsAreBoundedAndVersionsAreReadOnly(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	for _, body := range []string{`{"revision":"meta"}`, `{"extra":true}`, `{} {}`, strings.Repeat(" ", 1025)} {
		w := httptest.NewRecorder()
		s.routingRuleRepairHandler(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("invalid repair body accepted: %d %q", w.Code, body)
		}
	}
	w := httptest.NewRecorder()
	s.routingRuleRepairHandler(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")))
	if w.Code != 409 || len(fixture.requests) != 0 {
		t.Fatalf("empty installation repair guessed an upstream revision: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.syncRoutingRules(context.Background()); err != nil {
		t.Fatal(err)
	}
	requests := len(fixture.requests)
	w = httptest.NewRecorder()
	s.routingRuleVersionsHandler(w, httptest.NewRequest(http.MethodGet, "/?page_size=1", nil))
	var result struct {
		Versions []map[string]any `json:"versions"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Versions) != 1 || result.Versions[0]["verified"] != false || result.Versions[0]["health"] != "unchecked" || len(fixture.requests) != requests {
		t.Fatalf("versions overstates historical validation or fetches: %d %s", w.Code, w.Body.String())
	}
	usage := s.routingDiskUsage()
	if usage["disk_bytes"].(int64) < 1 || usage["disk_files"].(int) < 78 || usage["auto_cleanup"] != false {
		t.Fatalf("disk metadata missing: %+v", usage)
	}
}
