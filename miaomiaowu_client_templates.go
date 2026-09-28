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
	"sync"
	"time"
)

// Client adaptations have their own versioned namespace. A published version
// includes the template and every exact MRS projection in one atomic directory.
const miaomiaowuClientMaxBytes = 64 << 20

type miaomiaowuClientOption struct {
	miaomiaowuTemplateOption
	Prepared bool   `json:"prepared"`
	Filename string `json:"filename"`
}

type miaomiaowuClientManifest struct {
	Format string                        `json:"format"`
	Client string                        `json:"client"`
	Option miaomiaowuClientOption        `json:"option"`
	Files  map[string]legacyResourceFile `json:"files"`
	Inputs map[string]string             `json:"inputs"`
}

func miaomiaowuClientExtension(client string) string {
	switch client {
	case "surge":
		return ".conf"
	case "loon":
		return ".lcf"
	default:
		return ""
	}
}

func miaomiaowuClientFilename(client, source string) string {
	return "coralbay_yyds_" + source + "__" + client + miaomiaowuClientExtension(client)
}

func (s *server) registerMiaomiaowuClientRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/templates/miaomiaowu/prepare", s.auth(s.miaomiaowuPrepareClient))
	mux.HandleFunc("GET /_miaomiaowu/clients/v1/{client}/{revision}/{source}/{file}", s.miaomiaowuClientFile)
}

func (s *server) miaomiaowuClientDir(client, revision, source string) string {
	return filepath.Join(s.dataDir, "rule-templates", "miaomiaowu", "clients", "v1", client, revision, source)
}

func (s *server) miaomiaowuClientURL(client, revision, source, filename string) string {
	return "https://" + s.domain + "/_miaomiaowu/clients/v1/" + client + "/" + revision + "/" + source + "/" + filename
}

func miaomiaowuClientFileKnown(client, source, filename string) bool {
	if filename == miaomiaowuClientFilename(client, source) {
		return true
	}
	return strings.HasSuffix(filename, ".list") && miaomiaowuRulesetPath(strings.TrimSuffix(filename, ".list")) != ""
}

func (s *server) readMiaomiaowuClientManifest(client, revision, source string) (miaomiaowuClientManifest, error) {
	var manifest miaomiaowuClientManifest
	if miaomiaowuClientExtension(client) == "" || !legacyVersionPattern.MatchString(revision) || !miaomiaowuSourceValid(source) {
		return manifest, fmt.Errorf("客户端、来源或固定版本无效")
	}
	dir := s.miaomiaowuClientDir(client, revision, source)
	info, err := os.Lstat(dir)
	if err != nil {
		return manifest, err
	}
	if !info.IsDir() {
		return manifest, fmt.Errorf("模板版本目录类型无效")
	}
	path := filepath.Join(dir, "manifest.json")
	info, err = os.Lstat(path)
	if err != nil {
		return manifest, fmt.Errorf("模板版本目录已存在但清单缺失：%v", err)
	}
	if !info.Mode().IsRegular() {
		return manifest, fmt.Errorf("模板清单类型无效")
	}
	data, err := routingReadBoundedFile(path, 128<<10)
	if err != nil {
		return manifest, err
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Format != miaomiaowuTemplateFormat || manifest.Client != client || len(manifest.Files) != 34 || len(manifest.Inputs) != 34 {
		return manifest, fmt.Errorf("客户端模板清单无效")
	}
	option := manifest.Option
	if option.ID != source || !option.Available || !option.Prepared || option.Filename != miaomiaowuClientFilename(client, source) || option.Revision != revision || !legacyCommitPattern.MatchString(option.RuleRevision) || option.ProviderCount != 33 || option.GroupCount != 20 || option.RuleCount != 29 || option.SHA256 != manifest.Files[option.Filename].SHA256 {
		return manifest, fmt.Errorf("客户端模板来源清单无效")
	}
	for file, meta := range manifest.Files {
		if !miaomiaowuClientFileKnown(client, source, file) || meta.Bytes < 1 || meta.Bytes > miaomiaowuClientMaxBytes || !routingHashPattern.MatchString(meta.SHA256) {
			return manifest, fmt.Errorf("客户端模板文件清单无效")
		}
	}
	for _, path := range append(append([]string{}, legacyResourcePaths()...), "_templates/MihomoPro.yaml") {
		if !routingHashPattern.MatchString(manifest.Inputs[path]) {
			return manifest, fmt.Errorf("客户端模板原件清单无效")
		}
	}
	return manifest, nil
}

func (s *server) readMiaomiaowuClientFile(manifest miaomiaowuClientManifest, filename string) ([]byte, error) {
	meta, ok := manifest.Files[filename]
	if !ok || !miaomiaowuClientFileKnown(manifest.Client, manifest.Option.ID, filename) {
		return nil, fmt.Errorf("模板文件不在白名单内")
	}
	path := filepath.Join(s.miaomiaowuClientDir(manifest.Client, manifest.Option.Revision, manifest.Option.ID), filename)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != meta.Bytes {
		return nil, fmt.Errorf("固定版本文件缺失、类型或大小不符")
	}
	data, err := routingReadBoundedFile(path, miaomiaowuClientMaxBytes)
	if err != nil || int64(len(data)) != meta.Bytes || routingSHA256(data) != meta.SHA256 {
		return nil, fmt.Errorf("固定版本文件摘要校验失败")
	}
	return data, nil
}

func (s *server) validateMiaomiaowuClientFiles(manifest miaomiaowuClientManifest, resources legacyResourceManifest) error {
	if resources.Status.Commit != manifest.Option.RuleRevision {
		return fmt.Errorf("模板规则提交与原件不一致")
	}
	for path, sha := range manifest.Inputs {
		if resources.Files[path].SHA256 != sha {
			return fmt.Errorf("模板原件摘要与所选版本不一致")
		}
	}
	for file := range manifest.Files {
		if _, err := s.readMiaomiaowuClientFile(manifest, file); err != nil {
			return err
		}
	}
	return nil
}

func miaomiaowuClientOptionFor(client, source string, resources legacyResourceManifest) miaomiaowuClientOption {
	label, description := "CoralBay 本机镜像", "读取本机固定版本的 33 个 MRS，精确转换为客户端文本规则；模板和文本规则均由 CoralBay 固定托管。"
	if source == "upstream" {
		label, description = "666OS 上游来源", "从 666OS 同一固定提交拉取 33 个 MRS 并核对摘要后转换；转换后的模板和文本规则仍由 CoralBay 托管，上游失败不会改用本机源。"
	}
	return miaomiaowuClientOption{miaomiaowuTemplateOption: miaomiaowuTemplateOption{ID: source, Label: label, Revision: resources.Status.ReleaseID, RuleRevision: resources.Status.Commit, ProviderCount: 33, GroupCount: 20, RuleCount: 29, Description: description}, Filename: miaomiaowuClientFilename(client, source)}
}

func (s *server) miaomiaowuClientOptions(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	client := r.URL.Query().Get("client")
	ext := miaomiaowuClientExtension(client)
	if ext == "" {
		writeJSON(w, 400, map[string]string{"error": "请选择 Clash、Surge 或 Loon"})
		return
	}
	resources, baselineErr := s.retainLegacyResources()
	if baselineErr == nil {
		input, err := s.legacyVerifiedResource(resources, "_templates/MihomoPro.yaml")
		baselineErr = err
		if err == nil {
			_, _, _, _, baselineErr = s.miaomiaowuConfig(input, resources, "local")
		}
	}
	options := []miaomiaowuClientOption{}
	for _, source := range []string{"local", "upstream"} {
		option := miaomiaowuClientOptionFor(client, source, resources)
		err := baselineErr
		if err == nil {
			var saved miaomiaowuClientManifest
			saved, err = s.readMiaomiaowuClientManifest(client, resources.Status.ReleaseID, source)
			if err == nil {
				err = s.validateMiaomiaowuClientFiles(saved, resources)
				if err == nil {
					option = saved.Option
				}
			} else if os.IsNotExist(err) {
				// A missing manifest in an existing release is never a new release.
				if _, statErr := os.Lstat(s.miaomiaowuClientDir(client, resources.Status.ReleaseID, source)); os.IsNotExist(statErr) {
					err = nil
					if s.mrsDecoder == nil {
						if _, coreErr := os.Stat("/usr/local/bin/coralbay-probe-core"); coreErr != nil {
							err = fmt.Errorf("MRS 解码内核不可用，请使用 CoralBay 完整镜像")
						}
					}
				}
			}
		}
		option.Available = err == nil
		option.Reason = errorText(err)
		options = append(options, option)
	}
	writeJSON(w, 200, map[string]any{"format": miaomiaowuTemplateFormat, "client": client, "filename": "coralbay_yyds__" + client + ext, "extension": ext, "content_type": "text/plain; charset=utf-8", "source_name": "666OS / YYDS Pro_cn", "source_url": miaomiaowuTemplateSource, "docs_url": "https://miaomiaowux.com/docs/templates/", "source_options": options, "local_error": errorText(baselineErr)})
}

// The generation lock is cancelable and independent of all public reads and
// other template APIs. A canceled waiter cannot later publish in the background.
func (s *server) lockMiaomiaowuClient(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.miaomiaowuClientMu.TryLock() {
			return nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *server) miaomiaowuClientResources(revision string) (legacyResourceManifest, map[string][]byte, error) {
	var resources legacyResourceManifest
	data, err := routingReadBoundedFile(filepath.Join(s.legacyResourceDir(revision), "manifest.json"), 2<<20)
	if err != nil || json.Unmarshal(data, &resources) != nil || resources.Status.ReleaseID != revision || !resources.Status.OK || !legacyCommitPattern.MatchString(resources.Status.Commit) {
		return resources, nil, fmt.Errorf("所选固定规则版本不可用，请刷新目录并同步 666OS 规则")
	}
	inputs := map[string][]byte{}
	for _, path := range append(append([]string{}, legacyResourcePaths()...), "_templates/MihomoPro.yaml") {
		inputs[path], err = s.legacyVerifiedResource(resources, path)
		if err != nil {
			return resources, nil, err
		}
	}
	return resources, inputs, nil
}

// Fetch only a user-selected source, at a whitelisted immutable commit. Verify
// the entire input set before decoding; no fallback or partly published result.
func (s *server) miaomiaowuClientUpstream(ctx context.Context, resources legacyResourceManifest, inputs map[string][]byte) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	jobs := make(chan string)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				if ctx.Err() != nil {
					continue
				}
				raw, err := s.legacyFetch(ctx, "https://raw.githubusercontent.com/666OS/rules/"+resources.Status.Commit+"/"+path)
				meta := resources.Files[path]
				if err == nil && (int64(len(raw)) != meta.Bytes || routingSHA256(raw) != meta.SHA256) {
					err = fmt.Errorf("上游规则与所选固定版本摘要不一致：%s", path)
				}
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
					cancel()
				} else if err == nil {
					inputs[path] = raw
				}
				mu.Unlock()
			}
		}()
	}
	for _, path := range legacyResourcePaths() {
		select {
		case jobs <- path:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

func (s *server) ensureMiaomiaowuClient(ctx context.Context, client, revision, source string) (miaomiaowuClientOption, error) {
	var option miaomiaowuClientOption
	if miaomiaowuClientExtension(client) == "" || !legacyVersionPattern.MatchString(revision) || !miaomiaowuSourceValid(source) {
		return option, fmt.Errorf("客户端、来源或固定版本无效")
	}
	if err := s.lockMiaomiaowuClient(ctx); err != nil {
		return option, err
	}
	defer s.miaomiaowuClientMu.Unlock()
	resources, inputs, err := s.miaomiaowuClientResources(revision)
	if err != nil {
		return option, err
	}
	// This validates the exact same 17 business groups and ordered 29 rules as
	// the existing Clash adapter, without writing any of its artifacts.
	template, noResolve, err := s.miaomiaowuClientConfig(inputs["_templates/MihomoPro.yaml"], resources, client, source)
	if err != nil {
		return option, err
	}
	dest := s.miaomiaowuClientDir(client, revision, source)
	if _, err = os.Lstat(dest); err == nil {
		manifest, readErr := s.readMiaomiaowuClientManifest(client, revision, source)
		if readErr == nil {
			readErr = s.validateMiaomiaowuClientFiles(manifest, resources)
		}
		return manifest.Option, readErr
	} else if !os.IsNotExist(err) {
		return option, err
	}
	if source == "upstream" {
		if err = s.miaomiaowuClientUpstream(ctx, resources, inputs); err != nil {
			return option, err
		}
	}
	if err = ctx.Err(); err != nil {
		return option, err
	}
	if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return option, err
	}
	candidate, err := os.MkdirTemp(filepath.Dir(dest), ".candidate-")
	if err != nil {
		return option, err
	}
	defer os.RemoveAll(candidate)
	option = miaomiaowuClientOptionFor(client, source, resources)
	option.Available, option.Prepared = true, true
	option.TemplateURL = s.miaomiaowuClientURL(client, revision, source, option.Filename)
	option.SHA256 = routingSHA256(template)
	manifest := miaomiaowuClientManifest{Format: miaomiaowuTemplateFormat, Client: client, Option: option, Files: map[string]legacyResourceFile{}, Inputs: map[string]string{}}
	for path, raw := range inputs {
		manifest.Inputs[path] = routingSHA256(raw)
	}
	writeFile := func(name string, data []byte) error {
		if len(data) < 1 || len(data) > miaomiaowuClientMaxBytes {
			return fmt.Errorf("客户端模板或规则集大小超过限制")
		}
		if err := os.WriteFile(filepath.Join(candidate, name), data, 0644); err != nil {
			return err
		}
		manifest.Files[name] = legacyResourceFile{Bytes: int64(len(data)), SHA256: routingSHA256(data)}
		return nil
	}
	if err = writeFile(option.Filename, template); err != nil {
		return option, err
	}
	for _, path := range legacyResourcePaths() {
		if err = ctx.Err(); err != nil {
			return option, err
		}
		behavior := miaomiaowuRulesetBehavior(path)
		entries, err := s.decodedMRS(ctx, behavior, inputs[path])
		if err != nil {
			return option, err
		}
		data, err := miaomiaowuClientRules(client, behavior, entries, noResolve[path])
		if err != nil {
			return option, err
		}
		if err = writeFile(miaomiaowuRulesetID(path)+".list", data); err != nil {
			return option, err
		}
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return option, err
	}
	if err = os.WriteFile(filepath.Join(candidate, "manifest.json"), encoded, 0644); err != nil {
		return option, err
	}
	if err = ctx.Err(); err != nil {
		return option, err
	}
	if err = os.Rename(candidate, dest); err != nil {
		return option, err
	}
	return option, nil
}

func (s *server) miaomiaowuPrepareClient(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	var input struct {
		Client   string `json:"client"`
		Source   string `json:"source"`
		Revision string `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || miaomiaowuClientExtension(input.Client) == "" || !miaomiaowuSourceValid(input.Source) || !legacyVersionPattern.MatchString(input.Revision) {
		writeJSON(w, 400, map[string]string{"error": "请选择有效的客户端、来源和固定版本"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	option, err := s.ensureMiaomiaowuClient(ctx, input.Client, input.Revision, input.Source)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, option)
}

func (s *server) miaomiaowuClientFile(w http.ResponseWriter, r *http.Request) {
	client, revision, source, filename := r.PathValue("client"), r.PathValue("revision"), r.PathValue("source"), r.PathValue("file")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if miaomiaowuClientExtension(client) == "" || !legacyVersionPattern.MatchString(revision) || !miaomiaowuSourceValid(source) || !miaomiaowuClientFileKnown(client, source, filename) {
		http.NotFound(w, r)
		return
	}
	manifest, err := s.readMiaomiaowuClientManifest(client, revision, source)
	if os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "固定版本客户端模板清单校验失败", 503)
		return
	}
	data, err := s.readMiaomiaowuClientFile(manifest, filename)
	if err != nil {
		http.Error(w, "固定版本客户端模板或规则集校验失败", 503)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-CoralBay-Rule-Source", source)
	w.Header().Set("ETag", `"`+manifest.Files[filename].SHA256+`"`)
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	}
	if r.Header.Get("If-None-Match") == w.Header().Get("ETag") {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(data)
}
