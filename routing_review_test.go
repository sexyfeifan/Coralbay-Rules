package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRoutingMetadataDoesNotLoadCorruptLargeOutputs(t *testing.T) {
	s, spec, _, _ := routingIntegrationFixture(t)
	build, err := s.runRoutingBuild(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.createRoutingProfile(spec, build)
	if err != nil {
		t.Fatal(err)
	}
	db, err := s.routingDatabase()
	if err != nil {
		t.Fatal(err)
	}
	// Metadata remains usable even if a large cached configuration is corrupt.
	// This must not make that configuration acceptable for subscription delivery.
	corrupt := "not-valid-output-json:" + strings.Repeat("sensitive-cache-marker", 400000)
	if _, err := db.Exec("UPDATE profiles SET outputs=? WHERE id=?", corrupt, profile.ID); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.routingProfilesHandler(w, httptest.NewRequest(http.MethodGet, "/api/routing/profiles", nil))
	var response struct {
		Profiles []routingProfile `json:"profiles"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Profiles) != 1 {
		t.Fatalf("metadata listing depends on cached outputs: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sensitive-cache-marker") || len(w.Body.Bytes()) > 16384 {
		t.Fatal("metadata listing included cached configuration content")
	}
	for name, lookup := range map[string]func() (routingProfile, error){
		"admin detail":   func() (routingProfile, error) { return s.getRoutingProfile(profile.ID) },
		"token metadata": func() (routingProfile, error) { return s.routingTokenLookup(profile.Token, false) },
	} {
		p, err := lookup()
		if err != nil || p.ID != profile.ID || len(p.Outputs) != 0 {
			t.Fatalf("%s loaded broken configuration: %+v %v", name, p, err)
		}
	}
	if _, err := s.getRoutingProfileByToken(profile.Token); err == nil {
		t.Fatal("full configuration lookup accepted corrupt outputs")
	}
	// Exercise the actual delivery path: metadata success must not become a 200
	// response containing corrupt or invented configuration data.
	mux := http.NewServeMux()
	s.registerRoutingRoutes(mux)
	delivery := httptest.NewRecorder()
	mux.ServeHTTP(delivery, httptest.NewRequest(http.MethodGet, profile.Links["mihomo"], nil))
	if delivery.Code >= 200 && delivery.Code < 300 || strings.Contains(delivery.Body.String(), "sensitive-cache-marker") {
		t.Fatalf("corrupt cache was delivered: %d %s", delivery.Code, delivery.Body.String())
	}
}

func TestRoutingCancelledDeliveryDoesNotWaitForProfileRefresh(t *testing.T) {
	s, spec, _, _ := routingIntegrationFixture(t)
	build, err := s.runRoutingBuild(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.createRoutingProfile(spec, build)
	if err != nil {
		t.Fatal(err)
	}
	lock := s.routingProfileLock(profile.ID)
	<-lock // Another request is already refreshing this profile.
	defer func() { lock <- struct{}{} }()
	var networkCalls atomic.Int32
	previous := s.routingHTTPClient.Transport
	s.routingHTTPClient.Transport = routingIntegrationTransport(func(r *http.Request) (*http.Response, error) {
		networkCalls.Add(1)
		return previous.RoundTrip(r)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, profile.Links["mihomo"], nil).WithContext(ctx)
	mux := http.NewServeMux()
	s.registerRoutingRoutes(mux)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); mux.ServeHTTP(w, r) }()
	select {
	case <-done:
		t.Fatal("delivery skipped the held profile refresh lock")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled delivery remains blocked behind a different refresh")
	}
	if w.Body.Len() != 0 || networkCalls.Load() != 0 {
		t.Fatal("cancelled queued request fetched or delivered a configuration")
	}
	select {
	case <-lock:
		t.Fatal("cancelled waiter released a lock it did not own")
	default:
	}
}

func TestRoutingRefreshFailureCooldownPreservesPreviousOutput(t *testing.T) {
	s, spec, _, _ := routingIntegrationFixture(t)
	build, err := s.runRoutingBuild(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.createRoutingProfile(spec, build)
	if err != nil {
		t.Fatal(err)
	}
	db, err := s.routingDatabase()
	if err != nil {
		t.Fatal(err)
	}
	oldBuiltAt := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec("UPDATE profiles SET last_built_at=? WHERE id=?", oldBuiltAt, profile.ID); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	previous := s.routingHTTPClient.Transport
	s.routingHTTPClient.Transport = routingIntegrationTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "source.example" {
			calls.Add(1)
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("upstream temporarily unavailable")), Request: r}, nil
		}
		return previous.RoundTrip(r)
	})
	mux := http.NewServeMux()
	s.registerRoutingRoutes(mux)
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, profile.Links["mihomo"], nil))
		return w
	}
	first, second := get(), get()
	if first.Code != 503 || second.Code != 503 || second.Header().Get("Retry-After") != "30" || calls.Load() != 1 {
		t.Fatalf("upstream failure was not coalesced: first=%d second=%d retry=%q calls=%d", first.Code, second.Code, second.Header().Get("Retry-After"), calls.Load())
	}
	for _, response := range []*httptest.ResponseRecorder{first, second} {
		if strings.Contains(response.Body.String(), "demo-only-password") {
			t.Fatal("failed refresh silently served old node credentials")
		}
	}
	stored, err := s.getRoutingProfileByToken(profile.Token)
	if err != nil || stored.Outputs["mihomo"] != build.Outputs["mihomo"] || stored.LastBuiltAt != oldBuiltAt || stored.LastError == "" || stored.LastAttemptAt == "" || stored.Requests != 0 {
		t.Fatalf("failure changed cached output or omitted diagnostics: last_built=%s last_error=%q last_attempt=%s requests=%d err=%v", stored.LastBuiltAt, stored.LastError, stored.LastAttemptAt, stored.Requests, err)
	}
	// Expire the cooldown without waiting in real time, then verify retry resumes.
	if _, err := db.Exec("UPDATE profiles SET last_attempt_at=? WHERE id=?", time.Now().Add(-31*time.Second).UTC().Format(time.RFC3339Nano), profile.ID); err != nil {
		t.Fatal(err)
	}
	third := get()
	if third.Code != 503 || calls.Load() != 2 {
		t.Fatalf("expired failure cooldown did not retry: status=%d calls=%d", third.Code, calls.Load())
	}
}
