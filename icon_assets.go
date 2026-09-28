package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Icons are also compiled into the program. An upgrade can serve a newly
// bundled flag even before upstream rule synchronization succeeds.
//
//go:embed assets/icons assets/icon-manifest.json
var bundledIcons embed.FS

type bundledIconInfo struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

var bundledIconInventory = func() map[string]bundledIconInfo {
	data, err := bundledIcons.ReadFile("assets/icon-manifest.json")
	if err != nil {
		panic(err)
	}
	var manifest struct {
		Files map[string]bundledIconInfo `json:"files"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil || len(manifest.Files) == 0 {
		panic("invalid bundled icon inventory")
	}
	return manifest.Files
}()

func serveBundledIcon(w http.ResponseWriter, r *http.Request) bool {
	name := strings.TrimPrefix(r.URL.Path, "/_assets/icons/")
	info, exists := bundledIconInventory[name]
	if !exists || !strings.HasPrefix(r.URL.Path, "/_assets/icons/") {
		return false
	}
	data, err := bundledIcons.ReadFile("assets/icons/" + name)
	if err != nil {
		http.Error(w, "bundled icon unavailable", http.StatusServiceUnavailable)
		return true
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600, stale-if-error=86400")
	w.Header().Set("ETag", `"`+info.SHA256+`"`)
	http.ServeContent(w, r, filepath.Base(name), time.Time{}, bytes.NewReader(data))
	return true
}

func (s *server) iconStatus() map[string]any {
	mirrored, countries, size := 0, 0, int64(0)
	for name, info := range bundledIconInventory {
		size += info.Bytes
		if strings.HasPrefix(name, "flags/") {
			countries++
		}
		data, err := os.ReadFile(filepath.Join(s.dataDir, "current", "_assets", "icons", filepath.FromSlash(name)))
		if err == nil && int64(len(data)) == info.Bytes && routingSHA256(data) == info.SHA256 {
			mirrored++
		}
	}
	expected := len(bundledIconInventory)
	return map[string]any{"ok": true, "cached": expected, "expected": expected, "bytes": size,
		"source": "bundled", "countries": countries, "mirrored": mirrored, "mirrored_ok": mirrored == expected}
}
