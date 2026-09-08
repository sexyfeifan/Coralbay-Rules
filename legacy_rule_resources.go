package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed expected-files.txt
var legacyRuleFiles string

var legacyVersionPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,199}$`)
var legacyCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

const legacyResourceMaxBytes = 24 << 20

type legacyResourceFile struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type legacyResourceManifest struct {
	Status mirrorStatus                  `json:"status"`
	Files  map[string]legacyResourceFile `json:"files"`
}

type legacyUpstreamState struct {
	Revision    string `json:"revision"`
	GeoRevision string `json:"geo_revision"`
	CheckedAt   string `json:"checked_at"`
	LastError   string `json:"last_error,omitempty"`
}

func legacyResourcePaths() []string { return strings.Fields(legacyRuleFiles) }

func legacyKnownPath(path string) bool {
	for _, item := range legacyResourcePaths() {
		if item == path {
			return true
		}
	}
	return false
}

func (s *server) registerLegacyResourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/resources/666os/details", s.auth(s.legacyResourceDetails))
	mux.HandleFunc("POST /api/resources/666os/check", s.auth(s.legacyResourceCheck))
	mux.HandleFunc("GET /api/resources/666os/mihomopro", s.auth(s.mihomoProSourceOptions))
	mux.HandleFunc("GET /_rule-resources/666os/{revision}/{file...}", s.legacyResourceFile)
	mux.HandleFunc("GET /_rule-templates/666os/{revision}/{source}/{client}", s.legacyResourceTemplate)
}

func (s *server) legacyResourceDir(revision string) string {
	return filepath.Join(s.dataDir, "rule-resources", "666os", revision)
}

func (s *server) legacyCurrent() (string, mirrorStatus, error) {
	var status mirrorStatus
	root, err := filepath.EvalSymlinks(filepath.Join(s.dataDir, "current"))
	if err != nil {
		return "", status, err
	}
	content, err := os.ReadFile(filepath.Join(root, "_mirror", "status.json"))
	if err != nil {
		return "", status, err
	}
	if json.Unmarshal(content, &status) != nil || !status.OK || !legacyVersionPattern.MatchString(status.ReleaseID) || !legacyCommitPattern.MatchString(status.Commit) || !legacyCommitPattern.MatchString(status.GeoCommit) {
		return "", status, fmt.Errorf("本地规则版本信息尚未就绪")
	}
	return root, status, nil
}

// Keep the public resources independently of the legacy three-release cleanup.
// Source releases are immutable; hard links save space without following symlinks.
func (s *server) retainLegacyResources() (legacyResourceManifest, error) {
	s.legacyResourceMu.Lock()
	defer s.legacyResourceMu.Unlock()
	root, status, err := s.legacyCurrent()
	if err != nil {
		return legacyResourceManifest{}, err
	}
	destination := s.legacyResourceDir(status.ReleaseID)
	if content, err := os.ReadFile(filepath.Join(destination, "manifest.json")); err == nil {
		var saved legacyResourceManifest
		if json.Unmarshal(content, &saved) == nil && saved.Status.ReleaseID == status.ReleaseID {
			for _, path := range legacyResourcePaths() {
				if _, ok := saved.Files[path]; !ok {
					return saved, fmt.Errorf("本地固定资源缺失或摘要不一致：%s", path)
				}
			}
			// Associated geo and templates carry the same integrity guarantee.
			for path := range saved.Files {
				if _, err := s.legacyVerifiedResource(saved, path); err != nil {
					return saved, fmt.Errorf("本地固定资源缺失或摘要不一致：%s", path)
				}
			}
			if err := s.retainMihomoProInputs(root, &saved); err != nil {
				return saved, err
			}
			return saved, nil
		}
		return saved, fmt.Errorf("本地资源清单无效")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return legacyResourceManifest{}, err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(destination), ".candidate-")
	if err != nil {
		return legacyResourceManifest{}, err
	}
	defer os.RemoveAll(temporary)
	manifest := legacyResourceManifest{Status: status, Files: make(map[string]legacyResourceFile)}
	paths := legacyResourcePaths()
	required := len(paths)
	seen := map[string]bool{}
	for _, path := range paths {
		seen[path] = true
	}
	for _, path := range legacyResourcePaths() {
		if source := readableSource(path); source != "" {
			path = "_sources/geo/" + source
			if !seen[path] {
				paths = append(paths, path)
				seen[path] = true
			}
		}
	}
	paths = append(paths, "_sources/geo/LICENSE.txt")
	for _, client := range []string{"clash", "mihomo", "openclash", "stash"} {
		paths = append(paths, "_templates/clients/"+client+".gotmpl")
	}
	paths = append(paths, mihomoProInputPaths...)
	for i, path := range paths {
		from := filepath.Join(root, filepath.FromSlash(path))
		info, err := os.Lstat(from)
		if err != nil && os.IsNotExist(err) && i >= required {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > legacyResourceMaxBytes {
			return manifest, fmt.Errorf("本地资源缺失或无效：%s", path)
		}
		content, err := os.ReadFile(from)
		if err != nil {
			return manifest, err
		}
		digest := sha256.Sum256(content)
		to := filepath.Join(temporary, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
			return manifest, err
		}
		if err := os.Link(from, to); err != nil {
			if err := os.WriteFile(to, content, 0644); err != nil {
				return manifest, err
			}
		}
		manifest.Files[path] = legacyResourceFile{Bytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		return manifest, err
	}
	if err := os.WriteFile(filepath.Join(temporary, "manifest.json"), content, 0644); err != nil {
		return manifest, err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func (s *server) legacySourceStatus(manifest legacyResourceManifest, count int) map[string]any {
	return map[string]any{"local_revision": manifest.Status.ReleaseID, "local_commit": manifest.Status.Commit,
		"local_geo_commit": manifest.Status.GeoCommit, "local_updated_at": manifest.Status.SyncedAt,
		"local_count": count, "total": len(legacyResourcePaths()), "upstream": s.legacyReadUpstream(),
		"repository": "https://github.com/666OS/rules", "branch": "release"}
}

func (s *server) legacyRuleCatalog(w http.ResponseWriter, _ *http.Request) {
	manifest, snapshotErr := s.retainLegacyResources()
	if snapshotErr != nil {
		_, manifest.Status, _ = s.legacyCurrent()
		manifest.Files = nil
	}
	upstream := s.legacyReadUpstream()
	items := make([]map[string]any, 0, len(legacyResourcePaths()))
	ready := 0
	for _, path := range legacyResourcePaths() {
		behavior := "domain"
		if strings.Contains(path, "/ip/") {
			behavior = "ipcidr"
		}
		file, cached := manifest.Files[path]
		localURL := ""
		if cached {
			ready++
			localURL = "https://" + s.domain + "/_rule-resources/666os/" + manifest.Status.ReleaseID + "/" + path
		}
		item := map[string]any{"name": strings.TrimSuffix(filepath.Base(path), ".mrs"), "path": path, "behavior": behavior,
			"format": "mrs", "cached": cached, "bytes": file.Bytes, "sha256": file.SHA256, "readable": readableSource(path) != "",
			"original_url": "https://github.com/666OS/rules/raw/release/" + path, "mirror_url": "https://" + s.domain + "/" + path,
			"local_url": localURL, "local_revision": manifest.Status.ReleaseID, "modified": manifest.Status.SyncedAt,
			"upstream_revision": upstream.Revision, "upstream_checked_at": upstream.CheckedAt,
			"icon_url": "/_assets/icons/" + ruleIcon(path), "detail": "MRS 二进制规则；关联 geo 文本仅供参考，不是二进制文件的精确还原。"}
		if source := readableSource(path); source != "" {
			item["source_url"] = "https://github.com/666OS/rules/blob/geo/" + source
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"rules": items, "count": len(items), "source_status": s.legacySourceStatus(manifest, ready), "local_error": errorText(snapshotErr)})
}

func (s *server) legacyUpstreamPath() string {
	return filepath.Join(s.dataDir, "rule-sources", "666os", "check.json")
}

func (s *server) legacyReadUpstream() legacyUpstreamState {
	var state legacyUpstreamState
	content, _ := os.ReadFile(s.legacyUpstreamPath())
	_ = json.Unmarshal(content, &state)
	return state
}

func (s *server) legacyFetch(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "CoralBay-Rules/"+version)
	client := s.resourceHTTPClient
	if client == nil {
		client = safeHTTPClient(25 * time.Second)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("上游访问失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("上游返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, legacyResourceMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > legacyResourceMaxBytes {
		return nil, fmt.Errorf("上游文件为空、过大或读取失败")
	}
	return data, nil
}

func (s *server) legacyCheckUpstream(ctx context.Context) (legacyUpstreamState, error) {
	s.legacyUpstreamMu.Lock()
	defer s.legacyUpstreamMu.Unlock()
	previous := s.legacyReadUpstream()
	checked, _ := time.Parse(time.RFC3339Nano, previous.CheckedAt)
	if !checked.IsZero() && time.Since(checked) >= 0 && time.Since(checked) < time.Minute {
		if previous.LastError != "" {
			return previous, fmt.Errorf("%s", previous.LastError)
		}
		return previous, nil
	}
	state := previous
	state.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	state.LastError = ""
	refs := []string{}
	for _, branch := range []string{"release", "geo"} {
		data, err := s.legacyFetch(ctx, "https://api.github.com/repos/666OS/rules/git/ref/heads/"+branch)
		var ref struct {
			Ref    string `json:"ref"`
			Object struct {
				SHA  string `json:"sha"`
				Type string `json:"type"`
			} `json:"object"`
		}
		if err != nil {
			state.LastError = err.Error()
			break
		}
		if json.Unmarshal(data, &ref) != nil || ref.Ref != "refs/heads/"+branch || ref.Object.Type != "commit" || !legacyCommitPattern.MatchString(ref.Object.SHA) {
			state.LastError = "上游版本响应无效"
			break
		}
		refs = append(refs, ref.Object.SHA)
	}
	if len(refs) == 2 {
		state.Revision = refs[0]
		state.GeoRevision = refs[1]
	}
	data, _ := json.Marshal(state)
	path := s.legacyUpstreamPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return state, err
	}
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return state, err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return state, err
	}
	if state.LastError != "" {
		return state, fmt.Errorf("%s", state.LastError)
	}
	return state, nil
}

func (s *server) legacyResourceCheck(w http.ResponseWriter, r *http.Request) {
	state, err := s.legacyCheckUpstream(r.Context())
	status := 200
	if err != nil {
		status = 502
	}
	writeJSON(w, status, map[string]any{"revision": state.Revision, "geo_revision": state.GeoRevision, "checked_at": state.CheckedAt, "last_error": state.LastError, "error": errorText(err)})
}

func legacyEntries(content []byte) []string {
	entries := []string{}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			entries = append(entries, line)
		}
	}
	return entries
}

func legacyPaginate(entries []string, r *http.Request) ([]string, int, int, int) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	filtered := []string{}
	for _, entry := range entries {
		if query == "" || strings.Contains(strings.ToLower(entry), query) {
			filtered = append(filtered, entry)
		}
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if size < 1 || size > 500 {
		size = 200
	}
	// Clamp before multiplication so very large query parameters cannot overflow.
	maxPage := max(1, (len(filtered)+size-1)/size)
	if page > maxPage {
		page = maxPage
	}
	start := (page - 1) * size
	end := min(len(filtered), start+size)
	return filtered[start:end], len(filtered), page, size
}

func (s *server) legacyUpstreamFile(ctx context.Context, revision, path string) ([]byte, error) {
	if !legacyCommitPattern.MatchString(revision) {
		return nil, fmt.Errorf("请先检查上游版本")
	}
	destination := filepath.Join(s.dataDir, "rule-sources", "666os", "upstream", revision, filepath.FromSlash(path))
	if data, err := os.ReadFile(destination); err == nil {
		return data, nil
	}
	data, err := s.legacyFetch(ctx, "https://raw.githubusercontent.com/666OS/rules/"+revision+"/"+path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".fetch-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := os.Rename(file.Name(), destination); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *server) legacyResourceDetails(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	path, source := r.URL.Query().Get("path"), r.URL.Query().Get("source")
	if source == "" {
		source = "local"
	}
	if !legacyKnownPath(path) || (source != "local" && source != "upstream" && source != "diff") {
		writeJSON(w, 400, map[string]string{"error": "规则或来源无效"})
		return
	}
	_, status, localErr := s.legacyCurrent()
	behavior := "domain"
	if strings.Contains(path, "/ip/") {
		behavior = "ipcidr"
	}
	result := map[string]any{"source": source, "path": path, "name": strings.TrimSuffix(filepath.Base(path), ".mrs"), "format": "mrs", "behavior": behavior, "revision": status.Commit, "geo_revision": status.GeoCommit, "updated_at": status.SyncedAt, "content_kind": "metadata", "readable": false, "cached": false, "entries": []string{}, "total": 0, "page": 1, "page_size": 200}
	var content, readable []byte
	readPath := readableSource(path)
	result["url"] = "https://" + s.domain + "/" + path
	if localErr == nil {
		manifest, manifestErr := s.legacyReadManifest(status.ReleaseID)
		if os.IsNotExist(manifestErr) {
			manifest, manifestErr = s.retainLegacyResources()
		}
		if manifestErr == nil {
			content, localErr = s.legacyVerifiedResource(manifest, path)
			if readPath != "" {
				var readableErr error
				readable, readableErr = s.legacyVerifiedResource(manifest, "_sources/geo/"+readPath)
				if readableErr != nil {
					result["readable_error"] = readableErr.Error()
				}
			}
		} else {
			localErr = manifestErr
		}
		if localErr == nil {
			result["local_url"] = "https://" + s.domain + "/_rule-resources/666os/" + status.ReleaseID + "/" + path
			if source == "local" {
				result["url"] = result["local_url"]
			}
		}
	}
	if localErr != nil {
		result["local_error"] = "本地副本校验失败：" + localErr.Error()
	}
	localContent, localReadable := content, readable
	if source != "local" {
		state := s.legacyReadUpstream()
		if !legacyCommitPattern.MatchString(state.Revision) || !legacyCommitPattern.MatchString(state.GeoRevision) {
			writeJSON(w, 409, map[string]string{"error": "请先检查上游版本，再查看详情"})
			return
		}
		var err error
		content, err = s.legacyUpstreamFile(r.Context(), state.Revision, path)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		readable = nil
		if readPath != "" {
			readable, err = s.legacyUpstreamFile(r.Context(), state.GeoRevision, readPath)
			if err != nil {
				result["readable_error"] = err.Error()
			}
		}
		result["revision"], result["geo_revision"], result["checked_at"] = state.Revision, state.GeoRevision, state.CheckedAt
		result["updated_at"] = ""
		if info, err := os.Stat(filepath.Join(s.dataDir, "rule-sources", "666os", "upstream", state.Revision, filepath.FromSlash(path))); err == nil {
			result["updated_at"] = info.ModTime().UTC()
		}
		result["url"] = "https://raw.githubusercontent.com/666OS/rules/" + state.Revision + "/" + path
		if source == "diff" {
			result["comparable"] = len(localReadable) > 0 && len(readable) > 0
			result["local_revision"], result["upstream_revision"] = status.Commit, state.Revision
			result["local_geo_revision"], result["upstream_geo_revision"] = status.GeoCommit, state.GeoRevision
			if len(localContent) > 0 {
				result["same"] = sha256.Sum256(localContent) == sha256.Sum256(content)
			}
			if len(localReadable) > 0 && len(readable) > 0 {
				a, b := map[string]bool{}, map[string]bool{}
				for _, entry := range legacyEntries(localReadable) {
					a[entry] = true
				}
				for _, entry := range legacyEntries(readable) {
					b[entry] = true
				}
				changes := []string{}
				added, removed := 0, 0
				for entry := range b {
					if !a[entry] {
						changes = append(changes, "+ "+entry)
						added++
					}
				}
				for entry := range a {
					if !b[entry] {
						changes = append(changes, "- "+entry)
						removed++
					}
				}
				sort.Strings(changes)
				result["entries"], result["total"], result["page"], result["page_size"] = legacyPaginate(changes, r)
				result["added_count"], result["removed_count"] = added, removed
			}
		}
	}
	if len(content) > 0 {
		digest := sha256.Sum256(content)
		result["cached"] = true
		result["bytes"] = len(content)
		result["sha256"] = hex.EncodeToString(digest[:])
	}
	if len(readable) > 0 {
		result["readable"], result["content_kind"] = true, "associated_geo"
		result["source_url"] = "https://raw.githubusercontent.com/666OS/rules/" + fmt.Sprint(result["geo_revision"]) + "/" + readPath
		if source != "diff" {
			result["entries"], result["total"], result["page"], result["page_size"] = legacyPaginate(legacyEntries(readable), r)
		}
	}
	result["note"] = "条目来自关联 geo 文本，具有独立版本；不表示对该 MRS 的精确反向还原。无公开可读源时仅显示二进制元信息。"
	writeJSON(w, 200, result)
}

func (s *server) legacyReadManifest(revision string) (legacyResourceManifest, error) {
	var manifest legacyResourceManifest
	if !legacyVersionPattern.MatchString(revision) {
		return manifest, fmt.Errorf("版本无效")
	}
	data, err := os.ReadFile(filepath.Join(s.legacyResourceDir(revision), "manifest.json"))
	if err != nil {
		return manifest, err
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Status.ReleaseID != revision {
		return manifest, fmt.Errorf("资源清单无效")
	}
	return manifest, nil
}

func (s *server) legacyResourceFile(w http.ResponseWriter, r *http.Request) {
	revision, path := r.PathValue("revision"), r.PathValue("file")
	if !legacyKnownPath(path) {
		http.NotFound(w, r)
		return
	}
	manifest, err := s.legacyReadManifest(revision)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, ok := manifest.Files[path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := s.legacyVerifiedResource(manifest, path)
	if err != nil {
		http.Error(w, "本地规则文件不可用", 503)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+file.SHA256+`"`)
	if r.Header.Get("If-None-Match") == `"`+file.SHA256+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(data)
}

func legacyTemplateSupported(client string) bool {
	return client == "clash" || client == "mihomo" || client == "openclash" || client == "stash"
}

func (s *server) legacyTemplateOptions(client string, manifest legacyResourceManifest) []map[string]string {
	options := []map[string]string{}
	if !legacyTemplateSupported(client) || manifest.Status.ReleaseID == "" {
		return options
	}
	if _, ok := manifest.Files["_templates/clients/"+client+".gotmpl"]; !ok {
		return options
	}
	for _, source := range []string{"local", "upstream"} {
		description := "客户端下载 CoralBay 固定版本规则"
		if source == "upstream" {
			description = "客户端下载 666OS 同提交原始规则；图标仍由 CoralBay 提供"
		}
		options = append(options, map[string]string{"id": source, "url": "https://" + s.domain + "/_rule-templates/666os/" + manifest.Status.ReleaseID + "/" + source + "/" + client, "description": description, "revision": manifest.Status.Commit})
	}
	return options
}

func (s *server) legacyResourceTemplate(w http.ResponseWriter, r *http.Request) {
	revision, source, client := r.PathValue("revision"), r.PathValue("source"), r.PathValue("client")
	if client == "mihomopro-config" || client == "mihomopro-overwrite" {
		s.mihomoProSourceFile(w, r)
		return
	}
	if !legacyTemplateSupported(client) || (source != "local" && source != "upstream") {
		http.NotFound(w, r)
		return
	}
	manifest, err := s.legacyReadManifest(revision)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	path := "_templates/clients/" + client + ".gotmpl"
	meta, ok := manifest.Files[path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(s.legacyResourceDir(revision), filepath.FromSlash(path)))
	digest := sha256.Sum256(data)
	if err != nil || hex.EncodeToString(digest[:]) != meta.SHA256 {
		http.Error(w, "模板副本不可用", 503)
		return
	}
	content := string(data)
	for _, path := range legacyResourcePaths() {
		url := "https://" + s.domain + "/_rule-resources/666os/" + revision + "/" + path
		if source == "upstream" {
			url = "https://raw.githubusercontent.com/666OS/rules/" + manifest.Status.Commit + "/" + path
		}
		content = strings.ReplaceAll(content, "https://"+manifest.Status.MirrorDomain+"/"+path, url)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-CoralBay-Rule-Source", source)
	w.Header().Set("X-CoralBay-Rule-Revision", manifest.Status.Commit)
	w.Write([]byte(content))
}
