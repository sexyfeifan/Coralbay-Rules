package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

func (s *server) registerRoutingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /routing", s.adminPage)
	mux.HandleFunc("GET /routing/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/routing", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("GET /api/routing/catalog", s.auth(s.routingCatalogHandler))
	mux.HandleFunc("GET /api/routing/regions", s.auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"regions": routingRegions()})
	}))
	mux.HandleFunc("POST /api/routing/rules/sync", s.auth(s.routingRuleSyncHandler))
	mux.HandleFunc("POST /api/routing/rules/check", s.auth(s.routingRuleCheckHandler))
	mux.HandleFunc("GET /_rule-resources/metacubex/{revision}/{file}", s.routingRuleResourceHandler)
	mux.HandleFunc("GET /api/routing/rules/details", s.auth(s.routingRuleDetailsHandler))
	mux.HandleFunc("POST /api/routing/preview", s.auth(s.routingPreviewHandler))
	mux.HandleFunc("GET /api/routing/profiles", s.auth(s.routingProfilesHandler))
	mux.HandleFunc("GET /api/routing/profiles/{id}", s.auth(s.routingProfileHandler))
	mux.HandleFunc("POST /api/routing/profiles", s.auth(s.routingCreateHandler))
	mux.HandleFunc("PUT /api/routing/profiles/{id}", s.auth(s.routingUpdateHandler))
	mux.HandleFunc("PATCH /api/routing/profiles/{id}", s.auth(s.routingStateHandler))
	mux.HandleFunc("POST /api/routing/profiles/{id}/rotate", s.auth(s.routingRotateHandler))
	mux.HandleFunc("GET /api/routing/qr", s.auth(s.routingQRHandler))
	mux.HandleFunc("GET /routing/sub/{token}/{client}", s.routingSubscriptionHandler)
}

func routingDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求内容无效或过大"})
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "请求必须是单个 JSON 对象"})
		return false
	}
	return true
}

func routingPrivate(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("CDN-Cache-Control", "no-store")
}

func (s *server) runRoutingBuild(ctx context.Context, spec routingProfileSpec) (routingBuildResult, error) {
	if _, err := s.routingDatabase(); err != nil {
		return routingBuildResult{}, errors.New("分流存储不可用")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	select {
	case s.routingBuildSlots <- struct{}{}:
		defer func() { <-s.routingBuildSlots }()
	case <-ctx.Done():
		return routingBuildResult{}, errors.New("生成请求已取消或等待超时")
	}
	return s.buildRouting(ctx, spec)
}

func (s *server) routingPreviewHandler(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	var spec routingProfileSpec
	if !routingDecode(w, r, &spec) {
		return
	}
	if err := validateRoutingSpec(&spec); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	build, err := s.runRoutingBuild(r.Context(), spec)
	if err != nil {
		writeJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, build)
}

func (s *server) routingProfilesHandler(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	db, err := s.routingDatabase()
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "分流方案数据库不可用"})
		return
	}
	rows, err := db.Query("SELECT " + routingMetadataColumns + " FROM profiles ORDER BY updated_at DESC,id")
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "读取分流方案失败"})
		return
	}
	defer rows.Close()
	profiles := []routingProfile{}
	for rows.Next() {
		p, e := s.scanRoutingProfile(rows)
		if e != nil {
			writeJSON(w, 503, map[string]string{"error": "分流方案数据无法读取"})
			return
		}
		profiles = append(profiles, p)
	}
	if rows.Err() != nil {
		writeJSON(w, 503, map[string]string{"error": "读取分流方案失败"})
		return
	}
	writeJSON(w, 200, map[string]any{"profiles": profiles})
}

func (s *server) routingProfileHandler(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	p, err := s.getRoutingProfile(r.PathValue("id"))
	if err != nil {
		routingStoreError(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func routingStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, 404, map[string]string{"error": "分流方案不存在"})
	case errors.Is(err, errRoutingConflict):
		writeJSON(w, 409, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, 503, map[string]string{"error": "分流方案存储失败，请检查磁盘状态或方案数量上限"})
	}
}

func (s *server) routingCreateHandler(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	var spec routingProfileSpec
	if !routingDecode(w, r, &spec) {
		return
	}
	if err := validateRoutingSpec(&spec); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	build, err := s.runRoutingBuild(r.Context(), spec)
	if err != nil {
		writeJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	p, err := s.createRoutingProfile(spec, build)
	if err != nil {
		routingStoreError(w, err)
		return
	}
	s.audit("routing-create", "completed", p.ID)
	writeJSON(w, 201, p)
}

func (s *server) routingUpdateHandler(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	var body struct {
		Spec    routingProfileSpec `json:"spec"`
		Version int                `json:"version"`
	}
	if !routingDecode(w, r, &body) {
		return
	}
	id := r.PathValue("id")
	p, err := s.getRoutingProfile(id)
	if err != nil {
		routingStoreError(w, err)
		return
	}
	if body.Version != p.Version {
		routingStoreError(w, errRoutingConflict)
		return
	}
	if err := validateRoutingSpec(&body.Spec); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	build, err := s.runRoutingBuild(r.Context(), body.Spec)
	if err != nil {
		writeJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	p, err = s.updateRoutingProfile(id, body.Version, body.Spec, build)
	if err != nil {
		routingStoreError(w, err)
		return
	}
	s.audit("routing-update", "completed", p.ID)
	writeJSON(w, 200, p)
}

func (s *server) routingStateHandler(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	var body struct {
		Disabled *bool `json:"disabled"`
	}
	if !routingDecode(w, r, &body) {
		return
	}
	if body.Disabled == nil {
		writeJSON(w, 400, map[string]string{"error": "必须指定停用状态"})
		return
	}
	db, err := s.routingDatabase()
	if err != nil {
		routingStoreError(w, err)
		return
	}
	id := r.PathValue("id")
	result, err := db.Exec("UPDATE profiles SET disabled=?,version=version+1,updated_at=? WHERE id=?", *body.Disabled, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		routingStoreError(w, err)
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		routingStoreError(w, err)
		return
	}
	if count != 1 {
		routingStoreError(w, sql.ErrNoRows)
		return
	}
	p, err := s.getRoutingProfile(id)
	if err != nil {
		routingStoreError(w, err)
		return
	}
	s.audit("routing-state", "completed", fmt.Sprintf("%s disabled=%t", id, *body.Disabled))
	writeJSON(w, 200, p)
}

func (s *server) routingRotateHandler(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	token, err := routingRandom(32)
	if err != nil {
		routingStoreError(w, err)
		return
	}
	db, err := s.routingDatabase()
	if err != nil {
		routingStoreError(w, err)
		return
	}
	id := r.PathValue("id")
	result, err := db.Exec("UPDATE profiles SET token=?,version=version+1,updated_at=? WHERE id=?", token, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		routingStoreError(w, err)
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		routingStoreError(w, err)
		return
	}
	if count != 1 {
		routingStoreError(w, sql.ErrNoRows)
		return
	}
	p, err := s.getRoutingProfile(id)
	if err != nil {
		routingStoreError(w, err)
		return
	}
	s.audit("routing-rotate", "completed", id)
	writeJSON(w, 200, p)
}

func (s *server) routingQRHandler(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	p, err := s.getRoutingProfile(r.URL.Query().Get("id"))
	if err != nil {
		routingStoreError(w, err)
		return
	}
	link, ok := p.Links[r.URL.Query().Get("client")]
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "该方案未启用此客户端"})
		return
	}
	png, err := qrcode.Encode(link, qrcode.Medium, 256)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "二维码生成失败"})
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(png)
}

func (s *server) routingSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	routingPrivate(w)
	token, client := r.PathValue("token"), r.PathValue("client")
	p, err := s.routingTokenLookup(token, false)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "subscription unavailable", 503)
		}
		return
	}
	if p.Disabled {
		http.Error(w, "subscription disabled", 410)
		return
	}
	if _, ok := p.Links[client]; !ok {
		http.NotFound(w, r)
		return
	}
	// Coalesce refreshes per persisted profile; untrusted tokens cannot create locks.
	lock := s.routingProfileLock(p.ID)
	select {
	case <-lock:
		defer func() { lock <- struct{}{} }()
	case <-r.Context().Done():
		return
	}
	p, err = s.getRoutingProfileByToken(token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if p.Disabled {
		http.Error(w, "subscription disabled", 410)
		return
	}
	if _, ok := p.Links[client]; !ok {
		http.NotFound(w, r)
		return
	}
	last, _ := time.Parse(time.RFC3339Nano, p.LastBuiltAt)
	if time.Since(last) > routingCacheDuration || p.Outputs[client] == "" {
		attempt, _ := time.Parse(time.RFC3339Nano, p.LastAttemptAt)
		if p.LastError != "" && time.Since(attempt) < 30*time.Second {
			w.Header().Set("Retry-After", "30")
			http.Error(w, "subscription refresh temporarily unavailable; retry shortly", 503)
			return
		}
		build, buildErr := s.runRoutingBuild(r.Context(), p.Spec)
		db, dbErr := s.routingDatabase()
		if dbErr != nil {
			http.Error(w, "subscription unavailable", 503)
			return
		}
		if buildErr != nil {
			_, _ = db.Exec("UPDATE profiles SET last_error=?,last_attempt_at=? WHERE id=? AND version=?", buildErr.Error(), time.Now().UTC().Format(time.RFC3339Nano), p.ID, p.Version)
			http.Error(w, "subscription refresh failed; inspect this profile in CoralBay", 503)
			return
		}
		_, outputs, warnings, buildErr := routingBuildJSON(p.Spec, build)
		if buildErr != nil {
			http.Error(w, "subscription build invalid", 503)
			return
		}
		metadata, buildErr := routingBuildMetadataJSON(p.Spec, build)
		if buildErr != nil {
			http.Error(w, "subscription metadata invalid", 503)
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		result, err := db.Exec(`UPDATE profiles SET last_built_at=?,last_error='',node_count=?,rule_revision=?,outputs=?,usage_header=?,warnings=?,build_metadata=? WHERE id=? AND version=? AND token=? AND disabled=0`, now, build.NodeCount, build.Revision, outputs, build.UsageHeader, warnings, metadata, p.ID, p.Version, token)
		if err != nil {
			http.Error(w, "subscription unavailable", 503)
			return
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			http.Error(w, "subscription changed; refresh again", 409)
			return
		}
		p.Outputs, p.UsageHeader, p.LastBuiltAt = build.Outputs, build.UsageHeader, now
	}
	// Recheck revocation/client removal even when a refresh was served from cache.
	current, err := s.routingTokenLookup(token, false)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if current.Disabled {
		http.Error(w, "subscription disabled", 410)
		return
	}
	if _, ok := current.Links[client]; !ok {
		http.NotFound(w, r)
		return
	}
	if current.Version != p.Version {
		http.Error(w, "subscription changed; refresh again", 409)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="CoralBay_Routing_`+client+`.yaml"`)
	w.Header().Set("Profile-Update-Interval", strconv.Itoa(p.Spec.IntervalHours))
	w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(p.Spec.Name)))
	if p.UsageHeader != "" && !strings.ContainsAny(p.UsageHeader, "\r\n") {
		w.Header().Set("Subscription-Userinfo", p.UsageHeader)
	}
	if r.Method != http.MethodHead {
		db, err := s.routingDatabase()
		if err == nil {
			_, _ = db.Exec("UPDATE profiles SET requests=requests+1 WHERE id=?", p.ID)
		}
	}
	w.Write([]byte(p.Outputs[client]))
}
