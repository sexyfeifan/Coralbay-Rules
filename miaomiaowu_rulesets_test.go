package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestMiaomiaowuPayloadKeepsMRSExactSuffixAndIPv6(t *testing.T) {
	for _, test := range []struct {
		behavior string
		entries  []string
		want     []string
	}{
		{"domain", []string{"exact.example.com", "+.suffix.example.com"}, []string{"DOMAIN,exact.example.com", "DOMAIN-SUFFIX,suffix.example.com"}},
		{"ipcidr", []string{"192.0.2.0/24", "2001:db8::/32"}, []string{"IP-CIDR,192.0.2.0/24", "IP-CIDR6,2001:db8::/32"}},
	} {
		data, err := miaomiaowuRulesetPayload(test.behavior, test.entries)
		if err != nil {
			t.Fatal(err)
		}
		var got struct{ Payload []string }
		if err = yaml.Unmarshal(data, &got); err != nil || !reflect.DeepEqual(got.Payload, test.want) {
			t.Fatalf("semantic loss: %s %v", data, err)
		}
	}
	for _, entries := range [][]string{nil, {"*.example.com"}, {"DOMAIN,a.example,DIRECT"}, {"+."}, {".example.com"}} {
		if _, err := miaomiaowuRulesetPayload("domain", entries); err == nil {
			t.Fatalf("unsupported entry silently exported: %v", entries)
		}
	}
}

func TestMiaomiaowuRulesetsTwoSourcesImmutableAndIndependent(t *testing.T) {
	s, root := mihomoProFixture(t)
	before, _ := os.ReadFile(filepath.Join(root, "_templates/MihomoPro.yaml"))
	var decodes, fetches atomic.Int32
	s.mrsDecoder = func(_ context.Context, behavior string, raw []byte) ([]string, error) {
		decodes.Add(1)
		if behavior != "domain" || string(raw) != "binary:mihomo/domain/Google.mrs" {
			return nil, fmt.Errorf("wrong source passed to decoder")
		}
		return []string{"exact.google.example", "+.google.example"}, nil
	}
	resources, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	s.resourceHTTPClient = &http.Client{Transport: ruleSourceTransport(func(r *http.Request) (*http.Response, error) {
		fetches.Add(1)
		if r.URL.String() != "https://raw.githubusercontent.com/666OS/rules/"+resources.Status.Commit+"/mihomo/domain/Google.mrs" {
			t.Error("unfixed upstream address")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("binary:mihomo/domain/Google.mrs"))}, nil
	})}
	var local miaomiaowuRuleset
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ensureMiaomiaowuRuleset(context.Background(), resources.Status.ReleaseID, "local", "domain-Google"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	local, err = s.ensureMiaomiaowuRuleset(context.Background(), resources.Status.ReleaseID, "local", "domain-Google")
	if err != nil || local.Count != 2 || fetches.Load() != 0 || decodes.Load() != 1 {
		t.Fatalf("local isolation/caching failed: %+v %v", local, err)
	}
	upstream, err := s.ensureMiaomiaowuRuleset(context.Background(), resources.Status.ReleaseID, "upstream", "domain-Google")
	if err != nil || upstream.SHA256 != local.SHA256 || fetches.Load() != 1 || upstream.YAMLURL == local.YAMLURL || upstream.InputSHA256 != resources.Files["mihomo/domain/Google.mrs"].SHA256 {
		t.Fatalf("upstream equality failed: %+v %v", upstream, err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "_templates/MihomoPro.yaml"))
	if string(before) != string(after) {
		t.Fatal("old template mutated")
	}
	if err = os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.readMiaomiaowuRuleset(local.Revision, local.Source, local.ID); err != nil {
		t.Fatal("payload lost with old current cleanup: ", err)
	}
	mux := http.NewServeMux()
	s.registerMiaomiaowuRulesetRoutes(mux)
	request := httptest.NewRequest("GET", local.YAMLURL+"?download=1", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("Content-Disposition") != `attachment; filename="yyds-domain-Google.yaml"` || !strings.Contains(response.Body.String(), "payload:") || response.Header().Get("X-CoralBay-Rule-Source") != "local" {
		t.Fatalf("public download: %d %s", response.Code, response.Body.String())
	}
	request.Header.Set("If-None-Match", response.Header().Get("ETag"))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != 304 {
		t.Fatal("ETag not honored")
	}
	if err = os.WriteFile(filepath.Join(s.miaomiaowuRulesetDir(local.Revision, local.Source, local.ID), "content.yaml"), []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != 503 {
		t.Fatal("corrupt immutable payload served or accepted via 304")
	}
}

func TestMiaomiaowuRulesetUnavailableAndAccessBoundaries(t *testing.T) {
	s, _ := mihomoProFixture(t)
	mux := http.NewServeMux()
	s.registerMiaomiaowuRulesetRoutes(mux)
	for _, target := range []string{"/api/templates/miaomiaowu/rulesets", "/api/templates/miaomiaowu/rulesets/domain-Google"} {
		method := "GET"
		if strings.HasSuffix(target, "domain-Google") {
			method = "POST"
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(method, target, strings.NewReader(`{"source":"local"}`)))
		if response.Code != 401 {
			t.Fatal("administrative route is not authenticated")
		}
	}
	for _, id := range []string{"../settings", "domain-Google/../Private", "domain-NoSuchRule", ""} {
		if _, err := s.ensureMiaomiaowuRuleset(context.Background(), "valid", "local", id); err == nil {
			t.Fatal("invalid id accepted")
		}
	}
	resources, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	s.resourceHTTPClient = &http.Client{Transport: ruleSourceTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("wrong upstream"))}, nil
	})}
	s.mrsDecoder = func(context.Context, string, []byte) ([]string, error) {
		t.Error("mismatched upstream reached decoder")
		return nil, nil
	}
	if _, err = s.ensureMiaomiaowuRuleset(context.Background(), resources.Status.ReleaseID, "upstream", "domain-Google"); err == nil {
		t.Fatal("upstream mismatch was silently replaced")
	}
	if _, err = os.Stat(s.miaomiaowuRulesetDir(resources.Status.ReleaseID, "upstream", "domain-Google")); !os.IsNotExist(err) {
		t.Fatal("failed conversion left a published directory")
	}
	response := httptest.NewRecorder()
	s.miaomiaowuRulesetCatalog(response, httptest.NewRequest("GET", "/", nil))
	var body struct {
		Total int `json:"total"`
	}
	if json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Total != 33 {
		t.Fatal("catalog must describe all 33 MRS originals")
	}
}

func TestMiaomiaowuRulesetUpstreamFailuresNeverFallback(t *testing.T) {
	for _, failure := range []string{"http-500", "canceled", "deadline"} {
		t.Run(failure, func(t *testing.T) {
			s, _ := mihomoProFixture(t)
			resources, err := s.retainLegacyResources()
			if err != nil {
				t.Fatal(err)
			}
			var decodes atomic.Int32
			s.mrsDecoder = func(context.Context, string, []byte) ([]string, error) {
				decodes.Add(1)
				return []string{"exact.example.com", "+.suffix.example.com"}, nil
			}
			local, err := s.ensureMiaomiaowuRuleset(context.Background(), resources.Status.ReleaseID, "local", "domain-Google")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "deadline" {
				var cancelDeadline context.CancelFunc
				ctx, cancelDeadline = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancelDeadline()
			}
			var fetches atomic.Int32
			s.resourceHTTPClient = &http.Client{Transport: ruleSourceTransport(func(r *http.Request) (*http.Response, error) {
				fetches.Add(1)
				switch failure {
				case "http-500":
					return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
				case "canceled":
					cancel()
					return nil, r.Context().Err()
				default:
					return nil, context.DeadlineExceeded
				}
			})}
			if _, err = s.ensureMiaomiaowuRuleset(ctx, resources.Status.ReleaseID, "upstream", "domain-Google"); err == nil {
				t.Fatal("failed upstream request silently reused the local payload")
			}
			if failure != "deadline" && fetches.Load() != 1 {
				t.Fatalf("upstream source was not fetched: %d requests", fetches.Load())
			}
			if decodes.Load() != 1 {
				t.Fatal("failed upstream request reached the decoder")
			}
			if _, err = os.Stat(s.miaomiaowuRulesetDir(resources.Status.ReleaseID, "upstream", "domain-Google")); !os.IsNotExist(err) {
				t.Fatal("failed upstream request published an artifact")
			}
			if err = filepath.WalkDir(filepath.Join(s.dataDir, "miaomiaowu"), func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if strings.HasPrefix(entry.Name(), ".candidate-") {
					t.Errorf("failed request left temporary publication: %s", path)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			s.registerMiaomiaowuRulesetRoutes(mux)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest("GET", local.YAMLURL, nil))
			if response.Code != 200 || routingSHA256(response.Body.Bytes()) != local.SHA256 {
				t.Fatal("upstream failure damaged the previously published local artifact")
			}
		})
	}
}

func TestMiaomiaowuRulesetCorruptMRSCannotReusePublication(t *testing.T) {
	for _, state := range []string{"before-publication", "after-publication"} {
		t.Run(state, func(t *testing.T) {
			s, _ := mihomoProFixture(t)
			resources, err := s.retainLegacyResources()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.legacyResourceDir(resources.Status.ReleaseID), "mihomo/domain/Google.mrs")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var decodes, fetches atomic.Int32
			s.mrsDecoder = func(context.Context, string, []byte) ([]string, error) {
				decodes.Add(1)
				return []string{"exact.example.com", "+.suffix.example.com"}, nil
			}
			s.resourceHTTPClient = &http.Client{Transport: ruleSourceTransport(func(*http.Request) (*http.Response, error) {
				fetches.Add(1)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}
			published := []miaomiaowuRuleset{}
			if state == "after-publication" {
				for _, source := range []string{"local", "upstream"} {
					meta, err := s.ensureMiaomiaowuRuleset(context.Background(), resources.Status.ReleaseID, source, "domain-Google")
					if err != nil {
						t.Fatal(err)
					}
					published = append(published, meta)
				}
			}
			decodesBefore, fetchesBefore := decodes.Load(), fetches.Load()
			corrupt := append([]byte(nil), raw...)
			corrupt[0] ^= 1 // Keep the size unchanged so the SHA check is necessary.
			if err = os.WriteFile(path, corrupt, 0644); err != nil {
				t.Fatal(err)
			}
			for _, source := range []string{"local", "upstream"} {
				if _, err = s.ensureMiaomiaowuRuleset(context.Background(), resources.Status.ReleaseID, source, "domain-Google"); err == nil {
					t.Fatalf("%s accepted corrupt original by reusing a cache or alternate source", source)
				}
				if state == "before-publication" {
					if _, err = os.Stat(s.miaomiaowuRulesetDir(resources.Status.ReleaseID, source, "domain-Google")); !os.IsNotExist(err) {
						t.Fatal("corrupt original produced a public artifact")
					}
				}
			}
			if decodes.Load() != decodesBefore || fetches.Load() != fetchesBefore {
				t.Fatal("corrupt local baseline reached decoding or upstream fetching")
			}
			mux := http.NewServeMux()
			s.registerMiaomiaowuRulesetRoutes(mux)
			for _, meta := range published {
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, httptest.NewRequest("GET", meta.YAMLURL, nil))
				if response.Code != 200 || routingSHA256(response.Body.Bytes()) != meta.SHA256 || response.Header().Get("ETag") != `"`+meta.SHA256+`"` {
					t.Fatalf("published %s artifact stopped being independently readable", meta.Source)
				}
			}
		})
	}
}
