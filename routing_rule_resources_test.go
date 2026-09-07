package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func routingResourceTestServer(t *testing.T) (*server, *routingRulesFixture) {
	t.Helper()
	fixture := &routingRulesFixture{revision: strings.Repeat("a", 40)}
	return &server{dataDir: t.TempDir(), domain: "rules.example.test", routingHTTPClient: fixture.client()}, fixture
}

func routingResourceTestGet(s *server, revision, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/_rule-resources/metacubex/"+revision+"/"+id+".yaml", nil)
	req.SetPathValue("revision", revision)
	req.SetPathValue("file", id+".yaml")
	w := httptest.NewRecorder()
	s.routingRuleResourceHandler(w, req)
	return w
}

func routingResourceTestDetails(t *testing.T, s *server, query string, status int) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	s.routingRuleDetailsHandler(w, httptest.NewRequest(http.MethodGet, "/api/routing/rules/details?"+query, nil))
	if w.Code != status {
		t.Fatalf("details status=%d want=%d: %s", w.Code, status, w.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRoutingResourceFullMirrorExactBytesAndHistoricalPublication(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	first, err := s.syncRoutingRules(context.Background())
	if err != nil || first.Stale || len(fixture.requests) != 79 {
		t.Fatalf("full mirror: %+v, %v, requests=%d", first, err, len(fixture.requests))
	}
	manifest, err := s.readRoutingResourceManifest(first.Revision)
	if err != nil || len(manifest.Resources) != 78 {
		t.Fatalf("incomplete manifest: %d %v", len(manifest.Resources), err)
	}
	for id, metadata := range manifest.Resources {
		response := routingResourceTestGet(s, first.Revision, id)
		want := "payload:\n  - DOMAIN-SUFFIX,aaaaaaaa.example.com\n  - DOMAIN-KEYWORD,example\n"
		if metadata.Behavior == "ipcidr" {
			want = "payload:\n  - 10.0.0.0/8\n  - fc00::/7\n"
		}
		if response.Code != 200 || routingSHA256(response.Body.Bytes()) != metadata.SHA256 || int64(response.Body.Len()) != metadata.Bytes {
			t.Fatalf("resource %s changed bytes: %d %s", id, response.Code, response.Body.String())
		}
		if response.Body.String() != want {
			t.Fatalf("resource %s was rewritten from original upstream YAML", id)
		}
		if response.Header().Get("ETag") != `"`+metadata.SHA256+`"` || !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
			t.Fatalf("resource %s lacks immutable identity", id)
		}
	}
	oldRaw := append([]byte(nil), routingResourceTestGet(s, first.Revision, "openai").Body.Bytes()...)
	state, _, _ := s.readRoutingRuleState()
	state.CheckedAt = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	if err := s.saveRoutingRuleState(state); err != nil {
		t.Fatal(err)
	}
	requests := len(fixture.requests)
	local, err := s.loadPublishedRoutingRuleSnapshot(context.Background(), []string{"openai", "private-ip"})
	if err != nil || !local.Stale || local.LastError == "" || len(local.Resources) != 2 || len(fixture.requests) != requests {
		t.Fatalf("strict local should read expired published data without fetch: %+v %v", local, err)
	}
	resource := local.Resources["openai"]
	if !bytes.Equal(resource.Content, oldRaw) || resource.LocalURL != "https://rules.example.test/_rule-resources/metacubex/"+first.Revision+"/openai.yaml" || resource.SourceURL != routingPinnedURL(routingRuleIndex()["openai"], first.Revision) {
		t.Fatalf("resource identity mismatch: %+v", resource)
	}
	fixture.revision = strings.Repeat("b", 40)
	second, err := s.syncRoutingRules(context.Background())
	if err != nil || second.Revision == first.Revision {
		t.Fatalf("new revision: %+v %v", second, err)
	}
	if response := routingResourceTestGet(s, first.Revision, "openai"); response.Code != 200 || !bytes.Equal(response.Body.Bytes(), oldRaw) {
		t.Fatal("new publication modified historical resource")
	}
	catalog := s.routingCatalogResponse()
	if catalog["mirrored_count"] != 78 || catalog["complete"] != true || catalog["disk_bytes"].(int64) <= catalog["raw_bytes"].(int64) {
		t.Fatalf("catalog mirror metadata: %+v", catalog)
	}
}

func TestRoutingResourceUpstreamFailureKeepsInspectionCacheAndLocalState(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	if _, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai"}, false); err != nil {
		t.Fatal(err)
	}
	before, _, _ := s.readRoutingRuleState()
	first := routingResourceTestDetails(t, s, "id=openai&source=upstream&revision="+fixture.revision, 200)
	status := s.readRoutingUpstreamResourceStatus(fixture.revision, "openai")
	status.CheckedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	content, _ := json.Marshal(status)
	if err := routingRulesAtomicWrite(s.routingUpstreamPath(fixture.revision, "openai.json"), content); err != nil {
		t.Fatal(err)
	}
	fixture.failedFile = "openai"
	stale := routingResourceTestDetails(t, s, "id=openai&source=upstream&revision="+fixture.revision, 200)
	if stale["stale"] != true || stale["last_error"] == "" || stale["sha256"] != first["sha256"] || stale["count"] != first["count"] {
		t.Fatalf("upstream failure discarded last checked good data: %+v", stale)
	}
	requests := len(fixture.requests)
	routingResourceTestDetails(t, s, "id=openai&source=upstream&revision="+fixture.revision, 200)
	if len(fixture.requests) != requests {
		t.Fatal("upstream failure retry cooldown not honored")
	}
	after, _, _ := s.readRoutingRuleState()
	if after != before {
		t.Fatal("upstream inspection failure polluted published local snapshot status")
	}
	local := routingResourceTestDetails(t, s, "id=openai&source=local", 200)
	if local["stale"] != false || local["mirrored"] != true {
		t.Fatalf("local details inherited unrelated upstream failure: %+v", local)
	}
}

func TestRoutingResourceMissingCorruptAndUnpublishedFailClosed(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	routingResourceTestDetails(t, s, "id=openai&source=local", 404)
	if len(fixture.requests) != 0 {
		t.Fatal("empty local details fetched upstream")
	}
	if _, err := s.loadPublishedRoutingRuleSnapshot(context.Background(), []string{"openai"}); err == nil {
		t.Fatal("strict local silently accepted an empty installation")
	}
	if err := s.writeRoutingRawDocument(fixture.revision, "openai", []byte("payload: ['DOMAIN,orphan.example']")); err != nil {
		t.Fatal(err)
	}
	if response := routingResourceTestGet(s, fixture.revision, "openai"); response.Code != 404 {
		t.Fatal("unpublished raw candidate is publicly addressable")
	}
	if _, err := s.syncRoutingRules(context.Background()); err != nil {
		t.Fatal(err)
	}
	requests := len(fixture.requests)
	if err := os.WriteFile(s.routingRawPath(fixture.revision, "openai"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.loadPublishedRoutingRuleSnapshot(context.Background(), []string{"openai"}); err == nil {
		t.Fatal("strict local accepted corrupt resource")
	}
	if response := routingResourceTestGet(s, fixture.revision, "openai"); response.Code != 503 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("corrupt resource did not fail closed")
	}
	details := routingResourceTestDetails(t, s, "id=openai&source=local", 200)
	if details["mirrored"] != false || details["local_error"] == "" || details["hash_comparable"] != false || len(fixture.requests) != requests {
		t.Fatalf("corrupt local detail concealed missing raw or fetched: %+v", details)
	}
	if _, err := s.syncRoutingRules(context.Background()); err != nil {
		t.Fatal(err)
	}
	if response := routingResourceTestGet(s, fixture.revision, "openai"); response.Code != 200 {
		t.Fatal("explicit synchronization failed to repair verified raw")
	}
}

func TestRoutingResourceLegacyJSONNeedsExplicitMirror(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	if _, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai"}, false); err != nil {
		t.Fatal(err)
	}
	state, snapshot, err := s.readRoutingRuleState()
	if err != nil {
		t.Fatal(err)
	}
	doc := snapshot.Rules["openai"]
	doc.RawBytes, doc.DownloadedAt = 0, ""
	snapshot.Rules["openai"] = doc
	content, _ := json.Marshal(snapshot)
	state.Snapshot = routingSHA256(content)
	releaseDir := filepath.Join(s.routingRulesDir(), "releases", state.Revision)
	if err := routingRulesAtomicWrite(filepath.Join(releaseDir, state.Snapshot+".json"), content); err != nil {
		t.Fatal(err)
	}
	if err := s.saveRoutingRuleState(state); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(releaseDir, "resources.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.routingRawPath(state.Revision, "openai")); err != nil {
		t.Fatal(err)
	}
	requests := len(fixture.requests)
	legacy, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai"}, false)
	if err != nil || len(legacy.Rules["openai"]) != 2 || len(fixture.requests) != requests {
		t.Fatalf("old inline snapshot lost compatibility: %+v %v", legacy, err)
	}
	if _, err := s.loadPublishedRoutingRuleSnapshot(context.Background(), []string{"openai"}); err == nil {
		t.Fatal("old normalized data was misrepresented as raw mirror")
	}
	details := routingResourceTestDetails(t, s, "id=openai", 200)
	if details["mirrored"] != false || details["local_error"] == "" || details["bytes"] != float64(0) || len(fixture.requests) != requests {
		t.Fatalf("legacy details must explain missing raw: %+v", details)
	}
	if _, err := s.syncRoutingRules(context.Background()); err != nil {
		t.Fatal(err)
	}
	if local, err := s.loadPublishedRoutingRuleSnapshot(context.Background(), []string{"openai"}); err != nil || len(local.Resources["openai"].Content) == 0 {
		t.Fatalf("explicit mirror did not upgrade legacy snapshot: %+v %v", local, err)
	}
	if s.routingCatalogResponse()["mirrored_count"] != 78 {
		t.Fatal("manual mirror did not fill entire independent catalog")
	}
}

func TestRoutingResourceUpstreamInspectionDoesNotPublish(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	details := routingResourceTestDetails(t, s, "id=openai&source=upstream", 200)
	if details["count"] != float64(2) || details["source"] != "upstream" || details["hash_comparable"] != false || len(fixture.requests) != 2 {
		t.Fatalf("upstream inspection: %+v requests=%d", details, len(fixture.requests))
	}
	state, snapshot, err := s.readRoutingRuleState()
	if err != nil || state.Revision != "" || snapshot.Revision != "" || s.routingCatalogResponse()["mirrored_count"] != 0 {
		t.Fatalf("inspection published active state: %+v %+v %v", state, snapshot, err)
	}
	routingResourceTestDetails(t, s, "id=openai&source=local", 404)
	if response := routingResourceTestGet(s, fixture.revision, "openai"); response.Code != 404 {
		t.Fatal("upstream inspection became publicly served local resource")
	}
	routingResourceTestDetails(t, s, "id=openai&source=upstream&page_size=1&page=2&q=EXAMPLE", 200)
	if len(fixture.requests) != 2 {
		t.Fatal("upstream inspection ignored independent 5 minute cache")
	}
	if _, err := s.syncRoutingRules(context.Background()); err != nil {
		t.Fatal(err)
	}
	details = routingResourceTestDetails(t, s, "id=openai&source=local&q=keyword&page_size=1", 200)
	if details["count"] != float64(1) || details["total_count"] != float64(2) || details["hash_equal"] != true || details["hash_comparable"] != true {
		t.Fatalf("matching comparison/search metadata: %+v", details)
	}
	before, _, _ := s.readRoutingRuleState()
	fixture.revision = strings.Repeat("b", 40)
	w := httptest.NewRecorder()
	s.routingRuleCheckHandler(w, httptest.NewRequest(http.MethodPost, "/api/routing/rules/check", nil))
	if w.Code != 200 || s.routingCatalogResponse()["update_available"] != true {
		t.Fatalf("branch check: %d %s", w.Code, w.Body.String())
	}
	after, _, _ := s.readRoutingRuleState()
	if after != before {
		t.Fatal("branch check changed the locally published snapshot")
	}
	details = routingResourceTestDetails(t, s, "id=openai&source=upstream", 200)
	if details["hash_comparable"] != true || details["hash_equal"] != false || details["local_revision"] != before.Revision {
		t.Fatalf("different commit comparison: %+v", details)
	}
	fixture.failedBranch = true
	w = httptest.NewRecorder()
	s.routingRuleCheckHandler(w, httptest.NewRequest(http.MethodPost, "/api/routing/rules/check", nil))
	if w.Code != 502 {
		t.Fatal("upstream check failure was concealed")
	}
	after, _, _ = s.readRoutingRuleState()
	if after != before || s.routingCatalogResponse()["upstream_error"] == "" {
		t.Fatal("upstream check failure changed local state or lost error")
	}
}

func TestRoutingResourceLocalReadsDoNotWaitForSlowSync(t *testing.T) {
	s, _ := routingResourceTestServer(t)
	if _, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai"}, false); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	s.routingHTTPClient = &http.Client{Transport: routingRuleTestTransport(func(req *http.Request) (*http.Response, error) {
		close(entered)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.syncRoutingRules(ctx)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("sync did not reach network request")
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := s.loadPublishedRoutingRuleSnapshot(context.Background(), []string{"openai"})
		if err == nil {
			w := httptest.NewRecorder()
			s.routingRuleDetailsHandler(w, httptest.NewRequest(http.MethodGet, "/api/routing/rules/details?id=openai", nil))
			if w.Code != 200 {
				err = fmt.Errorf("local details status=%d", w.Code)
			}
		}
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("local read waited for unrelated slow synchronization")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled sync did not stop")
	}
}

func TestRoutingResourceFailedCandidateAndRollbackCannotMutatePublishedFiles(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	first, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai", "netflix"}, false)
	if err != nil {
		t.Fatal(err)
	}
	oldRaw := append([]byte(nil), routingResourceTestGet(s, first.Revision, "openai").Body.Bytes()...)
	fixture.revision = strings.Repeat("b", 40)
	fixture.failedFile = "netflix"
	failed, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai", "netflix"}, true)
	if err != nil || !failed.Stale || failed.Revision != first.Revision {
		t.Fatalf("failed candidate replaced active version: %+v %v", failed, err)
	}
	if response := routingResourceTestGet(s, fixture.revision, "openai"); response.Code != 404 {
		t.Fatal("partial candidate was published")
	}
	fixture.failedFile = ""
	second, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai", "netflix"}, true)
	if err != nil || second.Stale || second.Revision == first.Revision {
		t.Fatalf("second publication: %+v %v", second, err)
	}
	// Simulate an upstream serving changed bytes for a previously pinned commit.
	baseClient := fixture.client()
	s.routingHTTPClient = &http.Client{Transport: routingRuleTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == routingBranchURL {
			return routingRuleTestResponse(req, 200, fmt.Sprintf(`{"ref":"refs/heads/meta","object":{"type":"commit","sha":%q}}`, first.Revision)), nil
		}
		return baseClient.Transport.RoundTrip(req)
	})}
	rolled, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai", "netflix"}, true)
	if err != nil || !rolled.Stale || rolled.Revision != second.Revision || rolled.LastError == "" {
		t.Fatalf("historical hash change did not preserve active version: %+v %v", rolled, err)
	}
	if response := routingResourceTestGet(s, first.Revision, "openai"); response.Code != 200 || !bytes.Equal(response.Body.Bytes(), oldRaw) {
		t.Fatal("failed rollback changed previously published fixed URL bytes")
	}
}

func TestRoutingResourceDetailsValidationAndFreshScheduler(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	for _, query := range []string{"id=../openai", "id=openai&source=other", "id=openai&revision=meta", "id=openai&page=0", "id=openai&page_size=2001", "id=openai&q=" + strings.Repeat("a", 513)} {
		routingResourceTestDetails(t, s, query, 400)
	}
	if len(fixture.requests) != 0 {
		t.Fatal("invalid detail query caused network request")
	}
	if _, err := s.syncRoutingRules(context.Background()); err != nil {
		t.Fatal(err)
	}
	requests := len(fixture.requests)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	s.routingRulesScheduler(ctx)
	if len(fixture.requests) != requests {
		t.Fatal("fresh full mirror was unnecessarily synchronized on scheduler startup")
	}
}

func TestRoutingResourceCandidateRawBudgetKeepsLastGood(t *testing.T) {
	s, fixture := routingResourceTestServer(t)
	first, err := s.loadRoutingRuleSnapshot(context.Background(), []string{"openai"}, false)
	if err != nil {
		t.Fatal(err)
	}
	oldRaw := append([]byte(nil), routingResourceTestGet(s, first.Revision, "openai").Body.Bytes()...)
	fixture.revision = strings.Repeat("b", 40)
	base := fixture.client()
	// A valid tiny payload can have a large original file due to comments; the
	// normalized JSON limit alone cannot bound the bytes retained by a mirror.
	comment := "#" + strings.Repeat("x", 8<<20) + "\n"
	var fetched atomic.Int32
	s.routingHTTPClient = &http.Client{Transport: routingRuleTestTransport(func(req *http.Request) (*http.Response, error) {
		response, err := base.Transport.RoundTrip(req)
		if err == nil && req.URL.String() != routingBranchURL {
			fetched.Add(1)
			payload := "payload: ['DOMAIN,example.com']\n"
			if strings.Contains(req.URL.Path, "/geoip/") {
				payload = "payload: ['10.0.0.0/8']\n"
			}
			response.Body.Close()
			return routingRuleTestResponse(req, 200, comment+payload), nil
		}
		return response, err
	})}
	if _, err := s.syncRoutingRules(context.Background()); err == nil {
		t.Fatal("oversized full mirror was accepted")
	}
	state, _, err := s.readRoutingRuleState()
	if err != nil || state.Revision != first.Revision || !strings.Contains(state.LastError, "64 MiB") {
		t.Fatalf("raw budget failure changed last good or lost error: %+v %v", state, err)
	}
	if fetched.Load() >= 78 {
		t.Fatal("budget only checked after downloading entire oversized catalog")
	}
	if response := routingResourceTestGet(s, first.Revision, "openai"); response.Code != 200 || !bytes.Equal(response.Body.Bytes(), oldRaw) {
		t.Fatal("raw budget failure modified previously published resource")
	}
	if response := routingResourceTestGet(s, fixture.revision, "openai"); response.Code != 404 {
		t.Fatal("raw budget failure published partial mirror")
	}
}
