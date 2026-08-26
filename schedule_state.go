package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func (s *server) scheduleDeadline(initial bool, interval time.Duration) time.Duration {
	now := time.Now()
	deadline := now.Add(interval)
	path := filepath.Join(s.dataDir, "schedule.json")
	if initial {
		if b, e := os.ReadFile(path); e == nil {
			var previous time.Time
			if json.Unmarshal(b, &previous) == nil && !previous.IsZero() {
				deadline = previous
			}
		}
	}
	s.mu.Lock()
	s.nextSync = deadline
	s.mu.Unlock()
	b, _ := json.Marshal(deadline)
	if e := os.WriteFile(path+".tmp", b, 0600); e == nil {
		if e = os.Rename(path+".tmp", path); e != nil {
			s.audit("schedule", "save-failed", "")
		}
	} else {
		s.audit("schedule", "save-failed", "")
	}
	d := deadline.Sub(now)
	if d < 0 {
		d = 0
	}
	return d
}
