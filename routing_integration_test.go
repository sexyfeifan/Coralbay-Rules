package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type routingIntegrationTransport func(*http.Request) (*http.Response, error)

func (f routingIntegrationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func routingIntegrationFixture(t *testing.T) (*server, routingProfileSpec, func(string), func(chan struct{}, chan struct{})) {
	t.Helper()
	s := routingTestServer(t)
	var mu sync.Mutex
	name := "US initial"
	var entered, release chan struct{}
	s.routingHTTPClient = &http.Client{Transport: routingIntegrationTransport(func(r *http.Request) (*http.Response, error) {
		var body string
		switch {
		case r.URL.Host == "source.example":
			mu.Lock()
			n, e, g := name, entered, release
			mu.Unlock()
			if e != nil {
				select {
				case e <- struct{}{}:
				default:
				}
			}
			if g != nil {
				select {
				case <-g:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
			}
			body = fmt.Sprintf("proxies:\n - name: %s\n   type: trojan\n   server: 203.0.113.10\n   port: 443\n   password: demo-only-password\n", n)
		case r.URL.String() == routingBranchURL:
			body = `{"ref":"refs/heads/meta","object":{"sha":"` + routingBootstrapRevision + `","type":"commit"}}`
		case r.URL.Host == "raw.githubusercontent.com":
			body = "payload:\n - DOMAIN-SUFFIX,openai.com\n"
		default:
			return nil, fmt.Errorf("unexpected network destination")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Subscription-Userinfo": []string{"upload=0; download=1; total=100"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	spec := routingProfileSpec{Name: "集成测试", Sources: []string{"https://source.example/sub"}, Clients: []string{"mihomo"}, Rules: []routingRuleChoice{{ID: "openai", Action: "proxy"}}, Match: "proxy", Strategy: "select", IntervalHours: 6}
	return s, spec, func(v string) { mu.Lock(); name = v; mu.Unlock() }, func(e, g chan struct{}) { mu.Lock(); entered, release = e, g; mu.Unlock() }
}

func TestRoutingAPIRegeneratesNodesAtTheSameURL(t *testing.T) {
	s, spec, setNodes, _ := routingIntegrationFixture(t)
	b, _ := json.Marshal(spec)
	created := httptest.NewRecorder()
	s.routingCreateHandler(created, httptest.NewRequest("POST", "/api/routing/profiles", strings.NewReader(string(b))))
	if created.Code != 201 {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	var profile routingProfile
	if err := json.Unmarshal(created.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.Links) != 1 {
		t.Fatal("missing published URL")
	}
	if strings.Contains(created.Body.String(), "demo-only-password") {
		t.Fatal("profile metadata exposed generated node credentials")
	}
	setNodes("JP refreshed")
	db, _ := s.routingDatabase()
	if _, err := db.Exec("UPDATE profiles SET last_built_at=? WHERE id=?", time.Now().Add(-time.Hour).Format(time.RFC3339Nano), profile.ID); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.registerRoutingRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", profile.Links["mihomo"], nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "JP refreshed") || strings.Contains(w.Body.String(), "US initial") {
		t.Fatalf("refresh %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "DOMAIN-SUFFIX,openai.com,CB · openai") {
		t.Fatal("new rules missing")
	}
	for _, legacy := range []string{"666OS", "Pro_cn", "x-nextin", "rule-providers"} {
		if strings.Contains(w.Body.String(), legacy) {
			t.Fatalf("unexpected legacy dependency %s", legacy)
		}
	}
	if w.Header().Get("Subscription-Userinfo") == "" {
		t.Fatal("upstream usage header lost")
	}
	after, err := s.getRoutingProfile(profile.ID)
	if err != nil || after.Links["mihomo"] != profile.Links["mihomo"] {
		t.Fatal("URL changed during node refresh")
	}
}

func TestRoutingTokenRotationDuringRefreshPreventsDelivery(t *testing.T) {
	s, spec, _, gate := routingIntegrationFixture(t)
	build, err := s.runRoutingBuild(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.createRoutingProfile(spec, build)
	if err != nil {
		t.Fatal(err)
	}
	db, _ := s.routingDatabase()
	if _, err = db.Exec("UPDATE profiles SET last_built_at=? WHERE id=?", time.Now().Add(-time.Hour).Format(time.RFC3339Nano), p.ID); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}, 1), make(chan struct{})
	gate(entered, release)
	mux := http.NewServeMux()
	s.registerRoutingRoutes(mux)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", p.Links["mihomo"], nil))
		done <- w
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("refresh did not start")
	}
	r := httptest.NewRequest("POST", "/api/routing/profiles/"+p.ID+"/rotate", nil)
	r.SetPathValue("id", p.ID)
	w := httptest.NewRecorder()
	s.routingRotateHandler(w, r)
	close(release)
	if w.Code != 200 {
		t.Fatalf("rotation %d", w.Code)
	}
	select {
	case result := <-done:
		if result.Code != 409 || strings.Contains(result.Body.String(), "demo-only-password") {
			t.Fatalf("revoked in-flight request delivered %d %s", result.Code, result.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refresh failed to complete")
	}
}
