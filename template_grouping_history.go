package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Lifecycle metadata is separate from immutable exports. Recycling never
// rewrites a template or removes rule snapshots shared with other exports.
type templateGroupingLifecycle struct {
	DeletedAt       string `json:"deleted_at,omitempty"`
	LastGeneratedAt string `json:"last_generated_at,omitempty"`
	GenerationCount int    `json:"generation_count"`
}

func (s *server) groupingLifecyclePath(id string) string {
	return filepath.Join(s.dataDir, "grouped-history", id+".json")
}

func (s *server) groupingLifecycle(id string) (templateGroupingLifecycle, error) {
	var state templateGroupingLifecycle
	if !routingHashPattern.MatchString(id) {
		return state, fmt.Errorf("模板版本无效")
	}
	info, err := os.Lstat(s.groupingLifecyclePath(id))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if !info.Mode().IsRegular() {
		return state, fmt.Errorf("历史状态文件无效")
	}
	data, err := routingReadBoundedFile(s.groupingLifecyclePath(id), 4096)
	if err != nil {
		return state, err
	}
	if json.Unmarshal(data, &state) != nil || state.GenerationCount < 0 {
		return state, fmt.Errorf("历史状态损坏，请从备份恢复")
	}
	return state, nil
}

func (s *server) groupingLifecycleWrite(id string, state templateGroupingLifecycle) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return routingRulesAtomicWrite(s.groupingLifecyclePath(id), data)
}

// Caller holds the publishing lock. A read does not create or migrate files.
func (s *server) groupingTouch(id string) error {
	state, err := s.groupingLifecycle(id)
	if err != nil {
		return err
	}
	if state.DeletedAt != "" {
		return fmt.Errorf("此版本在回收站，请先恢复，再重新生成")
	}
	state.LastGeneratedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if state.GenerationCount == 0 {
		if manifest, readErr := s.groupingHistoryManifest(id); readErr == nil && manifest.Generator == "grouping-v1" {
			state.GenerationCount = 1
		}
	}
	state.GenerationCount++
	return s.groupingLifecycleWrite(id, state)
}

type templateGroupingHistoryItem struct {
	templateGroupingGeneration
	templateGroupingLifecycle
}

// Listing reads bounded manifests only. Full file checksums are verified on
// detail/download, rather than hashing every historical file on each refresh.
func (s *server) groupingHistoryManifest(id string) (templateGroupingGeneration, error) {
	var m templateGroupingGeneration
	if !routingHashPattern.MatchString(id) {
		return m, fmt.Errorf("模板版本无效")
	}
	info, err := os.Lstat(s.templateGroupingDir(id))
	if err != nil || !info.IsDir() {
		return m, fmt.Errorf("模板目录无效")
	}
	info, err = os.Lstat(filepath.Join(s.templateGroupingDir(id), "manifest.json"))
	if err != nil || !info.Mode().IsRegular() {
		return m, fmt.Errorf("历史清单缺失或无效")
	}
	data, err := routingReadBoundedFile(filepath.Join(s.templateGroupingDir(id), "manifest.json"), 128<<10)
	if err != nil {
		return m, err
	}
	if json.Unmarshal(data, &m) != nil || m.ID != id || !groupingGeneratorKnown(m.Generator) || !templateGroupingTargetValid(m.Scope, m.Client) || !miaomiaowuSourceValid(m.Source) || len(m.Artifacts) < 1 || len(m.Artifacts) > 2 {
		return m, fmt.Errorf("历史清单无效")
	}
	seen := map[string]bool{}
	for i := range m.Artifacts {
		a := &m.Artifacts[i]
		if seen[a.Name] || !templateGroupingFileName.MatchString(a.Name) || !routingHashPattern.MatchString(a.SHA256) || a.Bytes < 1 || a.Bytes > legacyResourceMaxBytes {
			return m, fmt.Errorf("历史文件无效")
		}
		seen[a.Name] = true
		a.Content = ""
		a.URL = "https://" + s.domain + "/_grouped-templates/" + id + "/" + a.Name
		a.DownloadURL = a.URL + "?download=1"
	}
	return m, nil
}

func groupingGeneratorKnown(generator string) bool {
	return generator == "grouping-v1" || generator == templateGroupingGenerator
}

func (s *server) groupingHistory(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	scope := r.URL.Query().Get("scope")
	if scope == "common" || !templateGroupingScopeValid(scope) {
		writeJSON(w, 400, map[string]string{"error": "请选择有效模板入口"})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		page = 10000
	}
	archived := r.URL.Query().Get("archived") == "true"
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if len([]rune(q)) > 256 {
		writeJSON(w, 400, map[string]string{"error": "搜索内容最多 256 字符"})
		return
	}
	templateGroupingPublishMu.Lock()
	defer templateGroupingPublishMu.Unlock()
	entries, err := os.ReadDir(filepath.Join(s.dataDir, "grouped-templates"))
	if err != nil && !os.IsNotExist(err) {
		writeJSON(w, 503, map[string]string{"error": "历史目录读取失败"})
		return
	}
	items := []templateGroupingHistoryItem{}
	invalid := 0
	for _, entry := range entries {
		if !entry.IsDir() || !routingHashPattern.MatchString(entry.Name()) {
			continue
		}
		m, err := s.groupingHistoryManifest(entry.Name())
		if err != nil {
			invalid++
			continue
		}
		if m.Scope != scope {
			continue
		}
		state, err := s.groupingLifecycle(m.ID)
		if err != nil {
			invalid++
			continue
		}
		if (state.DeletedAt != "") != archived {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(m.Profile.Name+" "+m.Client+" "+m.Source+" "+m.ID+" "+m.RuleRevision), q) {
			continue
		}
		if state.LastGeneratedAt == "" {
			state.LastGeneratedAt = m.CreatedAt
			state.GenerationCount = 1
		}
		items = append(items, templateGroupingHistoryItem{m, state})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].LastGeneratedAt == items[j].LastGeneratedAt {
			return items[i].ID > items[j].ID
		}
		ti, ei := time.Parse(time.RFC3339Nano, items[i].LastGeneratedAt)
		tj, ej := time.Parse(time.RFC3339Nano, items[j].LastGeneratedAt)
		if ei == nil && ej == nil {
			return ti.After(tj)
		}
		return items[i].LastGeneratedAt > items[j].LastGeneratedAt
	})
	total := len(items)
	start := (page - 1) * 20
	if start > total {
		start = total
	}
	end := start + 20
	if end > total {
		end = total
	}
	writeJSON(w, 200, map[string]any{"items": items[start:end], "total": total, "page": page, "page_size": 20, "invalid": invalid})
}

func (s *server) groupingHistoryDetail(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	id := r.PathValue("id")
	templateGroupingPublishMu.Lock()
	defer templateGroupingPublishMu.Unlock()
	m, err := s.templateGroupingReadGeneration(id, true)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "历史文件不存在或校验失败"})
		return
	}
	state, err := s.groupingLifecycle(id)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	for i := range m.Artifacts {
		m.Artifacts[i].URL = "https://" + s.domain + "/_grouped-templates/" + id + "/" + m.Artifacts[i].Name
		m.Artifacts[i].DownloadURL = m.Artifacts[i].URL + "?download=1"
	}
	writeJSON(w, 200, templateGroupingHistoryItem{m, state})
}

func (s *server) groupingHistoryRecycle(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	id := r.PathValue("id")
	templateGroupingPublishMu.Lock()
	defer templateGroupingPublishMu.Unlock()
	if _, err := s.groupingHistoryManifest(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "历史版本不存在"})
		return
	}
	state, err := s.groupingLifecycle(id)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	if r.Method == http.MethodDelete {
		state.DeletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	} else {
		state.DeletedAt = ""
	}
	if err = s.groupingLifecycleWrite(id, state); err != nil {
		writeJSON(w, 503, map[string]string{"error": "历史状态保存失败"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "id": id, "deleted_at": state.DeletedAt})
}

func (s *server) groupingAdvancedBaseline(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	scope, client := r.URL.Query().Get("scope"), r.URL.Query().Get("client")
	if !templateGroupingTargetValid(scope, client) {
		writeJSON(w, 400, map[string]string{"error": "当前客户端不支持这些高级选项"})
		return
	}
	resources, err := s.retainLegacyResources()
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "基础模板尚未就绪"})
		return
	}
	path := "_templates/MihomoPro.yaml"
	if scope == "ppanel" {
		path = "_templates/clients/" + client + ".gotmpl"
	}
	input, err := s.legacyVerifiedResource(resources, path)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "基础模板读取失败"})
		return
	}
	fields := map[string]any{}
	if scope != "ppanel" {
		if yaml.Unmarshal(input, &fields) != nil {
			writeJSON(w, 503, map[string]string{"error": "基础模板格式无效"})
			return
		}
	} else {
		// Extract literal top-level settings before the untouched Go node renderer.
		lines := strings.Split(string(input), "\n")
		for _, key := range []string{"dns", "ipv6", "sniffer"} {
			block := []string{}
			recording := false
			for _, line := range lines {
				if strings.HasPrefix(line, key+":") {
					recording = true
				} else if recording && len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '#' {
					break
				}
				if recording {
					block = append(block, line)
				}
			}
			var value map[string]any
			if len(block) > 0 && yaml.Unmarshal([]byte(strings.Join(block, "\n")), &value) != nil {
				writeJSON(w, 503, map[string]string{"error": "基础模板高级字段无法解析"})
				return
			}
			if len(block) > 0 {
				fields[key] = value[key]
			}
		}
	}
	writeJSON(w, 200, map[string]any{"revision": resources.Status.ReleaseID, "dns": fields["dns"], "ipv6": fields["ipv6"], "sniffer": fields["sniffer"], "scope": scope, "client": client})
}
