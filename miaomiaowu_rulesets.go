package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const miaomiaowuYAMLMaxBytes = 64 << 20

type miaomiaowuRuleset struct {
	ID           string `json:"id"`
	Filename     string `json:"filename"`
	Source       string `json:"source"`
	Revision     string `json:"revision"`
	RuleRevision string `json:"rule_revision"`
	Count        int    `json:"count"`
	SHA256       string `json:"sha256"`
	InputSHA256  string `json:"input_sha256"`
	YAMLURL      string `json:"yaml_url"`
	ProviderYAML string `json:"provider_yaml"`
	Format       string `json:"format"`
	Behavior     string `json:"behavior"`
	PreparedAt   string `json:"prepared_at"`
	Bytes        int64  `json:"bytes"`
}

func (s *server) registerMiaomiaowuRulesetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/templates/miaomiaowu/rulesets", s.auth(s.miaomiaowuRulesetCatalog))
	mux.HandleFunc("POST /api/templates/miaomiaowu/rulesets/{id}", s.auth(s.miaomiaowuPrepareRuleset))
	mux.HandleFunc("GET /_miaomiaowu/rulesets/v1/{revision}/{source}/{file}", s.miaomiaowuRulesetFile)
}

func miaomiaowuRulesetID(path string) string {
	parts := strings.Split(path, "/")
	return parts[1] + "-" + strings.TrimSuffix(parts[2], ".mrs")
}

func miaomiaowuRulesetPath(id string) string {
	for _, path := range legacyResourcePaths() {
		if miaomiaowuRulesetID(path) == id {
			return path
		}
	}
	return ""
}

func miaomiaowuRulesetBehavior(path string) string {
	if strings.HasPrefix(path, "mihomo/ip/") {
		return "ipcidr"
	}
	return "domain"
}

func (s *server) miaomiaowuRulesetCatalog(w http.ResponseWriter, _ *http.Request) {
	routingPrivate(w)
	resources, err := s.retainLegacyResources()
	if err == nil && s.mrsDecoder == nil {
		if _, coreErr := os.Stat("/usr/local/bin/coralbay-probe-core"); coreErr != nil {
			err = fmt.Errorf("MRS 解码内核不可用，请使用 CoralBay 完整镜像")
		}
	}
	items := make([]map[string]any, 0, len(legacyResourcePaths()))
	for _, path := range legacyResourcePaths() {
		id := miaomiaowuRulesetID(path)
		item := map[string]any{"id": id, "name": strings.TrimSuffix(filepath.Base(path), ".mrs"), "behavior": miaomiaowuRulesetBehavior(path), "filename": "yyds-" + id + ".yaml"}
		if err == nil {
			item["local_mrs_url"] = "https://" + s.domain + "/_rule-resources/666os/" + resources.Status.ReleaseID + "/" + path
			item["upstream_mrs_url"] = "https://raw.githubusercontent.com/666OS/rules/" + resources.Status.Commit + "/" + path
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"revision": resources.Status.ReleaseID, "rule_revision": resources.Status.Commit, "source_name": "666OS / YYDS", "available": err == nil, "reason": errorText(err), "items": items, "total": len(items)})
}

func (s *server) miaomiaowuRulesetDir(revision, source, id string) string {
	return filepath.Join(s.dataDir, "miaomiaowu", "rulesets", "v1", revision, source, id)
}

func (s *server) readMiaomiaowuRuleset(revision, source, id string) (miaomiaowuRuleset, []byte, error) {
	var meta miaomiaowuRuleset
	if !legacyVersionPattern.MatchString(revision) || (source != "local" && source != "upstream") || miaomiaowuRulesetPath(id) == "" {
		return meta, nil, fmt.Errorf("规则集路径无效")
	}
	dir := s.miaomiaowuRulesetDir(revision, source, id)
	encoded, err := routingReadBoundedFile(filepath.Join(dir, "manifest.json"), 64<<10)
	if err != nil {
		return meta, nil, err
	}
	if json.Unmarshal(encoded, &meta) != nil || meta.ID != id || meta.Revision != revision || meta.Source != source || meta.Format != "yaml" || meta.Behavior != "classical" || meta.Count < 1 || meta.Bytes < 1 || meta.Bytes > miaomiaowuYAMLMaxBytes || !routingHashPattern.MatchString(meta.SHA256) || !routingHashPattern.MatchString(meta.InputSHA256) {
		return meta, nil, fmt.Errorf("规则集清单无效")
	}
	file := filepath.Join(dir, "content.yaml")
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() != meta.Bytes {
		return meta, nil, fmt.Errorf("规则集文件缺失或大小不符")
	}
	data, err := routingReadBoundedFile(file, miaomiaowuYAMLMaxBytes)
	if err != nil || routingSHA256(data) != meta.SHA256 {
		return meta, nil, fmt.Errorf("规则集文件摘要校验失败")
	}
	return meta, data, nil
}

// Export exact MRS contents as classical YAML, not the independent geo lists.
// There is no policy in a payload item: the template chooses its routing policy.
func miaomiaowuRulesetPayload(behavior string, entries []string) ([]byte, error) {
	if len(entries) < 1 || len(entries) > 1000000 {
		return nil, fmt.Errorf("规则集为空或条目超过上限")
	}
	payload := make([]string, 0, len(entries))
	for _, entry := range entries {
		rule, err := nativeMRSRule(behavior, entry, "CoralBayPayload", false)
		if err != nil {
			return nil, err
		}
		payload = append(payload, strings.TrimSuffix(rule, ",CoralBayPayload"))
	}
	data, err := yaml.Marshal(struct {
		Payload []string `yaml:"payload"`
	}{payload})
	if err != nil {
		return nil, err
	}
	if len(data) > miaomiaowuYAMLMaxBytes {
		return nil, fmt.Errorf("规则集 YAML 超过大小上限")
	}
	return data, nil
}

func (s *server) ensureMiaomiaowuRuleset(ctx context.Context, revision, source, id string) (miaomiaowuRuleset, error) {
	s.miaomiaowuRulesMu.Lock()
	defer s.miaomiaowuRulesMu.Unlock()
	var meta miaomiaowuRuleset
	path := miaomiaowuRulesetPath(id)
	if path == "" || !legacyVersionPattern.MatchString(revision) || (source != "local" && source != "upstream") {
		return meta, fmt.Errorf("规则集或来源无效")
	}
	var resources legacyResourceManifest
	encoded, err := routingReadBoundedFile(filepath.Join(s.legacyResourceDir(revision), "manifest.json"), 2<<20)
	if err != nil || json.Unmarshal(encoded, &resources) != nil || resources.Status.ReleaseID != revision || !resources.Status.OK || !legacyCommitPattern.MatchString(resources.Status.Commit) {
		return meta, fmt.Errorf("所选规则版本不可用，请刷新目录并同步 666OS 规则")
	}
	raw, err := s.legacyVerifiedResource(resources, path)
	if err != nil {
		return meta, err
	}
	inputSHA := routingSHA256(raw)
	dir := s.miaomiaowuRulesetDir(revision, source, id)
	if _, err := os.Lstat(dir); err == nil {
		meta, _, err = s.readMiaomiaowuRuleset(revision, source, id)
		if err == nil && meta.InputSHA256 != inputSHA {
			err = fmt.Errorf("已发布规则集与原件摘要不一致")
		}
		return meta, err
	} else if !os.IsNotExist(err) {
		return meta, err
	}
	if source == "upstream" {
		raw, err = s.legacyFetch(ctx, "https://raw.githubusercontent.com/666OS/rules/"+resources.Status.Commit+"/"+path)
		if err != nil {
			return meta, err
		}
		if int64(len(raw)) != resources.Files[path].Bytes || routingSHA256(raw) != inputSHA {
			return meta, fmt.Errorf("上游规则与所选固定版本摘要不一致")
		}
	}
	behavior := miaomiaowuRulesetBehavior(path)
	entries, err := s.decodedMRS(ctx, behavior, raw)
	if err != nil {
		return meta, err
	}
	data, err := miaomiaowuRulesetPayload(behavior, entries)
	if err != nil {
		return meta, err
	}
	if err = ctx.Err(); err != nil {
		return meta, err
	}
	address := "https://" + s.domain + "/_miaomiaowu/rulesets/v1/" + revision + "/" + source + "/" + id + ".yaml"
	providerName := strings.TrimSuffix(filepath.Base(path), ".mrs")
	if behavior == "ipcidr" {
		providerName += "IP"
	}
	provider, err := yaml.Marshal(map[string]any{"rule-providers": map[string]any{providerName: map[string]any{"type": "http", "format": "yaml", "behavior": "classical", "url": address, "path": "./rule-providers/yyds-" + id + ".yaml", "interval": 86400}}})
	if err != nil {
		return meta, err
	}
	meta = miaomiaowuRuleset{ID: id, Filename: "yyds-" + id + ".yaml", Source: source, Revision: revision, RuleRevision: resources.Status.Commit, Count: len(entries), SHA256: routingSHA256(data), InputSHA256: inputSHA, YAMLURL: address, ProviderYAML: string(provider), Format: "yaml", Behavior: "classical", PreparedAt: time.Now().UTC().Format(time.RFC3339), Bytes: int64(len(data))}
	if err = os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return meta, err
	}
	candidate, err := os.MkdirTemp(filepath.Dir(dir), ".candidate-")
	if err != nil {
		return meta, err
	}
	defer os.RemoveAll(candidate)
	if err = os.WriteFile(filepath.Join(candidate, "content.yaml"), data, 0644); err != nil {
		return meta, err
	}
	encoded, err = json.Marshal(meta)
	if err != nil {
		return meta, err
	}
	if err = os.WriteFile(filepath.Join(candidate, "manifest.json"), encoded, 0644); err != nil {
		return meta, err
	}
	if err = os.Rename(candidate, dir); err != nil {
		return meta, err
	}
	return meta, nil
}

func (s *server) miaomiaowuPrepareRuleset(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	var input struct {
		Revision string `json:"revision"`
		Source   string `json:"source"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || !legacyVersionPattern.MatchString(input.Revision) || (input.Source != "local" && input.Source != "upstream") || miaomiaowuRulesetPath(r.PathValue("id")) == "" {
		writeJSON(w, 400, map[string]string{"error": "请选择有效的规则集、版本与来源"})
		return
	}
	meta, err := s.ensureMiaomiaowuRuleset(r.Context(), input.Revision, input.Source, r.PathValue("id"))
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, meta)
}

func (s *server) miaomiaowuRulesetFile(w http.ResponseWriter, r *http.Request) {
	revision, source, filename := r.PathValue("revision"), r.PathValue("source"), r.PathValue("file")
	id := strings.TrimSuffix(filename, ".yaml")
	if !strings.HasSuffix(filename, ".yaml") || miaomiaowuRulesetPath(id) == "" || !legacyVersionPattern.MatchString(revision) || (source != "local" && source != "upstream") {
		http.NotFound(w, r)
		return
	}
	meta, data, err := s.readMiaomiaowuRuleset(revision, source, id)
	if err != nil {
		http.Error(w, "固定版本规则集尚未生成或文件校验失败", 503)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-CoralBay-Rule-Source", source)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+meta.SHA256+`"`)
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="yyds-`+id+`.yaml"`)
	}
	if r.Header.Get("If-None-Match") == w.Header().Get("ETag") {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(data)
}
