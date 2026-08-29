package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUnifiedManagerGroupsHistoryAndPreservesState(t *testing.T) {
	s := &server{dataDir: t.TempDir()}
	raw := "https://rules.example/sub?target=stash&url=https%3A%2F%2Fexample.com%2Fsub&sig=old"
	id, _, err := s.startSubscriptionAccess(raw, "Stash")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finishSubscriptionAccess(id, true); err != nil {
		t.Fatal(err)
	}
	for i, historyID := range []string{"older", "newer"} {
		h := subscriptionHistoryItem{ID: historyID, URL: strings.Replace(raw, "sig=old", "sig=new", 1), Filename: "日本订阅", Target: "stash", CreatedAt: time.Now().Add(time.Duration(i) * time.Minute), NodeCount: 6, Settings: &subscriptionRequest{UDP: true}}
		if err = s.appendSubscriptionHistory(h); err != nil {
			t.Fatal(err)
		}
	}
	catalog := func(query string) []subscriptionUsage {
		t.Helper()
		res := httptest.NewRecorder()
		s.subscriptionUsageCatalog(res, httptest.NewRequest("GET", "/?"+query, nil))
		if res.Code != 200 {
			t.Fatal(res.Body.String())
		}
		var data struct{ Links []subscriptionUsage }
		if err := json.Unmarshal(res.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data.Links
	}
	links := catalog("q=日本&state=with_history")
	if len(links) != 1 || len(links[0].History) != 2 || links[0].History[0].ID != "newer" || links[0].Success != 1 || links[0].URL != raw {
		t.Fatalf("incorrect merged record: %#v", links)
	}
	res := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/", strings.NewReader(`{"disabled":true}`))
	req.SetPathValue("id", id)
	s.setSubscriptionDisabled(res, req)
	res = httptest.NewRecorder()
	req = httptest.NewRequest("DELETE", "/", nil)
	req.SetPathValue("id", "newer")
	s.deleteSubscriptionHistory(res, req)
	if res.Code != 200 || len(catalog("")[0].History) != 1 {
		t.Fatal("single generation deletion failed")
	}
	res = httptest.NewRecorder()
	s.clearSubscriptionHistory(res, httptest.NewRequest("DELETE", "/", nil))
	links = catalog("state=without_history")
	if len(links) != 1 || len(links[0].History) != 0 || links[0].Success != 1 || !links[0].Disabled {
		t.Fatal("history clearing lost managed link")
	}
	if len(catalog("state=with_history")) != 0 {
		t.Fatal("stale history filter")
	}
	res = httptest.NewRecorder()
	req = httptest.NewRequest("PUT", "/", strings.NewReader(`{"archived":true}`))
	req.SetPathValue("id", id)
	s.setSubscriptionDisabled(res, req)
	if res.Code != 200 || len(catalog("")) != 0 {
		t.Fatal("archived subscription remained in default catalog")
	}
	links = catalog("state=archived")
	if len(links) != 1 || !links[0].Archived || !links[0].Disabled || links[0].Success != 1 {
		t.Fatalf("archive lost state: %#v", links)
	}
	_, disabled, err := s.startSubscriptionAccess(raw, "Stash")
	if err != nil || !disabled {
		t.Fatalf("archived subscription was not blocked: %v %v", disabled, err)
	}
	res = httptest.NewRecorder()
	req = httptest.NewRequest("PUT", "/", strings.NewReader(`{"archived":false}`))
	req.SetPathValue("id", id)
	s.setSubscriptionDisabled(res, req)
	links = catalog("")
	if res.Code != 200 || len(links) != 1 || links[0].Archived || links[0].Disabled || links[0].Blocked != 1 {
		t.Fatalf("restore lost state: %#v", links)
	}
}

func TestUnifiedManagerNewGenerationBeforeOldAccess(t *testing.T) {
	s := &server{dataDir: t.TempDir()}
	old := "https://example.com/sub?target=stash&url=old"
	recent := "https://example.com/sub?target=stash&url=new"
	s.startSubscriptionAccess(old, "Stash")
	s.appendSubscriptionHistory(subscriptionHistoryItem{ID: "recent", URL: recent, Target: "stash", CreatedAt: time.Now().Add(time.Second)})
	for _, test := range []struct{ query, want string }{{"page_size=1", usageID(recent)}, {"page_size=1&sort=access", usageID(old)}} {
		res := httptest.NewRecorder()
		s.subscriptionUsageCatalog(res, httptest.NewRequest("GET", "/?"+test.query, nil))
		var data struct{ Links []subscriptionUsage }
		json.Unmarshal(res.Body.Bytes(), &data)
		if res.Code != 200 || len(data.Links) != 1 || data.Links[0].ID != test.want {
			t.Fatal(res.Body.String())
		}
	}
}

func TestOnlyOneSubscriptionManagerInUI(t *testing.T) {
	html, _ := os.ReadFile("web/admin.html")
	js, _ := os.ReadFile("web/app.js")
	for _, obsolete := range []string{"subHistoryRows", "loadSubscriptionHistory", "链接管理与拉取统计", "最近生成记录"} {
		if strings.Contains(string(html)+string(js), obsolete) {
			t.Errorf("obsolete duplicate panel: %s", obsolete)
		}
	}
	for _, control := range []string{"订阅管理", "data-history-reuse", "data-history-delete", "data-link-state", "data-link-archive", "data-link-restore", "已删除（回收站）", "usageSort", "clearSubHistory"} {
		if !strings.Contains(string(js), control) {
			t.Errorf("missing action %s", control)
		}
	}
}
