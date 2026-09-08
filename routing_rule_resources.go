package main

// Original MetaCubeX resources, inspection caches and active routing snapshots
// are deliberately separate. Reading a published resource never fetches it.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type routingRuleResource struct {
	ID           string `json:"id"`
	Revision     string `json:"revision"`
	Behavior     string `json:"behavior"`
	Format       string `json:"format"`
	Count        int    `json:"count"`
	Bytes        int64  `json:"bytes"`
	SHA256       string `json:"sha256"`
	SourceURL    string `json:"source_url"`
	LocalURL     string `json:"local_url,omitempty"`
	DownloadedAt string `json:"downloaded_at"`
	Content      []byte `json:"-"`
}

type routingResourceManifest struct {
	Version   int                            `json:"version"`
	Revision  string                         `json:"revision"`
	UpdatedAt string                         `json:"updated_at"`
	Resources map[string]routingRuleResource `json:"resources"`
}

type routingUpstreamStatus struct {
	Revision  string `json:"revision"`
	CheckedAt string `json:"checked_at"`
	LastError string `json:"last_error"`
}

type routingUpstreamResourceStatus struct {
	Resource  routingRuleResource `json:"resource"`
	CheckedAt string              `json:"checked_at"`
	LastError string              `json:"last_error"`
}

var routingErrNoRepairVersion = errors.New("没有已知本地版本，请先同步 MetaCubeX 规则")

func (s *server) cleanupRoutingCandidatePaths(revision string, paths []string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
	// Remove only empty candidate directories, never retained files or manifests.
	dir := filepath.Join(s.routingRulesDir(), "releases", revision)
	_ = os.Remove(filepath.Join(dir, "raw"))
	_ = os.Remove(dir)
}

func (s *server) routingPreviouslyKnownResource(revision, id string) bool {
	if manifest, err := s.readRoutingResourceManifest(revision); err == nil {
		if _, exists := manifest.Resources[id]; exists {
			return true
		}
	}
	if trusted, err := s.routingTrustedSnapshot(revision); err == nil {
		_, exists := trusted.Rules[id]
		return exists
	}
	return false
}

func (s *server) routingRawResourceURL(revision, id string) string {
	if !routingRevisionPattern.MatchString(revision) {
		return ""
	}
	if _, ok := routingRuleIndex()[id]; !ok {
		return ""
	}
	return "https://" + s.domain + "/_rule-resources/metacubex/" + revision + "/" + id + ".yaml"
}

func (s *server) routingRawPath(revision, id string) string {
	return filepath.Join(s.routingRulesDir(), "releases", revision, "raw", id+".yaml")
}

func routingResourceFromDocument(rule routingRule, revision string, doc routingRuleDocument) routingRuleResource {
	return routingRuleResource{ID: rule.ID, Revision: revision, Behavior: rule.Behavior, Format: "yaml", Count: len(doc.Entries), Bytes: doc.RawBytes, SHA256: doc.SHA256, SourceURL: doc.SourceURL, DownloadedAt: doc.DownloadedAt}
}

func (s *server) writeRoutingRawDocument(revision, id string, content []byte) error {
	if !routingRevisionPattern.MatchString(revision) || len(content) == 0 || len(content) > routingRuleMaxBytes {
		return fmt.Errorf("原始规则资源标识或大小无效")
	}
	if _, ok := routingRuleIndex()[id]; !ok {
		return fmt.Errorf("原始规则资源不在目录中")
	}
	// A branch can move backwards to an already served commit. Check the
	// historical publication before touching its file, not only on publication.
	manifest, err := s.readRoutingResourceManifest(revision)
	if err == nil {
		if published, exists := manifest.Resources[id]; exists && published.SHA256 != routingSHA256(content) {
			return fmt.Errorf("%s 的已发布固定版本内容不一致，拒绝改写原始资源", id)
		}
	} else {
		trusted, trustErr := s.routingTrustedSnapshot(revision)
		if trustErr == nil {
			if old, exists := trusted.Rules[id]; exists && old.SHA256 != routingSHA256(content) {
				return fmt.Errorf("%s 与已留存快照摘要不同，拒绝改写固定资源", id)
			}
		} else if !os.IsNotExist(trustErr) || !os.IsNotExist(err) {
			return fmt.Errorf("无法校验已有原始资源，拒绝改写: %w", trustErr)
		}
	}
	return routingRulesAtomicWrite(s.routingRawPath(revision, id), content)
}

func (s *server) readRoutingRawDocument(revision, id string, doc routingRuleDocument) ([]byte, error) {
	rule, ok := routingRuleIndex()[id]
	if !ok || !routingRevisionPattern.MatchString(revision) || !routingHashPattern.MatchString(doc.SHA256) || doc.SourceURL != routingPinnedURL(rule, revision) {
		return nil, fmt.Errorf("规则原始资源标识无效")
	}
	if doc.RawBytes < 1 || doc.RawBytes > routingRuleMaxBytes {
		return nil, fmt.Errorf("仅有旧规范化快照，原始 YAML 尚未镜像，请显式同步本地规则")
	}
	content, err := routingReadBoundedFile(s.routingRawPath(revision, id), routingRuleMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("原始 YAML 本地副本缺失，请同步本地规则")
	}
	if int64(len(content)) != doc.RawBytes || routingSHA256(content) != doc.SHA256 {
		return nil, fmt.Errorf("原始 YAML 本地副本摘要校验失败，请同步修复")
	}
	return content, nil
}

func (s *server) readRoutingResourceManifest(revision string) (routingResourceManifest, error) {
	var manifest routingResourceManifest
	if !routingRevisionPattern.MatchString(revision) {
		return manifest, fmt.Errorf("规则资源版本无效")
	}
	content, err := routingReadBoundedFile(filepath.Join(s.routingRulesDir(), "releases", revision, "resources.json"), 256<<10)
	if err != nil {
		return manifest, err
	}
	if json.Unmarshal(content, &manifest) != nil || manifest.Version != 1 || manifest.Revision != revision || len(manifest.Resources) > len(routingRuleCatalog()) {
		return routingResourceManifest{}, fmt.Errorf("原始规则资源清单无效")
	}
	index := routingRuleIndex()
	for id, resource := range manifest.Resources {
		rule, exists := index[id]
		if !exists || resource.ID != id || resource.Revision != revision || resource.SourceURL != routingPinnedURL(rule, revision) || resource.Behavior != rule.Behavior || resource.Format != "yaml" || resource.Count < 1 || resource.Count > routingRuleMaxEntries || resource.Bytes < 1 || resource.Bytes > routingRuleMaxBytes || !routingHashPattern.MatchString(resource.SHA256) {
			return routingResourceManifest{}, fmt.Errorf("原始规则资源清单条目无效")
		}
	}
	return manifest, nil
}

func (s *server) publishRoutingResourceManifest(snapshot routingDiskSnapshot) error {
	manifest, err := s.readRoutingResourceManifest(snapshot.Revision)
	if err != nil {
		manifest = routingResourceManifest{Version: 1, Revision: snapshot.Revision, Resources: map[string]routingRuleResource{}}
		trusted, trustErr := s.routingTrustedSnapshot(snapshot.Revision)
		if trustErr != nil && (!os.IsNotExist(trustErr) || !os.IsNotExist(err)) {
			return fmt.Errorf("清单损坏且没有可校验的恢复依据: %w", trustErr)
		}
		for id, doc := range trusted.Rules {
			if candidate, exists := snapshot.Rules[id]; exists {
				if candidate.SHA256 != doc.SHA256 {
					return fmt.Errorf("%s 固定资源与历史快照摘要冲突，拒绝重建清单", id)
				}
				continue
			}
			if doc.RawBytes > 0 {
				if _, err := s.readRoutingRawDocument(snapshot.Revision, id, doc); err != nil {
					return fmt.Errorf("历史资源 %s 尚未修复，拒绝发布不完整清单: %w", id, err)
				}
				manifest.Resources[id] = routingResourceFromDocument(routingRuleIndex()[id], snapshot.Revision, doc)
			}
		}
	}
	if manifest.Resources == nil {
		manifest.Resources = map[string]routingRuleResource{}
	}
	index := routingRuleIndex()
	for id, doc := range snapshot.Rules {
		if doc.RawBytes == 0 { // An older inline snapshot remains readable as before.
			continue
		}
		if _, err := s.readRoutingRawDocument(snapshot.Revision, id, doc); err != nil {
			return fmt.Errorf("%s 发布前原始文件校验失败: %w", id, err)
		}
		resource := routingResourceFromDocument(index[id], snapshot.Revision, doc)
		if old, exists := manifest.Resources[id]; exists && old.SHA256 != resource.SHA256 {
			return fmt.Errorf("%s 的已发布固定版本内容发生变化，拒绝替换", id)
		}
		manifest.Resources[id] = resource
	}
	if len(manifest.Resources) == 0 {
		return nil
	}
	manifest.UpdatedAt = snapshot.UpdatedAt
	content, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return routingRulesAtomicWrite(filepath.Join(s.routingRulesDir(), "releases", snapshot.Revision, "resources.json"), content)
}

func (s *server) routingPublishedResource(revision, id string) (routingRuleResource, error) {
	manifest, err := s.readRoutingResourceManifest(revision)
	if err != nil {
		return routingRuleResource{}, fmt.Errorf("该版本没有已发布的原始规则资源")
	}
	resource, ok := manifest.Resources[id]
	if !ok {
		return routingRuleResource{}, fmt.Errorf("该规则原始文件尚未镜像，请同步本地规则")
	}
	doc := routingRuleDocument{SHA256: resource.SHA256, RawBytes: resource.Bytes, SourceURL: resource.SourceURL}
	resource.Content, err = s.readRoutingRawDocument(revision, id, doc)
	if err != nil {
		return routingRuleResource{}, err
	}
	resource.LocalURL = s.routingRawResourceURL(revision, id)
	return resource, nil
}

// No network and no synchronization lock: the state pointer and resource files
// are atomically published, so a slow upstream cannot block local consumption.
func (s *server) loadPublishedRoutingRuleSnapshot(ctx context.Context, ids []string) (routingRuleSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return routingRuleSnapshot{}, err
	}
	state, snapshot, err := s.readRoutingRuleState()
	if err != nil {
		return routingRuleSnapshot{}, err
	}
	index := routingRuleIndex()
	var missing []string
	resources := make(map[string]routingRuleResource, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return routingRuleSnapshot{}, err
		}
		if _, exists := index[id]; !exists {
			return routingRuleSnapshot{}, fmt.Errorf("未知分流规则: %s", id)
		}
		doc := snapshot.Rules[id]
		if len(doc.Entries) == 0 {
			missing = append(missing, id+"（未同步）")
			continue
		}
		resource, resourceErr := s.routingPublishedResource(snapshot.Revision, id)
		if resourceErr != nil {
			missing = append(missing, id+"（"+resourceErr.Error()+"）")
			continue
		}
		if resource.SHA256 != doc.SHA256 || resource.Count != len(doc.Entries) {
			missing = append(missing, id+"（原始资源与快照不一致）")
			continue
		}
		resources[id] = resource
	}
	if len(missing) > 0 {
		return routingRuleSnapshot{}, fmt.Errorf("本地规则资源未就绪：%s。请先同步 MetaCubeX 本地规则，不会自动切换到上游", strings.Join(missing, "、"))
	}
	result := routingPublicSnapshot(state, snapshot, ids)
	result.Resources = resources
	if result.Stale && result.LastError == "" {
		result.LastError = "已发布本地规则超过检查周期；本次纯本地构建没有访问上游"
	}
	return result, nil
}

func (s *server) routingRuleResourceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	revision, file := r.PathValue("revision"), r.PathValue("file")
	id := strings.TrimSuffix(file, ".yaml")
	if file != id+".yaml" || !routingRevisionPattern.MatchString(revision) {
		http.NotFound(w, r)
		return
	}
	if _, ok := routingRuleIndex()[id]; !ok {
		http.NotFound(w, r)
		return
	}
	manifest, manifestErr := s.readRoutingResourceManifest(revision)
	if os.IsNotExist(manifestErr) {
		http.NotFound(w, r)
		return
	}
	if manifestErr == nil {
		if _, exists := manifest.Resources[id]; !exists {
			http.NotFound(w, r)
			return
		}
	}
	resource, err := s.routingPublishedResource(revision, id)
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "rule resource missing or failed integrity verification", 503)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.yaml"`)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+resource.SHA256+`"`)
	w.Header().Set("X-Content-SHA256", resource.SHA256)
	modified, _ := time.Parse(time.RFC3339Nano, resource.DownloadedAt)
	http.ServeContent(w, r, file, modified, bytes.NewReader(resource.Content))
}

func (s *server) routingUpstreamPath(parts ...string) string {
	return filepath.Join(append([]string{s.routingRulesDir(), "upstream"}, parts...)...)
}

func (s *server) readRoutingUpstreamStatus() routingUpstreamStatus {
	var status routingUpstreamStatus
	content, err := routingReadBoundedFile(s.routingUpstreamPath("state.json"), 16384)
	if err == nil && json.Unmarshal(content, &status) != nil {
		status.LastError = "上游检查状态损坏"
	}
	if status.Revision != "" && !routingRevisionPattern.MatchString(status.Revision) {
		return routingUpstreamStatus{LastError: "上游检查版本无效"}
	}
	return status
}

func (s *server) checkRoutingUpstream(ctx context.Context, force bool) (routingUpstreamStatus, error) {
	s.routingUpstreamMu.Lock()
	defer s.routingUpstreamMu.Unlock()
	return s.checkRoutingUpstreamLocked(ctx, force)
}

func (s *server) checkRoutingUpstreamLocked(ctx context.Context, force bool) (routingUpstreamStatus, error) {
	status := s.readRoutingUpstreamStatus()
	checked, _ := time.Parse(time.RFC3339Nano, status.CheckedAt)
	if !force && !checked.IsZero() && time.Since(checked) >= 0 && time.Since(checked) < routingRuleRetryInterval {
		if status.LastError != "" {
			return status, fmt.Errorf("上游版本检查失败: %s", status.LastError)
		}
		return status, nil
	}
	revision, err := s.resolveRoutingRevision(ctx)
	status.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	status.LastError = ""
	if err != nil {
		status.LastError = err.Error()
	} else {
		status.Revision = revision
	}
	content, _ := json.Marshal(status)
	if saveErr := routingRulesAtomicWrite(s.routingUpstreamPath("state.json"), content); saveErr != nil {
		return status, fmt.Errorf("上游检查状态保存失败: %w", saveErr)
	}
	return status, err
}

func (s *server) readRoutingUpstreamResourceStatus(revision, id string) routingUpstreamResourceStatus {
	var result routingUpstreamResourceStatus
	if !routingRevisionPattern.MatchString(revision) {
		return result
	}
	rule, ok := routingRuleIndex()[id]
	if !ok {
		return result
	}
	content, err := routingReadBoundedFile(s.routingUpstreamPath(revision, id+".json"), 16384)
	if err != nil || json.Unmarshal(content, &result) != nil {
		return routingUpstreamResourceStatus{}
	}
	resource := result.Resource
	if resource.SHA256 != "" && (resource.ID != id || resource.Revision != revision || resource.SourceURL != routingPinnedURL(rule, revision) || resource.Format != "yaml" || resource.Behavior != rule.Behavior || resource.Count < 1 || resource.Count > routingRuleMaxEntries || !routingHashPattern.MatchString(resource.SHA256) || resource.Bytes < 1 || resource.Bytes > routingRuleMaxBytes) {
		return routingUpstreamResourceStatus{LastError: "上游文件检查缓存无效"}
	}
	return result
}

func (s *server) routingLocalResourceHealth(snapshot routingDiskSnapshot, revision, id string) (routingRuleResource, string, error) {
	resource, err := s.routingPublishedResource(revision, id)
	doc := snapshot.Rules[id]
	if err == nil {
		if revision == snapshot.Revision && len(doc.Entries) == 0 {
			return resource, "missing", fmt.Errorf("当前快照未包含此规则，请修复本地快照")
		}
		if revision == snapshot.Revision && len(doc.Entries) > 0 && (resource.SHA256 != doc.SHA256 || resource.Count != len(doc.Entries)) {
			return resource, "corrupt", fmt.Errorf("原始资源与已发布快照不一致")
		}
		return resource, "verified", nil
	}
	if revision == snapshot.Revision && len(doc.Entries) > 0 && doc.RawBytes == 0 {
		return resource, "legacy_only", err
	}
	if !routingRevisionPattern.MatchString(revision) {
		return resource, "missing", err
	}
	if _, statErr := os.Stat(filepath.Join(s.routingRulesDir(), "releases", revision, "resources.json")); os.IsNotExist(statErr) {
		return resource, "missing", err
	}
	manifest, manifestErr := s.readRoutingResourceManifest(revision)
	if manifestErr == nil {
		if _, ok := manifest.Resources[id]; !ok {
			return resource, "missing", err
		}
		if _, statErr := os.Stat(s.routingRawPath(revision, id)); os.IsNotExist(statErr) {
			return resource, "missing", err
		}
	}
	return resource, "corrupt", err
}

// This is a pure cache read. Metadata alone is not evidence that the inspected
// upstream bytes still exist or retain the checksum used by a comparison.
func (s *server) routingUpstreamResourceHealth(revision, id string) (routingUpstreamResourceStatus, string, error) {
	status := s.readRoutingUpstreamResourceStatus(revision, id)
	if status.Resource.SHA256 == "" {
		if status.LastError != "" {
			return status, "error", fmt.Errorf("%s", status.LastError)
		}
		return status, "unchecked", nil
	}
	content, err := routingReadBoundedFile(s.routingUpstreamPath(revision, id+".yaml"), routingRuleMaxBytes)
	if err != nil || int64(len(content)) != status.Resource.Bytes || routingSHA256(content) != status.Resource.SHA256 {
		return status, "corrupt", fmt.Errorf("上游检查缓存原始文件缺失或摘要不一致，请重新查看上游详情")
	}
	return status, "verified", nil
}

func (s *server) inspectRoutingUpstreamRule(ctx context.Context, id, revision string) (routingRuleResource, []string, routingUpstreamResourceStatus, error) {
	s.routingUpstreamMu.Lock()
	defer s.routingUpstreamMu.Unlock()
	rule, exists := routingRuleIndex()[id]
	if !exists {
		return routingRuleResource{}, nil, routingUpstreamResourceStatus{}, fmt.Errorf("未知规则")
	}
	if revision == "" {
		status, err := s.checkRoutingUpstreamLocked(ctx, false)
		if err != nil {
			return routingRuleResource{}, nil, routingUpstreamResourceStatus{}, err
		}
		revision = status.Revision
	}
	if !routingRevisionPattern.MatchString(revision) {
		return routingRuleResource{}, nil, routingUpstreamResourceStatus{}, fmt.Errorf("上游文件版本须为固定提交 SHA")
	}
	status := s.readRoutingUpstreamResourceStatus(revision, id)
	checked, _ := time.Parse(time.RFC3339Nano, status.CheckedAt)
	fresh := !checked.IsZero() && time.Since(checked) >= 0 && time.Since(checked) < routingRuleRetryInterval
	readCached := func() ([]byte, []string, error) {
		content, err := routingReadBoundedFile(s.routingUpstreamPath(revision, id+".yaml"), routingRuleMaxBytes)
		if err != nil || int64(len(content)) != status.Resource.Bytes || routingSHA256(content) != status.Resource.SHA256 {
			return nil, nil, fmt.Errorf("上游检查缓存没有有效内容")
		}
		entries, err := parseRoutingRuleYAML(rule, content)
		return content, entries, err
	}
	if fresh {
		content, entries, err := readCached()
		if err == nil {
			resource := status.Resource
			resource.Content = content
			return resource, entries, status, nil
		}
		if status.LastError != "" {
			return routingRuleResource{}, nil, status, fmt.Errorf("上游文件检查失败: %s", status.LastError)
		}
	}
	content, err := s.routingSourceFetch(ctx, routingPinnedURL(rule, revision), routingRuleMaxBytes)
	var entries []string
	if err == nil {
		entries, err = parseRoutingRuleYAML(rule, content)
	}
	status.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	status.LastError = ""
	if err == nil {
		resource := routingRuleResource{ID: id, Revision: revision, Behavior: rule.Behavior, Format: "yaml", Count: len(entries), Bytes: int64(len(content)), SHA256: routingSHA256(content), SourceURL: routingPinnedURL(rule, revision), DownloadedAt: status.CheckedAt}
		if status.Resource.SHA256 != "" && status.Resource.SHA256 != resource.SHA256 {
			err = fmt.Errorf("固定提交的上游原始文件摘要改变")
		} else if saveErr := routingRulesAtomicWrite(s.routingUpstreamPath(revision, id+".yaml"), content); saveErr != nil {
			err = saveErr
		} else {
			status.Resource = resource
		}
	}
	if err != nil {
		status.LastError = err.Error()
	}
	metadata, _ := json.Marshal(status)
	if saveErr := routingRulesAtomicWrite(s.routingUpstreamPath(revision, id+".json"), metadata); saveErr != nil {
		return routingRuleResource{}, nil, status, fmt.Errorf("上游文件检查状态保存失败: %w", saveErr)
	}
	if err != nil {
		cached, cachedEntries, cachedErr := readCached()
		if cachedErr != nil {
			return routingRuleResource{}, nil, status, err
		}
		resource := status.Resource
		resource.Content = cached
		return resource, cachedEntries, status, nil
	}
	resource := status.Resource
	resource.Content = content
	return resource, entries, status, nil
}

func (s *server) routingRuleCheckHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, 405, map[string]string{"error": "仅支持 POST"})
		return
	}
	_, err := s.checkRoutingUpstream(r.Context(), true)
	result := s.routingCatalogResponse()
	status := http.StatusOK
	if err != nil {
		result["error"] = err.Error()
		status = http.StatusBadGateway
	}
	result["ok"] = err == nil
	writeJSON(w, status, result)
}

func routingDetailsPagination(r *http.Request) (string, int, int, error) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if len(query) > 512 {
		return "", 0, 0, fmt.Errorf("搜索条件过长")
	}
	page, pageSize := 1, 200
	var err error
	if value := r.URL.Query().Get("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil || page < 1 || page > 1000000 {
			return "", 0, 0, fmt.Errorf("页码无效")
		}
	}
	if value := r.URL.Query().Get("page_size"); value != "" {
		pageSize, err = strconv.Atoi(value)
		if err != nil || pageSize < 1 || pageSize > 2000 {
			return "", 0, 0, fmt.Errorf("每页条数须为 1 到 2000")
		}
	} else if value := r.URL.Query().Get("limit"); value != "" { // Older detail clients.
		pageSize, err = strconv.Atoi(value)
		if err != nil || pageSize < 1 || pageSize > 10000 {
			return "", 0, 0, fmt.Errorf("预览条数须为 1 到 10000")
		}
	}
	return query, page, pageSize, nil
}

func routingDetailsPage(entries []string, query string, page, pageSize int) ([]string, int) {
	result := make([]string, 0, pageSize)
	start := (page - 1) * pageSize
	count := 0
	for _, entry := range entries {
		if query != "" && !strings.Contains(strings.ToLower(entry), query) {
			continue
		}
		if count >= start && len(result) < pageSize {
			result = append(result, entry)
		}
		count++
	}
	return result, count
}

func (s *server) routingRuleDetailsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, 405, map[string]string{"error": "仅支持 GET"})
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	id, source := r.URL.Query().Get("id"), r.URL.Query().Get("source")
	if source == "" {
		source = "local"
	}
	rule, ok := routingRuleIndex()[id]
	query, page, pageSize, pageErr := routingDetailsPagination(r)
	if !ok || source != "local" && source != "upstream" || pageErr != nil {
		message := "未知规则或详情来源"
		if pageErr != nil {
			message = pageErr.Error()
		}
		writeJSON(w, 400, map[string]string{"error": message})
		return
	}
	requestedRevision := r.URL.Query().Get("revision")
	if requestedRevision != "" && !routingRevisionPattern.MatchString(requestedRevision) {
		writeJSON(w, 400, map[string]string{"error": "版本须为固定提交 SHA"})
		return
	}
	state, snapshot, readErr := s.readRoutingRuleState()
	var resource routingRuleResource
	var entries []string
	lastError, checkedAt := state.LastError, state.CheckedAt
	localError := ""
	localHealth := "missing"
	mirrored := false
	if source == "upstream" {
		var inspected routingUpstreamResourceStatus
		var err error
		resource, entries, inspected, err = s.inspectRoutingUpstreamRule(r.Context(), id, requestedRevision)
		if err != nil {
			writeJSON(w, 502, map[string]any{"error": err.Error(), "source": source, "id": id, "checked_at": inspected.CheckedAt, "last_error": inspected.LastError, "verified": false, "health": "error", "can_compare": false})
			return
		}
		lastError, checkedAt = inspected.LastError, inspected.CheckedAt
	} else {
		if readErr != nil && requestedRevision == "" {
			writeJSON(w, 503, map[string]string{"error": readErr.Error()})
			return
		}
		revision := requestedRevision
		if revision == "" {
			revision = snapshot.Revision
		}
		var err error
		resource, localHealth, err = s.routingLocalResourceHealth(snapshot, revision, id)
		if err == nil {
			entries, err = parseRoutingRuleYAML(rule, resource.Content)
			if err != nil {
				writeJSON(w, 503, map[string]string{"error": "已发布原始规则解析失败"})
				return
			}
			mirrored = true
		} else if revision == snapshot.Revision && len(snapshot.Rules[id].Entries) > 0 {
			doc := snapshot.Rules[id]
			resource = routingResourceFromDocument(rule, revision, doc)
			entries = doc.Entries
			localError = err.Error()
		} else {
			writeJSON(w, 404, map[string]any{"error": "本地规则尚未同步，请显式同步 MetaCubeX 本地规则", "verified": false, "health": localHealth, "can_compare": false, "local_url": s.routingRawResourceURL(revision, id), "revision": revision})
			return
		}
	}
	shown, count := routingDetailsPage(entries, query, page, pageSize)
	upstream := s.readRoutingUpstreamStatus()
	upstreamRevision := upstream.Revision
	if source == "upstream" {
		upstreamRevision = resource.Revision
	}
	upstreamFile, upstreamHealth, upstreamErr := s.routingUpstreamResourceHealth(upstreamRevision, id)
	localDoc := snapshot.Rules[id]
	localRevision, localSHA := snapshot.Revision, localDoc.SHA256
	localVerified := mirrored
	if source == "local" {
		localRevision, localSHA = resource.Revision, resource.SHA256
	} else {
		_, localHealth, _ = s.routingLocalResourceHealth(snapshot, snapshot.Revision, id)
		localVerified = localHealth == "verified"
	}
	upstreamVerified := upstreamHealth == "verified"
	hashComparable := localVerified && upstreamVerified && localSHA != "" && upstreamFile.Resource.SHA256 != ""
	verified, health := localVerified, localHealth
	if source == "upstream" {
		verified, health = upstreamVerified, upstreamHealth
	}
	upstreamError := upstreamFile.LastError
	if upstreamErr != nil {
		upstreamError = upstreamErr.Error()
	}
	updatedAt := resource.DownloadedAt
	if updatedAt == "" && source == "local" && resource.Revision == snapshot.Revision {
		updatedAt = snapshot.UpdatedAt
	}
	result := map[string]any{
		"id": id, "name": rule.Name, "source": source, "revision": resource.Revision,
		"behavior": resource.Behavior, "format": resource.Format, "entries": shown,
		"count": count, "total_count": len(entries), "page": page, "page_size": pageSize, "q": query,
		"truncated": (page-1)*pageSize+len(shown) < count, "sha256": resource.SHA256, "bytes": resource.Bytes,
		"source_url": resource.SourceURL, "local_url": s.routingRawResourceURL(resource.Revision, id), "updated_at": updatedAt,
		"checked_at": checkedAt, "last_error": lastError, "stale": lastError != "", "mirrored": mirrored,
		"local_error": localError, "local_revision": localRevision, "upstream_revision": upstreamRevision, "local_mirrored": localVerified,
		"hash_comparable": hashComparable, "hash_equal": hashComparable && localSHA == upstreamFile.Resource.SHA256,
		"verified": verified, "health": health, "can_compare": hashComparable,
		"local_verified": localVerified, "local_health": localHealth, "upstream_verified": upstreamVerified, "upstream_health": upstreamHealth, "upstream_error": upstreamError,
		"local_sha256": localSHA, "upstream_sha256": upstreamFile.Resource.SHA256,
	}
	writeJSON(w, 200, result)
}

func (s *server) routingResourceCatalogResponse() map[string]any {
	state, snapshot, err := s.readRoutingRuleState()
	publishedRevision := snapshot.Revision
	if publishedRevision == "" && routingRevisionPattern.MatchString(state.Revision) {
		publishedRevision = state.Revision
	}
	if err != nil {
		state.LastError = err.Error()
	}
	upstream := s.readRoutingUpstreamStatus()
	items := make([]map[string]any, 0, len(routingRuleCatalog()))
	mirrored, cached := 0, 0
	healthCounts := map[string]int{}
	var rawBytes int64
	for _, rule := range routingRuleCatalog() {
		encoded, _ := json.Marshal(rule)
		item := map[string]any{}
		_ = json.Unmarshal(encoded, &item)
		doc := snapshot.Rules[rule.ID]
		item["cached"], item["count"], item["sha256"] = len(doc.Entries) > 0, len(doc.Entries), doc.SHA256
		item["mirrored"], item["local_url"], item["bytes"] = false, s.routingRawResourceURL(publishedRevision, rule.ID), int64(0)
		pinnedURL := doc.SourceURL
		if pinnedURL == "" && routingRevisionPattern.MatchString(publishedRevision) {
			pinnedURL = routingPinnedURL(rule, publishedRevision)
		}
		item["pinned_source_url"], item["downloaded_at"] = pinnedURL, doc.DownloadedAt
		resource, health, readErr := s.routingLocalResourceHealth(snapshot, snapshot.Revision, rule.ID)
		if err != nil {
			health, readErr = "corrupt", err
		}
		item["health"], item["verified"] = health, health == "verified"
		healthCounts[health]++
		if len(doc.Entries) > 0 {
			cached++
			if readErr == nil {
				mirrored++
				rawBytes += resource.Bytes
				item["mirrored"], item["local_url"], item["bytes"] = true, resource.LocalURL, resource.Bytes
				item["local_error"] = ""
			} else {
				message := "原始资源与已发布快照不一致"
				if readErr != nil {
					message = readErr.Error()
				}
				item["local_error"] = message
			}
		} else {
			item["local_error"] = "本地副本缺失"
		}
		checkRevision := upstream.Revision
		if checkRevision == "" {
			checkRevision = snapshot.Revision
		}
		checked, upstreamHealth, upstreamErr := s.routingUpstreamResourceHealth(checkRevision, rule.ID)
		item["upstream_revision"], item["upstream_checked_at"], item["upstream_error"] = checked.Resource.Revision, checked.CheckedAt, checked.LastError
		if upstreamErr != nil {
			item["upstream_error"] = upstreamErr.Error()
		}
		item["upstream_verified"], item["upstream_health"] = upstreamHealth == "verified", upstreamHealth
		item["upstream_bytes"], item["upstream_sha256"] = checked.Resource.Bytes, checked.Resource.SHA256
		item["hash_comparable"] = item["mirrored"] == true && upstreamHealth == "verified" && doc.SHA256 != "" && checked.Resource.SHA256 != ""
		item["can_compare"] = item["hash_comparable"]
		item["hash_equal"] = item["hash_comparable"] == true && doc.SHA256 == checked.Resource.SHA256
		items = append(items, item)
	}
	health := "missing"
	if err != nil || healthCounts["corrupt"] > 0 {
		health = "corrupt"
	} else if healthCounts["legacy_only"] > 0 {
		health = "legacy_only"
	} else if mirrored == len(items) {
		health = "verified"
	}
	result := map[string]any{"rules": items, "total": len(items), "cached_count": cached, "mirrored_count": mirrored,
		"revision": publishedRevision, "updated_at": snapshot.UpdatedAt, "checked_at": state.CheckedAt, "last_error": state.LastError,
		"stale": routingPublicSnapshot(state, snapshot, nil).Stale, "upstream_revision": upstream.Revision,
		"upstream_checked_at": upstream.CheckedAt, "upstream_error": upstream.LastError,
		"update_available": upstream.Revision != "" && snapshot.Revision != "" && upstream.Revision != snapshot.Revision,
		"raw_bytes":        rawBytes, "complete": mirrored == len(items), "verified": mirrored == len(items), "health": health, "health_counts": healthCounts,
		"repository": "https://github.com/MetaCubeX/meta-rules-dat", "license_url": "https://github.com/MetaCubeX/meta-rules-dat/blob/master/LICENSE"}
	for key, value := range s.routingDiskUsage() {
		result[key] = value
	}
	return result
}

func (s *server) routingDiskUsage() map[string]any {
	var bytes int64
	files := 0
	warnings := []string{}
	err := filepath.WalkDir(s.routingRulesDir(), func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			bytes += info.Size()
			files++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		warnings = append(warnings, "部分规则文件的磁盘占用无法统计")
	}
	if bytes >= 1<<30 {
		warnings = append(warnings, "规则历史和检查缓存已超过 1 GiB；请检查磁盘容量，系统不会自动删除已交付版本")
	}
	result := map[string]any{"disk_bytes": bytes, "disk_files": files, "disk_warning_threshold_bytes": int64(1 << 30), "auto_cleanup": false}
	if free, total, err := routingFilesystemSpace(s.dataDir); err == nil {
		result["disk_free_bytes"], result["disk_capacity_bytes"] = free, total
		if free < 1<<30 || total > 0 && free < total/10 {
			warnings = append(warnings, "数据磁盘剩余空间低于 1 GiB 或 10%，请及时扩容或人工检查")
		}
	} else {
		warnings = append(warnings, "数据磁盘容量暂时无法读取")
	}
	result["disk_warning"], result["disk_warnings"] = strings.Join(warnings, "；"), warnings
	return result
}

func (s *server) syncRoutingRules(ctx context.Context) (routingRuleSnapshot, error) {
	ids := make([]string, 0, len(routingRuleCatalog()))
	for _, rule := range routingRuleCatalog() {
		ids = append(ids, rule.ID)
	}
	return s.loadRoutingRuleSnapshotMode(ctx, ids, true, true)
}

// This loop only maintains MetaCubeX. It neither runs sync.sh nor shares any of
// the legacy schedule/rollback state. The first incomplete installation is filled
// on startup; complete installations retain the 24-hour cadence across restarts.
func (s *server) routingRulesScheduler(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	delay := time.Duration(0)
	state, snapshot, err := s.readRoutingRuleState()
	if err == nil && state.LastError == "" && len(snapshot.Rules) == len(routingRuleCatalog()) {
		ids := make([]string, 0, len(snapshot.Rules))
		for id := range snapshot.Rules {
			ids = append(ids, id)
		}
		if _, err := s.loadPublishedRoutingRuleSnapshot(ctx, ids); err == nil {
			checked, _ := time.Parse(time.RFC3339Nano, state.CheckedAt)
			if !checked.IsZero() && time.Since(checked) >= 0 && time.Since(checked) < routingRuleCacheTTL {
				delay = routingRuleCacheTTL - time.Since(checked)
			}
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			snapshot, err := s.syncRoutingRules(ctx)
			delay = routingRuleCacheTTL
			if err != nil || snapshot.Stale {
				delay = routingRuleRetryInterval
			}
			timer.Reset(delay)
		}
	}
}

// A damaged manifest is recoverable only from snapshots whose file names still
// match their complete JSON hash. Do not infer original bytes from a damaged raw
// file or from the current contents of a moving upstream branch.
func (s *server) routingTrustedSnapshot(revision string) (routingDiskSnapshot, error) {
	result := routingDiskSnapshot{Revision: revision, Rules: map[string]routingRuleDocument{}}
	if !routingRevisionPattern.MatchString(revision) {
		return result, fmt.Errorf("版本无效")
	}
	dir := filepath.Join(s.routingRulesDir(), "releases", revision)
	files, err := os.ReadDir(dir)
	if err != nil {
		return result, err
	}
	var candidates []fs.FileInfo
	for _, file := range files {
		if !file.Type().IsRegular() || !routingHashPattern.MatchString(strings.TrimSuffix(file.Name(), ".json")) || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		info, err := file.Info()
		if err != nil {
			return result, err
		}
		candidates = append(candidates, info)
	}
	if len(candidates) == 0 {
		return result, os.ErrNotExist
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ModTime().After(candidates[j].ModTime()) })
	index := routingRuleIndex()
	var inspected int64
	for _, file := range candidates {
		inspected += file.Size()
		if inspected > 4*routingSnapshotMaxBytes {
			return result, fmt.Errorf("恢复快照读取超过 256 MiB 限制")
		}
		content, err := routingReadBoundedFile(filepath.Join(dir, file.Name()), routingSnapshotMaxBytes)
		var snapshot routingDiskSnapshot
		if err != nil || routingSHA256(content)+".json" != file.Name() || json.Unmarshal(content, &snapshot) != nil || snapshot.Revision != revision || len(snapshot.Rules) == 0 || len(snapshot.Rules) > len(index) {
			continue
		}
		valid := true
		for id, doc := range snapshot.Rules {
			rule, ok := index[id]
			if !ok || !routingHashPattern.MatchString(doc.SHA256) || doc.SourceURL != routingPinnedURL(rule, revision) || len(doc.Entries) < 1 || len(doc.Entries) > routingRuleMaxEntries || doc.RawBytes < 0 || doc.RawBytes > routingRuleMaxBytes {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		for id, doc := range snapshot.Rules {
			old, exists := result.Rules[id]
			if exists && old.SHA256 != doc.SHA256 {
				return result, fmt.Errorf("%s 的历史固定版本摘要冲突，不能自动修复", id)
			}
			if !exists || old.RawBytes == 0 && doc.RawBytes > 0 {
				result.Rules[id] = doc
			}
		}
		if snapshot.UpdatedAt > result.UpdatedAt {
			result.UpdatedAt = snapshot.UpdatedAt
		}
		if len(result.Rules) == len(index) {
			break
		}
	}
	if len(result.Rules) == 0 {
		return result, fmt.Errorf("没有通过内容摘要校验的本地快照，不能安全修复固定资源")
	}
	return result, nil
}

func (s *server) registerRoutingResourceMaintenanceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/routing/rules/repair", s.auth(s.routingRuleRepairHandler))
	mux.HandleFunc("GET /api/routing/rules/versions", s.auth(s.routingRuleVersionsHandler))
}

// Repair uses an explicitly known immutable commit. It intentionally never
// checks the branch API and never moves the current pointer to another commit.
func (s *server) repairRoutingRuleResources(ctx context.Context, revision string) (routingRuleSnapshot, error) {
	s.routingRuleMu.Lock()
	defer s.routingRuleMu.Unlock()
	if err := ctx.Err(); err != nil {
		return routingRuleSnapshot{}, err
	}
	state, current, readErr := s.readRoutingRuleState()
	if revision == "" {
		if readErr != nil {
			return routingRuleSnapshot{}, readErr
		}
		revision = current.Revision
	}
	if !routingRevisionPattern.MatchString(revision) {
		return routingRuleSnapshot{}, routingErrNoRepairVersion
	}
	trusted, err := s.routingTrustedSnapshot(revision)
	if err != nil {
		if os.IsNotExist(err) {
			return routingRuleSnapshot{}, fmt.Errorf("该版本没有可信恢复快照: %w", routingErrNoRepairVersion)
		}
		return routingRuleSnapshot{}, fmt.Errorf("该版本没有可信恢复快照: %w", err)
	}
	manifest, manifestErr := s.readRoutingResourceManifest(revision)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	candidate := routingDiskSnapshot{Revision: revision, UpdatedAt: now, Rules: map[string]routingRuleDocument{}}
	rawFiles := map[string][]byte{}
	var rawBytes int64
	ids := make([]string, 0, len(routingRuleCatalog()))
	for _, rule := range routingRuleCatalog() {
		if err := ctx.Err(); err != nil {
			return routingRuleSnapshot{}, err
		}
		ids = append(ids, rule.ID)
		old := trusted.Rules[rule.ID]
		expected := old.SHA256
		if meta, exists := manifest.Resources[rule.ID]; manifestErr == nil && exists {
			if expected != "" && expected != meta.SHA256 {
				return routingRuleSnapshot{}, fmt.Errorf("%s 的快照与已发布摘要冲突，拒绝修复", rule.ID)
			}
			expected = meta.SHA256
		}
		raw, rawErr := routingReadBoundedFile(s.routingRawPath(revision, rule.ID), routingRuleMaxBytes)
		if expected == "" || rawErr != nil || routingSHA256(raw) != expected {
			raw, err = s.routingSourceFetch(ctx, routingPinnedURL(rule, revision), routingRuleMaxBytes)
			if err != nil {
				return routingRuleSnapshot{}, fmt.Errorf("%s 修复下载失败: %w", rule.Name, err)
			}
			if expected != "" && routingSHA256(raw) != expected {
				return routingRuleSnapshot{}, fmt.Errorf("%s 与历史原始摘要不一致，拒绝改写固定地址", rule.Name)
			}
			rawFiles[rule.ID] = raw
		}
		entries, err := parseRoutingRuleYAML(rule, raw)
		if err != nil {
			return routingRuleSnapshot{}, fmt.Errorf("%s 修复内容无效: %w", rule.Name, err)
		}
		if old.SHA256 != "" && !reflect.DeepEqual(old.Entries, entries) {
			return routingRuleSnapshot{}, fmt.Errorf("%s 原始文件与可信快照条目不一致，拒绝修复", rule.Name)
		}
		rawBytes += int64(len(raw))
		if rawBytes > routingRawSnapshotMaxBytes {
			return routingRuleSnapshot{}, fmt.Errorf("原始规则候选总量超过 64 MiB 限制")
		}
		downloaded := old.DownloadedAt
		if downloaded == "" {
			downloaded = now
		}
		candidate.Rules[rule.ID] = routingRuleDocument{Entries: entries, SHA256: routingSHA256(raw), SourceURL: routingPinnedURL(rule, revision), RawBytes: int64(len(raw)), DownloadedAt: downloaded}
	}
	var created []string
	published := false
	defer func() {
		if !published {
			s.cleanupRoutingCandidatePaths(revision, created)
		}
	}()
	for id, raw := range rawFiles {
		path := s.routingRawPath(revision, id)
		if _, err := os.Stat(path); os.IsNotExist(err) && !s.routingPreviouslyKnownResource(revision, id) {
			created = append(created, path)
		}
		if err := s.writeRoutingRawDocument(revision, id, raw); err != nil {
			return routingRuleSnapshot{}, err
		}
	}
	base := trusted
	if revision == current.Revision {
		base = current
	}
	if reflect.DeepEqual(candidate.Rules, base.Rules) {
		candidate.UpdatedAt = base.UpdatedAt
	}
	content, err := json.Marshal(candidate)
	if err != nil || len(content) > routingSnapshotMaxBytes {
		return routingRuleSnapshot{}, fmt.Errorf("修复快照超过大小限制")
	}
	hash := routingSHA256(content)
	path := filepath.Join(s.routingRulesDir(), "releases", revision, hash+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		created = append(created, path)
	}
	if err := routingRulesAtomicWrite(path, content); err != nil {
		return routingRuleSnapshot{}, err
	}
	if err := s.publishRoutingResourceManifest(candidate); err != nil {
		return routingRuleSnapshot{}, err
	}
	published = true
	if revision == current.Revision {
		state.Snapshot, state.UpdatedAt = hash, candidate.UpdatedAt
		if err := s.saveRoutingRuleState(state); err != nil {
			return routingRuleSnapshot{}, err
		}
	}
	return routingPublicSnapshot(state, candidate, ids), nil
}

func (s *server) routingRuleRepairHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "仅支持 POST"})
		return
	}
	var request struct {
		Revision string `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&request)
	if err != nil && err != io.EOF || request.Revision != "" && !routingRevisionPattern.MatchString(request.Revision) {
		writeJSON(w, 400, map[string]string{"error": "修复参数无效，revision 须为固定提交 SHA"})
		return
	}
	if err == nil && decoder.Decode(new(any)) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "只允许单个 JSON 请求"})
		return
	}
	snapshot, err := s.repairRoutingRuleResources(r.Context(), request.Revision)
	result := s.routingCatalogResponse()
	result["ok"], result["repaired_revision"] = err == nil, snapshot.Revision
	result["upstream_checked"] = false
	status := 200
	if err != nil {
		status = 502
		if errors.Is(err, routingErrNoRepairVersion) {
			status = 409
		}
		result["error"] = err.Error()
	}
	writeJSON(w, status, result)
}

func (s *server) routingRuleVersionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]string{"error": "仅支持 GET"})
		return
	}
	_, page, size, err := routingDetailsPagination(r)
	if err != nil || size > 50 && r.URL.Query().Get("page_size") != "" {
		writeJSON(w, 400, map[string]string{"error": "版本分页参数无效，每页最多 50 项"})
		return
	}
	if size > 50 {
		size = 20
	}
	state, _, _ := s.readRoutingRuleState()
	files, err := os.ReadDir(filepath.Join(s.routingRulesDir(), "releases"))
	if err != nil && !os.IsNotExist(err) {
		writeJSON(w, 503, map[string]string{"error": "版本目录读取失败"})
		return
	}
	var revisions []string
	for _, file := range files {
		if file.IsDir() && routingRevisionPattern.MatchString(file.Name()) {
			revisions = append(revisions, file.Name())
		}
	}
	sort.Strings(revisions)
	start := min((page-1)*size, len(revisions))
	end := min(start+size, len(revisions))
	items := make([]map[string]any, 0, end-start)
	for _, revision := range revisions[start:end] {
		manifest, err := s.readRoutingResourceManifest(revision)
		item := map[string]any{"revision": revision, "current": revision == state.Revision, "manifest_valid": err == nil, "resource_count": len(manifest.Resources), "total": len(routingRuleCatalog()), "updated_at": manifest.UpdatedAt, "verified": false, "health": "unchecked"}
		if err != nil {
			health := "corrupt"
			if os.IsNotExist(err) {
				health = "missing"
			}
			item["health"], item["error"] = health, err.Error()
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"versions": items, "count": len(revisions), "page": page, "page_size": size, "current_revision": state.Revision, "note": "历史列表仅读取清单元信息；原始文件完整性由详情或显式修复验证。不会自动删除已交付版本。"})
}
