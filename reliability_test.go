package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLoginLimitsAcrossConnections(t *testing.T) {
	for _, addresses := range [][]string{{"192.0.2.1:10001", "192.0.2.1:10002"}, {"[2001:db8::1]:10001", "[2001:db8::1]:10002"}} {
		s := &server{dataDir: t.TempDir(), adminPassword: "correct"}
		for i, addr := range addresses {
			r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"wrong"}`))
			r.RemoteAddr = addr
			w := httptest.NewRecorder()
			s.login(w, r)
			want := []int{http.StatusUnauthorized, http.StatusTooManyRequests}[i]
			if w.Code != want {
				t.Fatalf("%s: got %d, want %d", addr, w.Code, want)
			}
		}
	}
}

func TestNodeCountAcceptsYAMLStyles(t *testing.T) {
	for _, config := range []string{
		"proxies: [{name: node, type: ss}]\n",
		"proxies:\n- type: ss\n  name: node\n",
		"proxies:\n  - type: ss\n    name: node\n",
	} {
		for _, target := range []string{"clash", "clashr", "stash"} {
			if got := convertedNodeCount(target, []byte(config)); got != 1 {
				t.Fatalf("%s valid YAML counted as %d: %q", target, got, config)
			}
		}
	}
}

func TestRegenerateDisabledSubscription(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("proxies: [{name: test, type: ss, server: 1.1.1.1, port: 443}]\n"))
	}))
	defer backend.Close()
	for _, state := range []string{`{"disabled":true}`, `{"archived":true}`} {
		t.Run(state, func(t *testing.T) {
			s := &server{dataDir: t.TempDir(), domain: "rules.example", subconverterURL: backend.URL}
			generate := func() *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				s.createSubscriptionLinkV2(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"target":"clash","url":"https://1.1.1.1/sub"}`)))
				return w
			}
			w := generate()
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			defer s.usageDB.Close()
			var result struct{ URL string }
			json.Unmarshal(w.Body.Bytes(), &result)
			r := httptest.NewRequest("PUT", "/", strings.NewReader(state))
			r.SetPathValue("id", usageID(result.URL))
			s.setSubscriptionDisabled(httptest.NewRecorder(), r)
			w = generate()
			if w.Code != 409 || !strings.Contains(w.Body.String(), "恢复") {
				t.Fatalf("generated unusable link: %d %s", w.Code, w.Body.String())
			}
			if len(s.readSubscriptionHistory()) != 1 {
				t.Fatal("failed generation added history")
			}
			_, disabled, err := s.startSubscriptionAccess(result.URL, "Clash")
			if err != nil || !disabled {
				t.Fatal("generation changed disabled state")
			}
		})
	}
}

func TestRollbackSharesReleaseLock(t *testing.T) {
	s := &server{dataDir: t.TempDir()}
	commit := strings.Repeat("a", 40)
	if err := os.MkdirAll(filepath.Join(s.dataDir, "releases", commit), 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := s.lockRelease()
	if err != nil {
		t.Fatal(err)
	}
	rollback := func() int {
		w := httptest.NewRecorder()
		s.rollback(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"commit":"`+commit+`"}`)))
		return w.Code
	}
	if got := rollback(); got != 409 {
		t.Fatalf("rollback while locked: %d", got)
	}
	lock.Close()
	if got := rollback(); got != 200 {
		t.Fatalf("rollback after unlock: %d", got)
	}
}

// The child is killed without cleanup: the lock file deliberately remains.
func TestReleaseLockSurvivesProcessDeath(t *testing.T) {
	if dir := os.Getenv("CORALBAY_TEST_LOCK_CHILD"); dir != "" {
		s := &server{dataDir: dir}
		lock, err := s.lockRelease()
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		os.Stdout.Write([]byte("locked\n"))
		time.Sleep(time.Minute)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestReleaseLockSurvivesProcessDeath$")
	cmd.Env = append(os.Environ(), "CORALBAY_TEST_LOCK_CHILD="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	buf := make([]byte, 7)
	if _, err = io.ReadFull(out, buf); err != nil || string(buf) != "locked\n" {
		t.Fatalf("child not ready: %q %v", buf, err)
	}
	s := &server{dataDir: dir}
	if lock, err := s.lockRelease(); err == nil {
		lock.Close()
		t.Fatal("concurrent process acquired lock")
	} else if !errors.Is(err, unix.EWOULDBLOCK) {
		t.Fatal(err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	lock, err := s.lockRelease()
	if err != nil {
		t.Fatalf("dead process left lock held: %v", err)
	}
	lock.Close()
}
