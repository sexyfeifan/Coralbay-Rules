package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"time"
)

var errSubscriptionDisabled = errors.New("相同配置的订阅已停用或已删除，请先在订阅管理中恢复（已删除订阅位于回收站）")

func (s *server) registerSubscription(raw, target string) error {
	s.mu.Lock()
	db, e := s.openUsageDB()
	s.mu.Unlock()
	if e != nil {
		return e
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = insertUsage(tx, &subscriptionUsage{ID: usageID(raw), URL: raw, Target: target}); e != nil {
		return e
	}
	var disabled, archived bool
	if e = tx.QueryRow("SELECT disabled,archived FROM links WHERE id=?", usageID(raw)).Scan(&disabled, &archived); e != nil {
		return e
	}
	if disabled || archived {
		return errSubscriptionDisabled
	}
	if _, e = tx.Exec("UPDATE links SET url=? WHERE id=?", raw, usageID(raw)); e != nil {
		return e
	}
	return tx.Commit()
}

// Re-signing never changes the canonical ID or disabled state.
func (s *server) renewSubscriptionLink(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	db, e := s.openUsageDB()
	s.mu.Unlock()
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "数据库不可用"})
		return
	}
	var raw string
	if e = db.QueryRow("SELECT url FROM links WHERE id=?", r.PathValue("id")).Scan(&raw); e != nil {
		code := 503
		if e == sql.ErrNoRows {
			code = 404
		}
		writeJSON(w, code, map[string]string{"error": "链接不存在或不可读取"})
		return
	}
	u, e := url.Parse(raw)
	if e != nil {
		writeJSON(w, 400, map[string]string{"error": "链接格式错误"})
		return
	}
	p := u.Query()
	p.Del("sig")
	link, e := s.subscriptionLink(p)
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "签名密钥不可用"})
		return
	}
	if _, e = db.Exec("UPDATE links SET url=? WHERE id=?", link, r.PathValue("id")); e != nil {
		writeJSON(w, 503, map[string]string{"error": "保存失败"})
		return
	}
	writeJSON(w, 200, map[string]string{"url": link})
}
func (s *server) subscriptionKeyStatus(w http.ResponseWriter, r *http.Request) {
	k, e := s.loadSubscriptionKeys()
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "签名密钥不可用"})
		return
	}
	writeJSON(w, 200, map[string]any{"version": 2, "legacy_until": k.LegacyUntil, "legacy_active": time.Now().Before(k.LegacyUntil)})
}
func (s *server) pruneSubscriptionStats(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	db, e := s.openUsageDB()
	s.mu.Unlock()
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "数据库不可用"})
		return
	}
	result, e := db.Exec("UPDATE links SET success=0,failure=0,blocked=0,first=NULL,client='' WHERE disabled=0 AND last<?", time.Now().UTC().Add(-180*24*time.Hour).Format(time.RFC3339Nano))
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "清理失败"})
		return
	}
	count, _ := result.RowsAffected()
	writeJSON(w, 200, map[string]any{"reset": count, "note": "已保留链接、最后访问时间和所有停用记录"})
}
