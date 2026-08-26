package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestUsageSurvivesHistoryDeletionAndRestart(t *testing.T) {
	s := &server{dataDir: t.TempDir()}
	raw := "https://rules.example/sub?target=stash&url=https%3A%2F%2Fexample.com%2Fsub&sig=old"
	s.appendSubscriptionHistory(subscriptionHistoryItem{ID: "history", URL: raw, Target: "stash"})
	id, disabled, err := s.startSubscriptionAccess(raw, "Stash/3.4")
	if err != nil || disabled {
		t.Fatal(err)
	}
	if err = s.finishSubscriptionAccess(id, true); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PUT", "/", strings.NewReader(`{"disabled":true}`))
	req.SetPathValue("id", id)
	res := httptest.NewRecorder()
	s.setSubscriptionDisabled(res, req)
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	res = httptest.NewRecorder()
	s.clearSubscriptionHistory(res, httptest.NewRequest("DELETE", "/", nil))
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	restarted := &server{dataDir: s.dataDir}
	_, disabled, err = restarted.startSubscriptionAccess(strings.Replace(raw, "sig=old", "sig=new", 1), "Mozilla/5.0")
	if err != nil || !disabled {
		t.Fatalf("deleted history reactivated link: %v", err)
	}
	items, err := restarted.readUsage()
	if err != nil {
		t.Fatal(err)
	}
	if items[id].Success != 1 || items[id].Blocked != 1 || items[id].First == nil || items[id].Client != "浏览器" {
		t.Fatalf("wrong stats: %+v", items[id])
	}
}
func TestUsageConcurrentCounters(t *testing.T) {
	s := &server{dataDir: t.TempDir()}
	id, _, err := s.startSubscriptionAccess("https://example.com/sub?target=stash", "Stash")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.finishSubscriptionAccess(id, true); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	items, _ := s.readUsage()
	if items[id].Success != 25 {
		t.Fatal(items[id].Success)
	}
}
func TestUsageCorruptionFailsClosed(t *testing.T) {
	s := &server{dataDir: t.TempDir()}
	if err := os.MkdirAll(filepath.Dir(s.usagePath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.usagePath(), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.startSubscriptionAccess("https://example.com/sub?target=stash", ""); err == nil {
		t.Fatal("corrupt denylist silently ignored")
	}
}
