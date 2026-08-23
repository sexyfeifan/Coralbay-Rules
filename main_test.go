package main

import "testing"

func TestNewerVersion(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"v3.0.1", "v3.0.0", true},
		{"v3.1.0", "v3.0.9", true},
		{"v4.0.0", "v3.9.9", true},
		{"v3.0.0", "v3.0.0", false},
		{"v2.1.0", "v3.0.0", false},
	}
	for _, test := range tests {
		if got := newerVersion(test.candidate, test.current); got != test.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", test.candidate, test.current, got, test.want)
		}
	}
}

func TestReadableSource(t *testing.T) {
	tests := map[string]string{
		"mihomo/domain/Google.mrs": "site/google.txt",
		"mihomo/ip/Google.mrs":     "ip/google.txt",
		"mihomo/domain/Emby.mrs":   "",
		"../../etc/passwd":         "",
	}
	for path, want := range tests {
		if got := readableSource(path); got != want {
			t.Errorf("readableSource(%q) = %q, want %q", path, got, want)
		}
	}
}
