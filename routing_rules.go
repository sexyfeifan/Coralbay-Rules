package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	routingBootstrapRevision   = "8964c30fc18a52dfc6761d30c664faaac0c219b1"
	routingBranchURL           = "https://api.github.com/repos/MetaCubeX/meta-rules-dat/git/ref/heads/meta"
	routingRuleMaxBytes        = 12 << 20
	routingSnapshotMaxBytes    = 64 << 20
	routingRawSnapshotMaxBytes = 64 << 20
	routingRuleMaxEntries      = 250000
	routingRuleCacheTTL        = 24 * time.Hour
	routingRuleRetryInterval   = 5 * time.Minute
)

// Only this reviewed catalog is accepted. No request may add arbitrary rule
// URLs, and these sources never enter the legacy 666OS synchronization path.
//
//go:embed routing_catalog.json
var routingCatalogJSON []byte

type routingRule struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Family        string `json:"family"`
	File          string `json:"file"`
	Behavior      string `json:"behavior"`
	DefaultAction string `json:"default_action"`
	Priority      int    `json:"priority"`
	Recommended   bool   `json:"recommended"`
	SourceURL     string `json:"source_url"`
}

type routingRuleSnapshot struct {
	Revision  string                         `json:"revision"`
	Rules     map[string][]string            `json:"rules"`
	UpdatedAt string                         `json:"updated_at"`
	LastError string                         `json:"last_error"`
	Stale     bool                           `json:"stale"`
	Resources map[string]routingRuleResource `json:"-"`
}

type routingRuleDocument struct {
	Entries      []string `json:"entries"`
	SHA256       string   `json:"sha256"` // Original upstream YAML, before normalization.
	SourceURL    string   `json:"source_url"`
	RawBytes     int64    `json:"raw_bytes,omitempty"`
	DownloadedAt string   `json:"downloaded_at,omitempty"`
}

type routingDiskSnapshot struct {
	Revision  string                         `json:"revision"`
	UpdatedAt string                         `json:"updated_at"`
	Rules     map[string]routingRuleDocument `json:"rules"`
}

type routingRuleState struct {
	Revision  string `json:"revision"`
	Snapshot  string `json:"snapshot"` // SHA-256 of the immutable JSON snapshot.
	UpdatedAt string `json:"updated_at"`
	CheckedAt string `json:"checked_at"`
	LastError string `json:"last_error"`
}

var routingRevisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var routingHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func routingRuleCatalog() []routingRule {
	var rules []routingRule
	// Invalid embedded data is a programming error, not a recoverable network
	// problem; fail before an incomplete catalog can be offered to users.
	if err := json.Unmarshal(routingCatalogJSON, &rules); err != nil {
		panic("invalid embedded routing catalog: " + err.Error())
	}
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].Priority < rules[j].Priority })
	return rules
}

func routingRuleIndex() map[string]routingRule {
	index := make(map[string]routingRule)
	for _, rule := range routingRuleCatalog() {
		index[rule.ID] = rule
	}
	return index
}

func routingPinnedURL(rule routingRule, revision string) string {
	return strings.Replace(rule.SourceURL, "/meta/geo/", "/"+revision+"/geo/", 1)
}

func (s *server) routingRulesDir() string {
	return filepath.Join(s.dataDir, "routing", "rules")
}

func routingSHA256(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// Atomic rename is the publication point. Snapshot files are content addressed
// and immutable; state.json can therefore never point at half-written data.
func routingRulesAtomicWrite(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".routing-next-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func routingReadBoundedFile(path string, maximum int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err == nil && int64(len(content)) > maximum {
		err = fmt.Errorf("规则缓存超过大小限制")
	}
	return content, err
}

func (s *server) readRoutingRuleState() (routingRuleState, routingDiskSnapshot, error) {
	var state routingRuleState
	var snapshot routingDiskSnapshot
	content, err := routingReadBoundedFile(filepath.Join(s.routingRulesDir(), "state.json"), 16384)
	if os.IsNotExist(err) {
		return state, snapshot, nil
	}
	if err != nil || json.Unmarshal(content, &state) != nil {
		return state, snapshot, fmt.Errorf("新分流规则状态读取失败")
	}
	if state.Revision == "" && state.Snapshot == "" {
		return state, snapshot, nil
	}
	if !routingRevisionPattern.MatchString(state.Revision) || !routingHashPattern.MatchString(state.Snapshot) {
		return state, snapshot, fmt.Errorf("新分流规则快照标识无效")
	}
	content, err = routingReadBoundedFile(filepath.Join(s.routingRulesDir(), "releases", state.Revision, state.Snapshot+".json"), routingSnapshotMaxBytes)
	if err != nil || routingSHA256(content) != state.Snapshot || json.Unmarshal(content, &snapshot) != nil {
		return state, routingDiskSnapshot{}, fmt.Errorf("新分流规则快照校验失败")
	}
	if snapshot.Revision != state.Revision || snapshot.UpdatedAt != state.UpdatedAt || len(snapshot.Rules) == 0 {
		return state, routingDiskSnapshot{}, fmt.Errorf("新分流规则快照版本不一致")
	}
	index := routingRuleIndex()
	for id, document := range snapshot.Rules {
		rule, exists := index[id]
		if !exists || document.SourceURL != routingPinnedURL(rule, state.Revision) || !routingHashPattern.MatchString(document.SHA256) || len(document.Entries) == 0 || len(document.Entries) > routingRuleMaxEntries {
			return state, routingDiskSnapshot{}, fmt.Errorf("新分流规则快照条目无效")
		}
	}
	return state, snapshot, nil
}

func (s *server) saveRoutingRuleState(state routingRuleState) error {
	content, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return routingRulesAtomicWrite(filepath.Join(s.routingRulesDir(), "state.json"), content)
}

func routingSnapshotContains(snapshot routingDiskSnapshot, ids []string) bool {
	if snapshot.Revision == "" {
		return false
	}
	for _, id := range ids {
		if len(snapshot.Rules[id].Entries) == 0 {
			return false
		}
	}
	return true
}

func routingPublicSnapshot(state routingRuleState, snapshot routingDiskSnapshot, ids []string) routingRuleSnapshot {
	result := routingRuleSnapshot{Revision: snapshot.Revision, UpdatedAt: snapshot.UpdatedAt, LastError: state.LastError, Rules: make(map[string][]string), Stale: state.LastError != ""}
	checked, err := time.Parse(time.RFC3339Nano, state.CheckedAt)
	result.Stale = result.Stale || err != nil || time.Since(checked) >= routingRuleCacheTTL
	for _, id := range ids {
		result.Rules[id] = append([]string(nil), snapshot.Rules[id].Entries...)
	}
	return result
}

func (s *server) routingSourceFetch(ctx context.Context, address string, maximum int64) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("不允许的规则源地址")
	}
	allowed := address == routingBranchURL
	if u.Host == "raw.githubusercontent.com" {
		for _, rule := range routingRuleCatalog() {
			parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
			if len(parts) >= 4 && routingRevisionPattern.MatchString(parts[2]) && address == routingPinnedURL(rule, parts[2]) {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return nil, fmt.Errorf("不允许的规则源地址")
	}
	base := s.routingHTTPClient
	if base == nil {
		base = safeHTTPClient(30 * time.Second)
	}
	client := *base
	// A reviewed URL cannot redirect to another repository, host, or revision.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return fmt.Errorf("规则源不允许重定向") }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "CoralBay-Routing/1")
	if address == routingBranchURL {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("规则源请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("规则源返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maximum {
		return nil, fmt.Errorf("规则源超过大小限制")
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("规则源读取失败: %w", err)
	}
	if int64(len(content)) > maximum {
		return nil, fmt.Errorf("规则源超过大小限制")
	}
	return content, nil
}

func (s *server) resolveRoutingRevision(ctx context.Context) (string, error) {
	content, err := s.routingSourceFetch(ctx, routingBranchURL, 16384)
	if err != nil {
		return "", err
	}
	var ref struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	if json.Unmarshal(content, &ref) != nil || ref.Ref != "refs/heads/meta" || ref.Object.Type != "commit" || !routingRevisionPattern.MatchString(ref.Object.SHA) {
		return "", fmt.Errorf("MetaCubeX 分支版本响应无效")
	}
	return ref.Object.SHA, nil
}

// Parse a strict, small YAML surface, without aliases, custom tags or arbitrary
// client options. Domain regexes are split only at the first comma: quantifiers
// such as {0,61} are part of the pattern and must remain intact.
func parseRoutingRuleYAML(rule routingRule, content []byte) ([]string, error) {
	if len(content) == 0 || len(content) > routingRuleMaxBytes {
		return nil, fmt.Errorf("规则内容为空或超过大小限制")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("规则 YAML 无效: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("规则只允许单个 YAML 文档")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("规则必须包含 payload 列表")
	}
	root := document.Content[0]
	if root.Tag != "!!map" || len(root.Content) != 2 || root.Content[0].Kind != yaml.ScalarNode || root.Content[0].Value != "payload" || root.Content[0].Tag != "!!str" {
		return nil, fmt.Errorf("规则只允许 payload 字段")
	}
	items := root.Content[1]
	if items.Kind != yaml.SequenceNode || items.Tag != "!!seq" || len(items.Content) == 0 || len(items.Content) > routingRuleMaxEntries {
		return nil, fmt.Errorf("规则 payload 列表为空或超过条目限制")
	}
	entries := make([]string, 0, len(items.Content))
	seen := make(map[string]bool, len(items.Content))
	for i, item := range items.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || item.Anchor != "" || len(item.Value) > 8192 || strings.IndexFunc(item.Value, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("第 %d 条规则不是有效文本", i+1)
		}
		entry := strings.TrimSpace(item.Value)
		if rule.Family == "geoip" {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil || prefix.Addr().Is4In6() {
				return nil, fmt.Errorf("第 %d 条 IP 规则不是有效 CIDR", i+1)
			}
			kind := "IP-CIDR6"
			if prefix.Addr().Is4() {
				kind = "IP-CIDR"
			}
			entry = kind + "," + prefix.Masked().String() + ",no-resolve"
		} else if rule.Family == "geosite" {
			kind, value, ok := strings.Cut(entry, ",")
			if !ok || value == "" || value != strings.TrimSpace(value) {
				return nil, fmt.Errorf("第 %d 条域名规则格式无效", i+1)
			}
			switch kind {
			case "DOMAIN", "DOMAIN-SUFFIX":
				if len(value) > 253 || strings.IndexFunc(value, func(r rune) bool {
					return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_')
				}) >= 0 || strings.Contains(value, "..") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
					return nil, fmt.Errorf("第 %d 条域名规则格式无效", i+1)
				}
			case "DOMAIN-KEYWORD":
				if strings.Contains(value, ",") || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
					return nil, fmt.Errorf("第 %d 条关键词规则格式无效", i+1)
				}
			case "DOMAIN-REGEX":
				if _, err := regexp.Compile(value); err != nil {
					return nil, fmt.Errorf("第 %d 条域名正则不受支持: %w", i+1, err)
				}
			default:
				return nil, fmt.Errorf("第 %d 条规则类型 %q 不受支持", i+1, kind)
			}
		} else {
			return nil, fmt.Errorf("规则来源类型无效")
		}
		if !seen[entry] {
			seen[entry] = true
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// The mutex covers both synchronization and publication. Every build receives
// one complete commit, and a failed candidate never changes the active pointer.
func (s *server) loadRoutingRuleSnapshot(ctx context.Context, ids []string, force bool) (routingRuleSnapshot, error) {
	return s.loadRoutingRuleSnapshotMode(ctx, ids, force, false)
}

func (s *server) loadRoutingRuleSnapshotMode(ctx context.Context, ids []string, force, requireRaw bool) (routingRuleSnapshot, error) {
	s.routingRuleMu.Lock()
	defer s.routingRuleMu.Unlock()
	if err := ctx.Err(); err != nil {
		return routingRuleSnapshot{}, err
	}
	index := routingRuleIndex()
	requested := make(map[string]bool)
	for _, id := range ids {
		if _, ok := index[id]; !ok {
			return routingRuleSnapshot{}, fmt.Errorf("未知分流规则: %s", id)
		}
		requested[id] = true
	}
	if len(ids) == 0 {
		return routingRuleSnapshot{Rules: map[string][]string{}}, nil
	}
	state, previous, readErr := s.readRoutingRuleState()
	if readErr != nil {
		// Keep corrupt files for diagnosis; a successfully fetched candidate may
		// repair the pointer without reading or modifying any legacy directory.
		state = routingRuleState{}
		previous = routingDiskSnapshot{}
	}
	now := time.Now().UTC()
	checked, _ := time.Parse(time.RFC3339Nano, state.CheckedAt)
	ttl := routingRuleCacheTTL
	if state.LastError != "" {
		ttl = routingRuleRetryInterval
	}
	fresh := !checked.IsZero() && !checked.After(now.Add(time.Minute)) && now.Sub(checked) < ttl
	if !force && fresh && routingSnapshotContains(previous, ids) && !requireRaw {
		return routingPublicSnapshot(state, previous, ids), nil
	}
	failure := func(err error) (routingRuleSnapshot, error) {
		state.LastError = err.Error()
		state.CheckedAt = now.Format(time.RFC3339Nano)
		if saveErr := s.saveRoutingRuleState(state); saveErr != nil {
			return routingRuleSnapshot{}, fmt.Errorf("%v；同步状态保存失败: %w", err, saveErr)
		}
		if routingSnapshotContains(previous, ids) {
			return routingPublicSnapshot(state, previous, ids), nil
		}
		return routingRuleSnapshot{}, err
	}
	revision := previous.Revision
	warning := state.LastError
	if force || !fresh || revision == "" {
		resolved, err := s.resolveRoutingRevision(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return routingRuleSnapshot{}, ctx.Err()
			}
			if routingSnapshotContains(previous, ids) && !requireRaw {
				return failure(fmt.Errorf("MetaCubeX 版本检查失败，保留上一有效快照: %w", err))
			}
			if revision == "" {
				revision = routingBootstrapRevision
			}
			warning = "MetaCubeX 版本检查失败，使用固定提交 " + revision + ": " + err.Error()
		} else {
			revision = resolved
			warning = ""
		}
	}
	// Refresh the union of existing and requested rules, so a new publication
	// cannot silently evict another saved profile's cached rules.
	for id := range previous.Rules {
		requested[id] = true
	}
	candidate := routingDiskSnapshot{Revision: revision, UpdatedAt: now.Format(time.RFC3339Nano), Rules: make(map[string]routingRuleDocument)}
	if revision == previous.Revision {
		for id, doc := range previous.Rules {
			candidate.Rules[id] = doc
		}
	}
	var candidateRawBytes int64
	for _, doc := range candidate.Rules {
		candidateRawBytes += doc.RawBytes
	}
	if candidateRawBytes > routingRawSnapshotMaxBytes {
		return failure(fmt.Errorf("原始规则候选总量超过 64 MiB 限制"))
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var workers sync.WaitGroup
	var resultMu sync.Mutex
	var fetchErr error
	rawFiles := make(map[string][]byte)
	semaphore := make(chan struct{}, 4)
	var missing []routingRule
	for _, rule := range routingRuleCatalog() {
		if !requested[rule.ID] {
			continue
		}
		if doc := candidate.Rules[rule.ID]; len(doc.Entries) != 0 {
			if !requireRaw {
				continue
			}
			if _, err := s.readRoutingRawDocument(revision, rule.ID, doc); err == nil {
				continue
			}
		}
		missing = append(missing, rule)
	}
	for _, rule := range missing {
		workers.Add(1)
		go func(rule routingRule) {
			defer workers.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				resultMu.Lock()
				if fetchErr == nil {
					fetchErr = ctx.Err()
				}
				resultMu.Unlock()
				return
			}
			address := routingPinnedURL(rule, revision)
			content, err := s.routingSourceFetch(ctx, address, routingRuleMaxBytes)
			var entries []string
			if err == nil {
				entries, err = parseRoutingRuleYAML(rule, content)
			}
			resultMu.Lock()
			defer resultMu.Unlock()
			if fetchErr != nil {
				return
			}
			if err != nil {
				if fetchErr == nil {
					fetchErr = fmt.Errorf("%s 同步失败: %w", rule.Name, err)
					cancel()
				}
				return
			}
			nextRawBytes := candidateRawBytes + int64(len(content)) - candidate.Rules[rule.ID].RawBytes
			if nextRawBytes > routingRawSnapshotMaxBytes {
				fetchErr = fmt.Errorf("原始规则候选总量超过 64 MiB 限制")
				cancel()
				return
			}
			hash := routingSHA256(content)
			if old, ok := candidate.Rules[rule.ID]; ok && old.SHA256 != hash {
				if fetchErr == nil {
					fetchErr = fmt.Errorf("%s 固定提交的原始文件摘要改变，已拒绝替换", rule.Name)
					cancel()
				}
				return
			}
			candidate.Rules[rule.ID] = routingRuleDocument{Entries: entries, SHA256: hash, SourceURL: address, RawBytes: int64(len(content)), DownloadedAt: now.Format(time.RFC3339Nano)}
			candidateRawBytes = nextRawBytes
			rawFiles[rule.ID] = content
		}(rule)
	}
	workers.Wait()
	if fetchErr != nil {
		return failure(fetchErr)
	}
	// Remove newly created, unpublished candidates on failure. Existing fixed
	// resources are never removed, including valid publications whose pointer
	// update could not be completed.
	var created []string
	published := false
	defer func() {
		if !published {
			s.cleanupRoutingCandidatePaths(revision, created)
		}
	}()
	// Raw candidates are never publicly addressable before the whole candidate
	// has validated. Published resources retain their original bytes permanently.
	for id, raw := range rawFiles {
		path := s.routingRawPath(revision, id)
		if _, err := os.Stat(path); os.IsNotExist(err) && !s.routingPreviouslyKnownResource(revision, id) {
			created = append(created, path)
		}
		if err := s.writeRoutingRawDocument(revision, id, raw); err != nil {
			return failure(err)
		}
	}
	unchanged := revision == previous.Revision && reflect.DeepEqual(candidate.Rules, previous.Rules)
	if unchanged {
		candidate.UpdatedAt = previous.UpdatedAt
	}
	content, err := json.Marshal(candidate)
	if err != nil || len(content) > routingSnapshotMaxBytes {
		return failure(fmt.Errorf("新分流规则快照超过大小限制或编码失败"))
	}
	hash := routingSHA256(content)
	if !unchanged {
		path := filepath.Join(s.routingRulesDir(), "releases", revision, hash+".json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			created = append(created, path)
		}
		if err := routingRulesAtomicWrite(path, content); err != nil {
			return failure(fmt.Errorf("新分流规则快照保存失败: %w", err))
		}
	}
	if err := s.publishRoutingResourceManifest(candidate); err != nil {
		return failure(err)
	}
	published = true
	if unchanged {
		state.CheckedAt, state.LastError = now.Format(time.RFC3339Nano), warning
		if err := s.saveRoutingRuleState(state); err != nil {
			return routingRuleSnapshot{}, err
		}
		return routingPublicSnapshot(state, previous, ids), nil
	}
	state = routingRuleState{Revision: revision, Snapshot: hash, UpdatedAt: candidate.UpdatedAt, CheckedAt: now.Format(time.RFC3339Nano), LastError: warning}
	if err := s.saveRoutingRuleState(state); err != nil {
		return routingRuleSnapshot{}, fmt.Errorf("新分流规则发布失败: %w", err)
	}
	return routingPublicSnapshot(state, candidate, ids), nil
}

func (s *server) routingCatalogResponse() map[string]any {
	return s.routingResourceCatalogResponse()
}

func (s *server) routingCatalogHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "仅支持 GET"})
		return
	}
	writeJSON(w, http.StatusOK, s.routingCatalogResponse())
}

func (s *server) routingRuleSyncHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "仅支持 POST"})
		return
	}
	ids := make([]string, 0, len(routingRuleCatalog()))
	for _, rule := range routingRuleCatalog() {
		ids = append(ids, rule.ID)
	}
	_, err := s.loadRoutingRuleSnapshotMode(r.Context(), ids, true, true)
	result := s.routingCatalogResponse()
	status := http.StatusOK
	if err != nil {
		result["error"] = err.Error()
		status = http.StatusBadGateway
	} else if message, _ := result["last_error"].(string); message != "" {
		result["error"] = message
		status = http.StatusBadGateway
	}
	result["ok"] = status == http.StatusOK
	writeJSON(w, status, result)
}
