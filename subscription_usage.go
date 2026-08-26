package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	_ "modernc.org/sqlite"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type subscriptionUsage struct {
	ID       string     `json:"id"`
	URL      string     `json:"url"`
	Target   string     `json:"target"`
	Disabled bool       `json:"disabled"`
	Success  uint64     `json:"success"`
	Failure  uint64     `json:"failure"`
	Blocked  uint64     `json:"blocked"`
	First    *time.Time `json:"first,omitempty"`
	Last     *time.Time `json:"last,omitempty"`
	Client   string     `json:"client"`
}

func usageID(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	p := u.Query()
	p.Del("sig")
	h := sha256.Sum256([]byte(p.Encode()))
	return hex.EncodeToString(h[:])
}
func (s *server) usagePath() string {
	return filepath.Join(s.dataDir, "settings", "subscription-usage.json")
}
func usageTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func insertUsage(tx *sql.Tx, v *subscriptionUsage) error {
	_, e := tx.Exec("INSERT OR IGNORE INTO links(id,url,target,disabled,success,failure,blocked,first,last,client) VALUES(?,?,?,?,?,?,?,?,?,?)", v.ID, v.URL, v.Target, v.Disabled, v.Success, v.Failure, v.Blocked, usageTime(v.First), usageTime(v.Last), v.Client)
	return e
}

// Caller holds s.mu during initialization. Legacy JSON remains as backup.
func (s *server) openUsageDB() (*sql.DB, error) {
	if s.usageDB != nil {
		return s.usageDB, nil
	}
	dir := filepath.Join(s.dataDir, "settings")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, "subscription-usage.sqlite")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	f.Close()
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	ok := false
	defer func() {
		if !ok {
			db.Close()
		}
	}()
	_, e = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS links(id TEXT PRIMARY KEY,url TEXT NOT NULL,target TEXT NOT NULL,disabled INTEGER NOT NULL DEFAULT 0,success INTEGER NOT NULL DEFAULT 0,failure INTEGER NOT NULL DEFAULT 0,blocked INTEGER NOT NULL DEFAULT 0,first TEXT,last TEXT,client TEXT NOT NULL DEFAULT '');
 CREATE INDEX IF NOT EXISTS links_last ON links(last DESC,id);
 CREATE INDEX IF NOT EXISTS links_state ON links(disabled,last DESC);
 CREATE TRIGGER IF NOT EXISTS link_capacity BEFORE INSERT ON links WHEN NOT EXISTS(SELECT 1 FROM links WHERE id=NEW.id) AND (SELECT count(*) FROM links)>=10000 BEGIN SELECT RAISE(ABORT,'link registry capacity reached'); END;
 CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL);`)
	if e != nil {
		return nil, e
	}
	var done string
	e = db.QueryRow("SELECT value FROM metadata WHERE key='json-migrated'").Scan(&done)
	if e != nil && e != sql.ErrNoRows {
		return nil, e
	}
	if e == sql.ErrNoRows {
		items := map[string]*subscriptionUsage{}
		data, err := os.ReadFile(s.usagePath())
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			if err = json.Unmarshal(data, &items); err != nil || items == nil {
				return nil, fmt.Errorf("旧统计损坏，拒绝丢弃停用记录")
			}
		}
		tx, err := db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		for key, v := range items {
			if v == nil || v.ID != key || usageID(v.URL) != key {
				return nil, fmt.Errorf("旧统计记录损坏")
			}
			if err = insertUsage(tx, v); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec("INSERT INTO metadata VALUES('json-migrated','1')"); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
	}
	s.usageDB = db
	ok = true
	return db, nil
}
func scanUsage(rows *sql.Rows) (*subscriptionUsage, error) {
	v := &subscriptionUsage{}
	var first, last sql.NullString
	e := rows.Scan(&v.ID, &v.URL, &v.Target, &v.Disabled, &v.Success, &v.Failure, &v.Blocked, &first, &last, &v.Client)
	if e != nil {
		return nil, e
	}
	if first.Valid {
		t, e := time.Parse(time.RFC3339Nano, first.String)
		if e != nil {
			return nil, e
		}
		v.First = &t
	}
	if last.Valid {
		t, e := time.Parse(time.RFC3339Nano, last.String)
		if e != nil {
			return nil, e
		}
		v.Last = &t
	}
	return v, nil
}
func (s *server) readUsage() (map[string]*subscriptionUsage, error) {
	db, e := s.openUsageDB()
	if e != nil {
		return nil, e
	}
	rows, e := db.Query("SELECT id,url,target,disabled,success,failure,blocked,first,last,client FROM links")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := map[string]*subscriptionUsage{}
	for rows.Next() {
		v, e := scanUsage(rows)
		if e != nil {
			return nil, e
		}
		items[v.ID] = v
	}
	return items, rows.Err()
}
func (s *server) preserveHistoryUsage() error {
	db, e := s.openUsageDB()
	if e != nil {
		return e
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, h := range s.readSubscriptionHistory() {
		if e = insertUsage(tx, &subscriptionUsage{ID: usageID(h.URL), URL: h.URL, Target: h.Target}); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func subscriptionClient(ua string) string {
	ua = strings.ToLower(ua)
	for _, name := range []string{"stash", "loon", "surge", "shadowrocket", "mihomo", "openclash", "clash", "sing-box", "quantumult"} {
		if strings.Contains(ua, name) {
			return name
		}
	}
	if strings.Contains(ua, "mozilla") {
		return "浏览器"
	}
	return "其他 / 未知"
}
func (s *server) startSubscriptionAccess(raw, ua string) (string, bool, error) {
	s.mu.Lock()
	db, e := s.openUsageDB()
	s.mu.Unlock()
	if e != nil {
		return "", false, e
	}
	u, e := url.Parse(raw)
	if e != nil {
		return "", false, e
	}
	id := usageID(raw)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, e := db.Begin()
	if e != nil {
		return "", false, e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`INSERT INTO links(id,url,target,first,last,client) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET first=COALESCE(links.first,excluded.first),last=excluded.last,client=excluded.client,blocked=links.blocked+links.disabled`, id, raw, u.Query().Get("target"), now, now, subscriptionClient(ua))
	if e != nil {
		return "", false, e
	}
	var disabled bool
	if e = tx.QueryRow("SELECT disabled FROM links WHERE id=?", id).Scan(&disabled); e != nil {
		return "", false, e
	}
	return id, disabled, tx.Commit()
}
func (s *server) finishSubscriptionAccess(id string, success bool) error {
	s.mu.Lock()
	db, e := s.openUsageDB()
	s.mu.Unlock()
	if e != nil {
		return e
	}
	column := "failure"
	if success {
		column = "success"
	}
	res, e := db.Exec("UPDATE links SET "+column+"="+column+"+1 WHERE id=?", id)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e == nil && n != 1 {
		return fmt.Errorf("missing link")
	}
	return e
}
func boundedInt(raw string, fallback, max int) int {
	n, e := strconv.Atoi(raw)
	if e != nil || n < 1 {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}
func (s *server) subscriptionUsageCatalog(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	e := s.preserveHistoryUsage()
	db := s.usageDB
	s.mu.Unlock()
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "链接数据库不可用"})
		return
	}
	q := r.URL.Query()
	page := boundedInt(q.Get("page"), 1, 1000000)
	size := boundedInt(q.Get("page_size"), 20, 100)
	where := " WHERE 1=1"
	args := []any{}
	search := strings.TrimSpace(q.Get("q"))
	if len(search) > 120 {
		search = search[:120]
	}
	if search != "" {
		where += " AND (instr(id,?)>0 OR instr(target,?)>0 OR instr(client,?)>0)"
		args = append(args, search, search, search)
	}
	cutoff := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	switch q.Get("state") {
	case "disabled":
		where += " AND disabled=1"
	case "enabled":
		where += " AND disabled=0"
	case "never":
		where += " AND last IS NULL"
	case "recent":
		where += " AND last>=?"
		args = append(args, cutoff)
	case "inactive":
		where += " AND last<?"
		args = append(args, cutoff)
	}
	var count int
	if e = db.QueryRow("SELECT count(*) FROM links"+where, args...).Scan(&count); e != nil {
		writeJSON(w, 503, map[string]string{"error": "统计查询失败"})
		return
	}
	pages := (count + size - 1) / size
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	rows, e := db.Query("SELECT id,url,target,disabled,success,failure,blocked,first,last,client FROM links"+where+" ORDER BY last DESC,id LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "查询失败"})
		return
	}
	defer rows.Close()
	list := []*subscriptionUsage{}
	for rows.Next() {
		v, e := scanUsage(rows)
		if e != nil {
			writeJSON(w, 503, map[string]string{"error": "数据损坏"})
			return
		}
		list = append(list, v)
	}
	if rows.Err() != nil {
		writeJSON(w, 503, map[string]string{"error": "读取失败"})
		return
	}
	writeJSON(w, 200, map[string]any{"links": list, "count": count, "page": page, "page_size": size, "pages": pages})
}
func (s *server) setSubscriptionDisabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Disabled *bool `json:"disabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil || body.Disabled == nil {
		writeJSON(w, 400, map[string]string{"error": "需要 disabled 布尔值"})
		return
	}
	s.mu.Lock()
	db, e := s.openUsageDB()
	s.mu.Unlock()
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "数据库不可用"})
		return
	}
	res, e := db.Exec("UPDATE links SET disabled=? WHERE id=?", *body.Disabled, r.PathValue("id"))
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "保存失败"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, 404, map[string]string{"error": "链接不存在"})
		return
	}
	s.audit("subscription-link-state", fmt.Sprintf("disabled=%t", *body.Disabled), r.PathValue("id"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}
