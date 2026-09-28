package main

import (
	"bytes"
	"image/png"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledCountryAndMacroIconsComplete(t *testing.T) {
	count := 0
	if err := fs.WalkDir(bundledIcons, "assets/icons", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".png") {
			return nil
		}
		count++
		name := strings.TrimPrefix(path, "assets/icons/")
		info, exists := bundledIconInventory[name]
		data, err := bundledIcons.ReadFile(path)
		if err != nil || !exists || info.Bytes != int64(len(data)) || info.SHA256 != routingSHA256(data) {
			t.Fatalf("icon inventory mismatch: %s", name)
		}
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			t.Fatalf("invalid PNG %s: %v", name, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != len(bundledIconInventory) {
		t.Fatal("inventory contains unbundled icons")
	}
	for _, country := range templateGroupingCountries() {
		icon := templateGroupingIcon(country.Name)
		if icon == "Global.png" || bundledIconInventory[icon].Bytes == 0 {
			t.Fatalf("country %s %s has no national icon: %s", country.Code, country.Name, icon)
		}
	}
	for _, macro := range templateGroupingMacros {
		icon := templateGroupingIcon(macro.Name)
		if icon == "Global.png" || bundledIconInventory[icon].Bytes == 0 {
			t.Fatalf("macro %s has no local icon", macro.Code)
		}
	}
	for _, name := range []string{"广告拦截", "全球手动", "香港", "YouTube", "其他未识别"} {
		if bundledIconInventory[templateGroupingIcon(name)].Bytes == 0 {
			t.Fatal("legacy icon missing", name)
		}
	}
}

func TestBundledIconAvailableBeforeRuleSyncAndCacheRevalidation(t *testing.T) {
	s := &server{dataDir: t.TempDir()}
	r := httptest.NewRequest("GET", "/_assets/icons/flags/vn.png", nil)
	w := httptest.NewRecorder()
	s.publicFiles(w, r)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("first-start icon unavailable: %d", w.Code)
	}
	if w.Header().Get("ETag") != `"`+bundledIconInventory["flags/vn.png"].SHA256+`"` {
		t.Fatal("icon ETag missing")
	}
	r = httptest.NewRequest("GET", "/_assets/icons/flags/vn.png", nil)
	r.Header.Set("If-None-Match", w.Header().Get("ETag"))
	w = httptest.NewRecorder()
	s.publicFiles(w, r)
	if w.Code != 304 {
		t.Fatal("icon revalidation failed", w.Code)
	}
	r = httptest.NewRequest("HEAD", "/_assets/icons/flags/de.png", nil)
	w = httptest.NewRecorder()
	s.publicFiles(w, r)
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("icon HEAD failed")
	}
	for _, path := range []string{"/_assets/icons/", "/_assets/icons/flags/", "/_assets/icons/flags/missing.png", "/_assets/icons/../icon-manifest.json"} {
		w = httptest.NewRecorder()
		s.publicFiles(w, httptest.NewRequest("GET", path, nil))
		if w.Code == 200 {
			t.Fatal("unlisted bundled asset exposed", path)
		}
	}
	status := s.iconStatus()
	if status["ok"] != true || status["cached"] != len(bundledIconInventory) || status["mirrored"] != 0 {
		t.Fatal("first-start availability reported incorrectly", status)
	}
	for name := range bundledIconInventory {
		data, _ := bundledIcons.ReadFile("assets/icons/" + name)
		path := filepath.Join(s.dataDir, "current", "_assets", "icons", name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if status = s.iconStatus(); status["mirrored_ok"] != true || status["mirrored"] != len(bundledIconInventory) {
		t.Fatal("recursive mirror inventory incomplete", status)
	}
	if err := os.WriteFile(filepath.Join(s.dataDir, "current", "_assets", "icons", "flags", "vn.png"), []byte("invalid"), 0644); err != nil {
		t.Fatal(err)
	}
	status = s.iconStatus()
	if status["mirrored_ok"] != false || status["mirrored"] != len(bundledIconInventory)-1 || status["ok"] != true {
		t.Fatal("damaged mirror not detected or bundled availability lost", status)
	}
}
