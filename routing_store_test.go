package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func routingTestServer(t *testing.T) *server {
	t.Helper()
	s := &server{dataDir: t.TempDir(), domain: "rules.example.com", actionToken: "routing-test-token", adminPassword: "routing-test-password"}
	t.Cleanup(func() {
		if s.routingDB != nil {
			s.routingDB.Close()
		}
	})
	return s
}

func routingStoredFixture() (routingProfileSpec, routingBuildResult) {
	spec := routingProfileSpec{Name: "家庭分流", Sources: []string{"https://source.example/secret-subscription"}, Clients: []string{"mihomo", "stash"}, Match: "proxy", Strategy: "select", IntervalHours: 24}
	build := routingBuildResult{Outputs: map[string]string{"mihomo": "proxies: []\nrules: [MATCH,DIRECT]\n", "stash": "proxies: []\nrules: [MATCH,DIRECT]\n"}, NodeCount: 1, Revision: strings.Repeat("a", 40), Warnings: []string{}}
	return spec, build
}

func TestRoutingProfileStableLinkAndOptimisticUpdates(t *testing.T) {
	s := routingTestServer(t)
	spec, build := routingStoredFixture()
	p, err := s.createRoutingProfile(spec, build)
	if err != nil {
		t.Fatal(err)
	}
	original := p.Links["mihomo"]
	if strings.Contains(original, "source.example") || len(p.Token) != 43 {
		t.Fatal("delivery URL must contain only an opaque token")
	}
	spec.Name = "更新方案"
	updated, err := s.updateRoutingProfile(p.ID, p.Version, spec, build)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Links["mihomo"] != original || updated.Version != 2 || updated.Spec.Name != spec.Name {
		t.Fatal("editing changed the link or failed to update spec")
	}
	if _, err = s.updateRoutingProfile(p.ID, p.Version, spec, build); !errors.Is(err, errRoutingConflict) {
		t.Fatalf("stale update: %v", err)
	}
	db, _ := s.routingDatabase()
	var revisions int
	if err = db.QueryRow("SELECT count(*) FROM profile_revisions WHERE profile_id=?", p.ID).Scan(&revisions); err != nil || revisions != 2 {
		t.Fatalf("revisions=%d err=%v", revisions, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s.routingDB = nil
	reloaded, err := s.getRoutingProfile(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Links["mihomo"] != original || reloaded.Spec.Name != spec.Name {
		t.Fatal("persistent profile lost after reopening database")
	}
}

func TestRoutingFailedPublishPreservesPreviousRevision(t *testing.T) {
	s := routingTestServer(t)
	spec, build := routingStoredFixture()
	p, err := s.createRoutingProfile(spec, build)
	if err != nil {
		t.Fatal(err)
	}
	delete(build.Outputs, "stash")
	spec.Name = "should not be published"
	if _, err = s.updateRoutingProfile(p.ID, 1, spec, build); err == nil {
		t.Fatal("published without all target clients")
	}
	after, err := s.getRoutingProfile(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != 1 || after.Spec.Name == spec.Name {
		t.Fatal("failed publication replaced valid profile")
	}
}

func TestRoutingConcurrentUpdatesDoNotOverwrite(t *testing.T) {
	s := routingTestServer(t)
	spec, build := routingStoredFixture()
	p, err := s.createRoutingProfile(spec, build)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.updateRoutingProfile(p.ID, 1, spec, build); errs <- e }()
	}
	wg.Wait()
	close(errs)
	var successes, conflicts int
	for err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, errRoutingConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", successes, conflicts)
	}
}

func TestRoutingDeliveryRevokeRotateAndLegacyIsolation(t *testing.T) {
	s := routingTestServer(t)
	legacyPath := filepath.Join(s.dataDir, "settings", "subscription-history.json")
	oldRulePath := filepath.Join(s.dataDir, "current", "mihomo", "domain", "Google.mrs")
	for _, p := range []string{legacyPath, oldRulePath} {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("legacy sentinel"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	spec, build := routingStoredFixture()
	p, err := s.createRoutingProfile(spec, build)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.registerRoutingRoutes(mux)
	get := func(raw string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", raw, nil))
		return w
	}
	if w := get(p.Links["mihomo"]); w.Code != 200 || w.Body.String() != build.Outputs["mihomo"] {
		t.Fatalf("delivery %d: %s", w.Code, w.Body.String())
	}
	if w := get(p.Links["stash"]); w.Code != 200 {
		t.Fatalf("stash %d", w.Code)
	}
	if w := get(strings.Replace(p.Links["mihomo"], "/mihomo", "/surge", 1)); w.Code != 404 {
		t.Fatalf("unselected client got %d", w.Code)
	}
	setState := func(disabled bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]bool{"disabled": disabled})
		r := httptest.NewRequest("PATCH", "/api/routing/profiles/"+p.ID, strings.NewReader(string(body)))
		r.SetPathValue("id", p.ID)
		w := httptest.NewRecorder()
		s.routingStateHandler(w, r)
		if w.Code != 200 {
			t.Fatalf("state %d %s", w.Code, w.Body.String())
		}
	}
	setState(true)
	if w := get(p.Links["mihomo"]); w.Code != 410 {
		t.Fatalf("disabled link got %d", w.Code)
	}
	setState(false)
	if w := get(p.Links["mihomo"]); w.Code != 200 {
		t.Fatalf("restored link got %d", w.Code)
	}
	r := httptest.NewRequest("POST", "/api/routing/profiles/"+p.ID+"/rotate", nil)
	r.SetPathValue("id", p.ID)
	w := httptest.NewRecorder()
	s.routingRotateHandler(w, r)
	if w.Code != 200 {
		t.Fatalf("rotate: %d %s", w.Code, w.Body.String())
	}
	if w := get(p.Links["mihomo"]); w.Code != 404 {
		t.Fatalf("revoked token got %d", w.Code)
	}
	newP, err := s.getRoutingProfile(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w := get(newP.Links["mihomo"]); w.Code != 200 {
		t.Fatalf("new token got %d", w.Code)
	}
	if newP.Requests != 3 {
		t.Fatalf("unexpected profile request count %d", newP.Requests)
	}
	for _, path := range []string{legacyPath, oldRulePath} {
		b, e := os.ReadFile(path)
		if e != nil || string(b) != "legacy sentinel" {
			t.Fatal("custom routing modified legacy files")
		}
	}
	if s.usageDB != nil {
		t.Fatal("new delivery touched legacy link statistics")
	}
}

func TestRoutingAdminEndpointsRequireSession(t *testing.T) {
	s := routingTestServer(t)
	mux := http.NewServeMux()
	s.registerRoutingRoutes(mux)
	for _, target := range []string{"/api/routing/catalog", "/api/routing/profiles", "/api/routing/regions", "/api/routing/rules/details?id=openai", "/api/routing/qr?id=x&client=mihomo"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != 401 {
			t.Fatalf("%s=%d", target, w.Code)
		}
	}
	for _, target := range []string{"/api/routing/profiles", "/api/routing/preview", "/api/routing/rules/sync"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", target, strings.NewReader("{}")))
		if w.Code != 401 {
			t.Fatalf("%s=%d", target, w.Code)
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/routing", nil))
	if !strings.Contains(w.Body.String(), "login") {
		t.Fatal("new page did not retain login gate")
	}
}

func TestRoutingDecodeRejectsUnknownOrTrailingPayload(t *testing.T) {
	for _, body := range []string{`{"name":"a","unknown":true}`, `{} {}`, `[]`, strings.Repeat(" ", 129<<10) + `{}`} {
		var spec routingProfileSpec
		w := httptest.NewRecorder()
		if routingDecode(w, httptest.NewRequest("POST", "/api/routing/preview", strings.NewReader(body)), &spec) {
			t.Fatalf("accepted invalid request")
		}
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}
