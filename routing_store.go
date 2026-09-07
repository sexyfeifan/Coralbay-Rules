package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const routingCacheDuration = 5 * time.Minute

type routingProfile struct {
	ID            string             `json:"id"`
	Spec          routingProfileSpec `json:"spec"`
	Version       int                `json:"version"`
	Disabled      bool               `json:"disabled"`
	Links         map[string]string  `json:"links"`
	CreatedAt     string             `json:"created_at"`
	UpdatedAt     string             `json:"updated_at"`
	LastBuiltAt   string             `json:"last_built_at"`
	LastError     string             `json:"last_error"`
	NodeCount     int                `json:"node_count"`
	RuleRevision  string             `json:"rule_revision"`
	Requests      int64              `json:"requests"`
	Warnings      []string           `json:"warnings"`
	Token         string             `json:"-"`
	Outputs       map[string]string  `json:"-"`
	UsageHeader   string             `json:"-"`
	LastAttemptAt string             `json:"last_attempt_at"`
}

func routingRandom(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *server) routingDatabase() (*sql.DB, error) {
	s.routingStoreMu.Lock()
	defer s.routingStoreMu.Unlock()
	if s.routingDB != nil {
		return s.routingDB, nil
	}
	dir := filepath.Join(s.dataDir, "routing")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "routing.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS profiles(
 id TEXT PRIMARY KEY, spec TEXT NOT NULL, version INTEGER NOT NULL DEFAULT 1,
 token TEXT NOT NULL UNIQUE, disabled INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, last_built_at TEXT NOT NULL,
 last_error TEXT NOT NULL DEFAULT '', node_count INTEGER NOT NULL,
 rule_revision TEXT NOT NULL, requests INTEGER NOT NULL DEFAULT 0,
 outputs TEXT NOT NULL, usage_header TEXT NOT NULL DEFAULT '', warnings TEXT NOT NULL DEFAULT '[]', last_attempt_at TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS profile_revisions(
 profile_id TEXT NOT NULL, version INTEGER NOT NULL, spec TEXT NOT NULL,
 rule_revision TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(profile_id,version));
CREATE TRIGGER IF NOT EXISTS routing_capacity BEFORE INSERT ON profiles
 WHEN (SELECT count(*) FROM profiles)>=1000
 BEGIN SELECT RAISE(ABORT,'routing profile capacity reached'); END;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	var hasAttempt int
	if err = db.QueryRow("SELECT count(*) FROM pragma_table_info('profiles') WHERE name='last_attempt_at'").Scan(&hasAttempt); err == nil && hasAttempt == 0 {
		_, err = db.Exec("ALTER TABLE profiles ADD COLUMN last_attempt_at TEXT NOT NULL DEFAULT ''")
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	s.routingDB = db
	s.routingBuildSlots = make(chan struct{}, 3)
	return db, nil
}

const routingProfileColumns = `id,spec,version,token,disabled,created_at,updated_at,last_built_at,last_error,node_count,rule_revision,requests,outputs,usage_header,warnings,last_attempt_at`
const routingMetadataColumns = `id,spec,version,token,disabled,created_at,updated_at,last_built_at,last_error,node_count,rule_revision,requests,'{}' AS outputs,usage_header,warnings,last_attempt_at`

type routingScanner interface{ Scan(...any) error }

func (s *server) scanRoutingProfile(row routingScanner) (routingProfile, error) {
	var p routingProfile
	var spec, outputs, warnings string
	err := row.Scan(&p.ID, &spec, &p.Version, &p.Token, &p.Disabled, &p.CreatedAt, &p.UpdatedAt, &p.LastBuiltAt, &p.LastError, &p.NodeCount, &p.RuleRevision, &p.Requests, &outputs, &p.UsageHeader, &warnings, &p.LastAttemptAt)
	if err != nil {
		return p, err
	}
	if json.Unmarshal([]byte(spec), &p.Spec) != nil || json.Unmarshal([]byte(outputs), &p.Outputs) != nil || json.Unmarshal([]byte(warnings), &p.Warnings) != nil {
		return p, errors.New("分流方案数据损坏")
	}
	p.Links = make(map[string]string, len(p.Spec.Clients))
	for _, client := range p.Spec.Clients {
		p.Links[client] = "https://" + s.domain + "/routing/sub/" + p.Token + "/" + client
	}
	return p, nil
}

func (s *server) getRoutingProfile(id string) (routingProfile, error) {
	db, err := s.routingDatabase()
	if err != nil {
		return routingProfile{}, err
	}
	return s.scanRoutingProfile(db.QueryRow("SELECT "+routingMetadataColumns+" FROM profiles WHERE id=?", id))
}

func (s *server) getRoutingProfileByToken(token string) (routingProfile, error) {
	return s.routingTokenLookup(token, true)
}

func (s *server) routingTokenLookup(token string, withOutputs bool) (routingProfile, error) {
	if len(token) != 43 {
		return routingProfile{}, sql.ErrNoRows
	}
	db, err := s.routingDatabase()
	if err != nil {
		return routingProfile{}, err
	}
	columns := routingMetadataColumns
	if withOutputs {
		columns = routingProfileColumns
	}
	return s.scanRoutingProfile(db.QueryRow("SELECT "+columns+" FROM profiles WHERE token=?", token))
}

func (s *server) routingProfileLock(id string) chan struct{} {
	semaphore := make(chan struct{}, 1)
	semaphore <- struct{}{}
	lock, _ := s.routingBuildLocks.LoadOrStore(id, semaphore)
	return lock.(chan struct{})
}

func routingBuildJSON(spec routingProfileSpec, build routingBuildResult) (string, string, string, error) {
	if build.NodeCount < 1 {
		return "", "", "", errors.New("没有可交付的节点")
	}
	for _, client := range spec.Clients {
		if strings.TrimSpace(build.Outputs[client]) == "" {
			return "", "", "", fmt.Errorf("%s 配置尚未生成", client)
		}
	}
	a, err := json.Marshal(spec)
	if err != nil {
		return "", "", "", err
	}
	b, err := json.Marshal(build.Outputs)
	if err != nil {
		return "", "", "", err
	}
	c, err := json.Marshal(build.Warnings)
	return string(a), string(b), string(c), err
}

func (s *server) createRoutingProfile(spec routingProfileSpec, build routingBuildResult) (routingProfile, error) {
	a, b, c, err := routingBuildJSON(spec, build)
	if err != nil {
		return routingProfile{}, err
	}
	id, err := routingRandom(16)
	if err != nil {
		return routingProfile{}, err
	}
	token, err := routingRandom(32)
	if err != nil {
		return routingProfile{}, err
	}
	db, err := s.routingDatabase()
	if err != nil {
		return routingProfile{}, err
	}
	tx, err := db.Begin()
	if err != nil {
		return routingProfile{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.Exec(`INSERT INTO profiles(id,spec,token,created_at,updated_at,last_built_at,node_count,rule_revision,outputs,usage_header,warnings) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, a, token, now, now, now, build.NodeCount, build.Revision, b, build.UsageHeader, c)
	if err != nil {
		return routingProfile{}, err
	}
	_, err = tx.Exec(`INSERT INTO profile_revisions(profile_id,version,spec,rule_revision,created_at) VALUES(?,1,?,?,?)`, id, a, build.Revision, now)
	if err != nil {
		return routingProfile{}, err
	}
	if err = tx.Commit(); err != nil {
		return routingProfile{}, err
	}
	return s.getRoutingProfile(id)
}

var errRoutingConflict = errors.New("方案已在其他页面修改，请重新加载后再保存")

func (s *server) updateRoutingProfile(id string, version int, spec routingProfileSpec, build routingBuildResult) (routingProfile, error) {
	a, b, c, err := routingBuildJSON(spec, build)
	if err != nil {
		return routingProfile{}, err
	}
	db, err := s.routingDatabase()
	if err != nil {
		return routingProfile{}, err
	}
	tx, err := db.Begin()
	if err != nil {
		return routingProfile{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r, err := tx.Exec(`UPDATE profiles SET spec=?,version=version+1,updated_at=?,last_built_at=?,last_error='',node_count=?,rule_revision=?,outputs=?,usage_header=?,warnings=? WHERE id=? AND version=?`, a, now, now, build.NodeCount, build.Revision, b, build.UsageHeader, c, id, version)
	if err != nil {
		return routingProfile{}, err
	}
	count, err := r.RowsAffected()
	if err != nil {
		return routingProfile{}, err
	}
	if count != 1 {
		return routingProfile{}, errRoutingConflict
	}
	_, err = tx.Exec(`INSERT INTO profile_revisions(profile_id,version,spec,rule_revision,created_at) VALUES(?,?,?,?,?)`, id, version+1, a, build.Revision, now)
	if err != nil {
		return routingProfile{}, err
	}
	if err = tx.Commit(); err != nil {
		return routingProfile{}, err
	}
	return s.getRoutingProfile(id)
}
