package main

import (
	"context"
	"encoding/json"
	"errors"
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

func miaomiaowuClientFixture(t *testing.T) (*server, string, legacyResourceManifest, *atomic.Int32) {
	t.Helper()
	s, root := mihomoProFixture(t)
	var decodes atomic.Int32
	s.mrsDecoder = func(_ context.Context, behavior string, raw []byte) ([]string, error) {
		decodes.Add(1)
		path := strings.TrimPrefix(string(raw), "binary:")
		if !legacyKnownPath(path) || behavior != miaomiaowuRulesetBehavior(path) {
			return nil, fmt.Errorf("decoder received a non-MRS source")
		}
		if behavior == "ipcidr" {
			return []string{"192.0.2.0/24", "2001:db8::/32"}, nil
		}
		return []string{"exact.example.com", "+.suffix.example.com"}, nil
	}
	resources, err := s.retainLegacyResources()
	if err != nil {
		t.Fatal(err)
	}
	return s, root, resources, &decodes
}

func miaomiaowuINISections(text string) map[string][]string {
	sections := map[string][]string{}
	section := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line
			continue
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			sections[section] = append(sections[section], line)
		}
	}
	return sections
}

func TestMiaomiaowuClientConfigNativeGroupsRulesAndNoResolve(t *testing.T) {
	s, root, resources, _ := miaomiaowuClientFixture(t)
	input, _ := os.ReadFile(filepath.Join(root, "_templates/MihomoPro.yaml"))
	var original struct {
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(input, &original); err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"surge", "loon"} {
		for _, source := range []string{"local", "upstream"} {
			data, flags, err := s.miaomiaowuClientConfig(input, resources, client, source)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, forbidden := range []string{"__PROXY_", "include-all-providers", "payload:", "proxy-providers:", "external-controller", "mixed-port", "raw.githubusercontent.com", "geosite:", "GEOIP,"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("%s leaked incompatible setting %q", client, forbidden)
				}
			}
			sections := miaomiaowuINISections(text)
			if len(sections["[Proxy]"]) != 0 || !strings.Contains(text, "[Proxy]\n") || len(sections["[Proxy Group]"]) != 20 {
				t.Fatalf("%s invalid injection section or group count: %s", client, text)
			}
			for _, name := range miaomiaowuBusinessNames {
				if !strings.Contains(text, name+" = select, ") {
					t.Fatalf("lost business group %s", name)
				}
			}
			for _, line := range []string{"广告拦截 = select, REJECT-DROP, REJECT, DIRECT", "国内流量 = select, DIRECT, 故障转移, 全球手动, 全球自动", "苹果服务 = select, DIRECT, 故障转移, 全球手动, 全球自动", "人工智能 = select, 故障转移, 全球手动, 全球自动, DIRECT"} {
				if !strings.Contains(text, line) {
					t.Fatalf("changed default %s", line)
				}
			}
			if !flags["mihomo/ip/Private.mrs"] || !flags["mihomo/ip/China.mrs"] || flags["mihomo/ip/Netflix.mrs"] || flags["mihomo/domain/Private.mrs"] {
				t.Fatal("no-resolve must be derived from actual uses, including unreferenced IP set")
			}
			remote := sections["[Rule]"]
			if client == "surge" {
				if len(remote) != 29 || strings.Count(text, "include-all-proxies=true") != 3 || strings.Contains(text, ", url=") || !strings.Contains(text, "proxy-test-url = https://cp.cloudflare.com/generate_204") {
					t.Fatal("Surge native node test settings or rule count invalid")
				}
				remote = remote[:28]
			} else {
				remote = sections["[Remote Rule]"]
				if len(remote) != 28 || len(sections["[Rule]"]) != 1 || !reflect.DeepEqual(sections["[Remote Filter]"], []string{`全部节点 = NameRegex, FilterKey=".*"`}) || strings.Contains(text, "no-resolve=true") {
					t.Fatal("Loon native rule or node-filter layout invalid")
				}
			}
			for i, line := range remote {
				parts := strings.Split(original.Rules[i], ",")
				if !strings.Contains(line, "/"+client+"/"+resources.Status.ReleaseID+"/"+source+"/") || !strings.Contains(line, parts[2]) || strings.Contains(line, ".mrs") {
					t.Fatalf("lost order/target or immutable text URL at %d: %s", i, line)
				}
				if client == "surge" && strings.HasSuffix(line, ",no-resolve") != (len(parts) == 4) {
					t.Fatal("Surge rule-set no-resolve changed")
				}
			}
			if sections["[Rule]"][len(sections["[Rule]"])-1] != "FINAL,漏网之鱼" {
				t.Fatal("final policy changed")
			}
		}
	}
	var cfg map[string]any
	_ = yaml.Unmarshal(input, &cfg)
	cfg["rules"].([]any)[4] = "RULE-SET,PrivateIP,DIRECT"
	changed, _ := yaml.Marshal(cfg)
	if _, _, err := s.miaomiaowuClientConfig(changed, resources, "loon", "local"); err == nil {
		t.Fatal("ambiguous no-resolve uses silently shared one artifact")
	}
}

func TestMiaomiaowuClientRulesPreserveDomainAndIPv6Semantics(t *testing.T) {
	for _, client := range []string{"surge", "loon"} {
		data, err := miaomiaowuClientRules(client, "domain", []string{"exact.example.com", "+.suffix.example.com"}, false)
		if err != nil || string(data) != "DOMAIN,exact.example.com\nDOMAIN-SUFFIX,suffix.example.com\n" {
			t.Fatalf("domain semantics changed: %s %v", data, err)
		}
		for _, flag := range []bool{false, true} {
			data, err = miaomiaowuClientRules(client, "ipcidr", []string{"192.0.2.0/24", "2001:db8::/32"}, flag)
			suffix := ""
			if client == "loon" && flag {
				suffix = ",no-resolve"
			}
			want := "IP-CIDR,192.0.2.0/24" + suffix + "\nIP-CIDR6,2001:db8::/32" + suffix + "\n"
			if err != nil || string(data) != want {
				t.Fatalf("IPv6/no-resolve changed: %s %v", data, err)
			}
		}
		for _, entries := range [][]string{nil, {"*.example.com"}, {"DOMAIN,a.example,DIRECT"}, {"+."}, {".example.com"}} {
			if _, err := miaomiaowuClientRules(client, "domain", entries, false); err == nil {
				t.Fatalf("unsupported MRS accepted: %v", entries)
			}
		}
	}
}

func TestMiaomiaowuClientPublicationConcurrentImmutableAndIndependent(t *testing.T) {
	s, root, resources, decodes := miaomiaowuClientFixture(t)
	clash, err := s.ensureMiaomiaowuTemplates(resources)
	if err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, dir := range []string{root, s.miaomiaowuDir(resources.Status.ReleaseID)} {
		if err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.Type().IsRegular() {
				before[path], err = os.ReadFile(path)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"routing.db", "usage.db", "subscription-signing.key"} {
		path := filepath.Join(s.dataDir, name)
		before[path] = []byte("old-" + name)
		if err = os.WriteFile(path, before[path], 0600); err != nil {
			t.Fatal(err)
		}
	}
	var fetches atomic.Int32
	s.resourceHTTPClient = &http.Client{Transport: ruleSourceTransport(func(r *http.Request) (*http.Response, error) {
		fetches.Add(1)
		prefix := "https://raw.githubusercontent.com/666OS/rules/" + resources.Status.Commit + "/"
		path := strings.TrimPrefix(r.URL.String(), prefix)
		if !strings.HasPrefix(r.URL.String(), prefix) || !legacyKnownPath(path) {
			return nil, fmt.Errorf("unapproved upstream URL: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("binary:" + path))}, nil
	})}
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, err := s.ensureMiaomiaowuClient(context.Background(), "surge", resources.Status.ReleaseID, "local"); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	if decodes.Load() != 33 || fetches.Load() != 0 {
		t.Fatalf("concurrent local preparation fetched or repeated decoding: %d/%d", decodes.Load(), fetches.Load())
	}
	var options []miaomiaowuClientOption
	for _, client := range []string{"surge", "loon"} {
		for _, source := range []string{"local", "upstream"} {
			option, err := s.ensureMiaomiaowuClient(context.Background(), client, resources.Status.ReleaseID, source)
			if err != nil || !option.Available || !option.Prepared || option.ID != source || option.ProviderCount != 33 || option.GroupCount != 20 || option.RuleCount != 29 {
				t.Fatalf("invalid prepared option: %+v %v", option, err)
			}
			options = append(options, option)
			manifest, err := s.readMiaomiaowuClientManifest(client, resources.Status.ReleaseID, source)
			if err != nil || len(manifest.Files) != 34 || len(manifest.Inputs) != 34 {
				t.Fatalf("incomplete atomic publication: %+v %v", manifest, err)
			}
		}
	}
	if fetches.Load() != 66 || decodes.Load() != 33 {
		t.Fatalf("sources/decoder cache wrongly shared: fetches=%d, decodes=%d", fetches.Load(), decodes.Load())
	}
	for path, expected := range before {
		data, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(data, expected) {
			t.Fatalf("old data or Clash artifact changed: %s %v", path, err)
		}
	}
	if err = os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(s.legacyResourceDir(resources.Status.ReleaseID)); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.registerMiaomiaowuRoutes(mux)
	for _, option := range options {
		request := httptest.NewRequest("GET", option.TemplateURL, nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != 200 || routingSHA256(response.Body.Bytes()) != option.SHA256 || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" || response.Header().Get("Content-Disposition") != "" || response.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
			t.Fatalf("independent public template unavailable: %d %s", response.Code, response.Body.String())
		}
		request.Header.Set("If-None-Match", response.Header().Get("ETag"))
		response = httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != 304 {
			t.Fatal("ETag ignored")
		}
		response = httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest("GET", option.TemplateURL+"?download=1", nil))
		if response.Header().Get("Content-Disposition") != `attachment; filename="`+option.Filename+`"` {
			t.Fatal("download filename changed")
		}
		base := strings.TrimSuffix(option.TemplateURL, option.Filename)
		for _, path := range legacyResourcePaths() {
			response = httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest("GET", base+miaomiaowuRulesetID(path)+".list", nil))
			if response.Code != 200 {
				t.Fatalf("public text dependency lost after cleanup: %s %d", path, response.Code)
			}
		}
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", clash.Options[0].TemplateURL, nil))
	if response.Code != 200 || routingSHA256(response.Body.Bytes()) != clash.Options[0].SHA256 {
		t.Fatal("old Clash fixed URL changed")
	}
}

func TestMiaomiaowuClientUpstreamFailuresDoNotDecodeOrPublish(t *testing.T) {
	for _, failure := range []string{"500", "sha", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			s, _, resources, decodes := miaomiaowuClientFixture(t)
			local, err := s.ensureMiaomiaowuClient(context.Background(), "loon", resources.Status.ReleaseID, "local")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.resourceHTTPClient = &http.Client{Transport: ruleSourceTransport(func(r *http.Request) (*http.Response, error) {
				if failure == "cancel" {
					cancel()
					return nil, r.Context().Err()
				}
				status := 200
				if failure == "500" {
					status = 500
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("wrong bytes"))}, nil
			})}
			if _, err = s.ensureMiaomiaowuClient(ctx, "loon", resources.Status.ReleaseID, "upstream"); err == nil {
				t.Fatal("failed upstream silently used local")
			}
			if decodes.Load() != 33 {
				t.Fatal("unverified upstream reached decoder")
			}
			if _, err = os.Lstat(s.miaomiaowuClientDir("loon", resources.Status.ReleaseID, "upstream")); !os.IsNotExist(err) {
				t.Fatal("failed source was published")
			}
			mux := http.NewServeMux()
			s.registerMiaomiaowuRoutes(mux)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest("GET", local.TemplateURL, nil))
			if response.Code != 200 {
				t.Fatal("upstream failure invalidated independent local")
			}
		})
	}
}

func TestMiaomiaowuClientCorruptionNeverRepairsOrReturns304(t *testing.T) {
	for _, corrupt := range []string{"template", "ruleset", "manifest-path", "manifest-missing", "symlink", "input"} {
		t.Run(corrupt, func(t *testing.T) {
			s, _, resources, decodes := miaomiaowuClientFixture(t)
			option, err := s.ensureMiaomiaowuClient(context.Background(), "surge", resources.Status.ReleaseID, "local")
			if err != nil {
				t.Fatal(err)
			}
			dir := s.miaomiaowuClientDir("surge", resources.Status.ReleaseID, "local")
			filename := option.Filename
			if corrupt == "ruleset" || corrupt == "symlink" {
				filename = "domain-Google.list"
			}
			path := filepath.Join(dir, filename)
			data, _ := os.ReadFile(path)
			sha := routingSHA256(data)
			switch corrupt {
			case "manifest-missing":
				err = os.Remove(filepath.Join(dir, "manifest.json"))
			case "manifest-path":
				manifest, _ := s.readMiaomiaowuClientManifest("surge", resources.Status.ReleaseID, "local")
				manifest.Files["../private.txt"] = manifest.Files["domain-Google.list"]
				delete(manifest.Files, "domain-Google.list")
				encoded, _ := json.Marshal(manifest)
				err = os.WriteFile(filepath.Join(dir, "manifest.json"), encoded, 0644)
			case "symlink":
				target := filepath.Join(t.TempDir(), "copied.list")
				if err = os.WriteFile(target, data, 0644); err == nil {
					err = os.Remove(path)
				}
				if err == nil {
					err = os.Symlink(target, path)
				}
			case "input":
				err = os.WriteFile(filepath.Join(s.legacyResourceDir(resources.Status.ReleaseID), "mihomo/domain/Google.mrs"), []byte("corrupt"), 0644)
			default:
				data[0] ^= 1 // same size, different SHA
				err = os.WriteFile(path, data, 0644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ensureMiaomiaowuClient(context.Background(), "surge", resources.Status.ReleaseID, "local"); err == nil || decodes.Load() != 33 {
				t.Fatal("corrupt cache was reused or repaired")
			}
			mux := http.NewServeMux()
			s.registerMiaomiaowuRoutes(mux)
			request := httptest.NewRequest("GET", strings.TrimSuffix(option.TemplateURL, option.Filename)+filename, nil)
			request.Header.Set("If-None-Match", `"`+sha+`"`)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if corrupt == "input" {
				if response.Code != 304 {
					t.Fatal("source corruption invalidated independently published artifact")
				}
			} else if response.Code != 503 {
				t.Fatalf("corruption returned content or trusted ETag: %d", response.Code)
			}
		})
	}
}

func TestMiaomiaowuClientCatalogBoundariesAndCancellation(t *testing.T) {
	s, _, resources, decodes := miaomiaowuClientFixture(t)
	s.resourceHTTPClient = &http.Client{Transport: ruleSourceTransport(func(*http.Request) (*http.Response, error) {
		t.Error("catalog or invalid request performed a network fetch")
		return nil, fmt.Errorf("unexpected network")
	})}
	mux := http.NewServeMux()
	s.registerMiaomiaowuRoutes(mux)
	for _, target := range []string{"/api/templates/miaomiaowu?client=surge", "/api/templates/miaomiaowu/prepare"} {
		method := "GET"
		if strings.HasSuffix(target, "/prepare") {
			method = "POST"
		}
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, httptest.NewRequest(method, target, nil))
		if out.Code != 401 {
			t.Fatal("admin route publicly available")
		}
	}
	for _, client := range []string{"surge", "loon"} {
		out := httptest.NewRecorder()
		s.miaomiaowuTemplateOptions(out, httptest.NewRequest("GET", "/api/templates/miaomiaowu?client="+client, nil))
		var got struct {
			Client    string                   `json:"client"`
			Extension string                   `json:"extension"`
			Options   []miaomiaowuClientOption `json:"source_options"`
		}
		if json.Unmarshal(out.Body.Bytes(), &got) != nil || got.Client != client || got.Extension != miaomiaowuClientExtension(client) || len(got.Options) != 2 {
			t.Fatalf("catalog contract: %s", out.Body.String())
		}
		for _, option := range got.Options {
			if !option.Available || option.Prepared || option.TemplateURL != "" || option.Revision != resources.Status.ReleaseID || option.Filename != miaomiaowuClientFilename(client, option.ID) {
				t.Fatalf("catalog pre-generated or exposed invalid option: %+v", option)
			}
		}
	}
	for _, body := range []string{`{"client":"clash","source":"local","revision":"valid"}`, `{"client":"surge","source":"https://evil.example","revision":"valid"}`, `{"client":"loon","source":"local","revision":"../bad"}`, `{"client":"loon","source":"local","revision":"valid","url":"https://evil.example"}`, `{} {}`} {
		out := httptest.NewRecorder()
		s.miaomiaowuPrepareClient(out, httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if out.Code != 400 {
			t.Fatalf("invalid preparation accepted: %s %d", body, out.Code)
		}
	}
	for _, suffix := range []string{"surge/valid/other/domain-Google.list", "clash/valid/local/domain-Google.list", "surge/valid/local/manifest.json", "surge/valid/local/domain-Unknown.list", "surge/missing/local/domain-Google.list"} {
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, httptest.NewRequest("GET", "/_miaomiaowu/clients/v1/"+suffix, nil))
		if out.Code != 404 {
			t.Fatalf("invalid public artifact accepted: %s %d", suffix, out.Code)
		}
	}
	if decodes.Load() != 0 {
		t.Fatal("catalog performed expensive decoding")
	}
	s.miaomiaowuClientMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := s.ensureMiaomiaowuClient(ctx, "surge", resources.Status.ReleaseID, "local")
	s.miaomiaowuClientMu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second || decodes.Load() != 0 {
		t.Fatal("canceled waiter blocked or generated after cancellation")
	}
}

func TestMiaomiaowuClientFailedCandidateAndBadOriginalAreNotPublished(t *testing.T) {
	for _, failure := range []string{"decoder", "canceled-decode", "bad-original"} {
		t.Run(failure, func(t *testing.T) {
			s, _, resources, _ := miaomiaowuClientFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int32
			s.mrsDecoder = func(context.Context, string, []byte) ([]string, error) {
				calls.Add(1)
				if failure == "canceled-decode" {
					cancel()
					return []string{"exact.example.com"}, nil
				}
				return nil, fmt.Errorf("decoder failed")
			}
			if failure == "bad-original" {
				path := filepath.Join(s.legacyResourceDir(resources.Status.ReleaseID), "mihomo/domain/Google.mrs")
				if err := os.WriteFile(path, []byte("bad"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.ensureMiaomiaowuClient(ctx, "loon", resources.Status.ReleaseID, "local"); err == nil {
				t.Fatal("failed conversion reported success")
			}
			dir := s.miaomiaowuClientDir("loon", resources.Status.ReleaseID, "local")
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Fatal("failed conversion published a directory")
			}
			candidates, err := filepath.Glob(filepath.Join(filepath.Dir(dir), ".candidate-*"))
			if err != nil || len(candidates) != 0 {
				t.Fatal("failed conversion left a temporary publication")
			}
			if failure == "bad-original" && calls.Load() != 0 {
				t.Fatal("bad original reached decoder")
			}
		})
	}
}

func TestMiaomiaowuClientPreparedAPIAndReadsNeverWaitForGenerationLock(t *testing.T) {
	s, _, resources, _ := miaomiaowuClientFixture(t)
	body, _ := json.Marshal(map[string]string{"client": "surge", "source": "local", "revision": resources.Status.ReleaseID})
	out := httptest.NewRecorder()
	s.miaomiaowuPrepareClient(out, httptest.NewRequest("POST", "/", strings.NewReader(string(body))))
	var option miaomiaowuClientOption
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &option) != nil || !option.Available || !option.Prepared || option.ID != "local" || option.Revision != resources.Status.ReleaseID || option.Filename != "coralbay_yyds_local__surge.conf" || option.TemplateURL == "" {
		t.Fatalf("prepare response contract: %d %s", out.Code, out.Body.String())
	}
	mux := http.NewServeMux()
	s.registerMiaomiaowuRoutes(mux)
	s.miaomiaowuClientMu.Lock()
	defer s.miaomiaowuClientMu.Unlock()
	finished := make(chan error, 1)
	go func() {
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, httptest.NewRequest("GET", option.TemplateURL, nil))
		if out.Code != 200 {
			finished <- fmt.Errorf("published GET: %d", out.Code)
			return
		}
		out = httptest.NewRecorder()
		s.miaomiaowuTemplateOptions(out, httptest.NewRequest("GET", "/api/templates/miaomiaowu?client=surge", nil))
		var got struct {
			Options []miaomiaowuClientOption `json:"source_options"`
		}
		if json.Unmarshal(out.Body.Bytes(), &got) != nil || len(got.Options) != 2 || !got.Options[0].Prepared || got.Options[0].TemplateURL != option.TemplateURL || got.Options[1].Prepared || !got.Options[1].Available {
			finished <- fmt.Errorf("prepared catalog: %s", out.Body.String())
			return
		}
		finished <- nil
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GET waited on the expensive generation lock")
	}
}
