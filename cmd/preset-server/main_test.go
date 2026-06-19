package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jrnewton/bose-radio/internal/preset"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustLoad(t *testing.T, usb, cache string) *preset.Config {
	t.Helper()
	res, err := preset.Load(usb, cache)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return res.Config
}

// TestReloadOnce exercises the CLI's reload logic: no-op when unchanged, swap on
// edit, and keep-running-config on load error.
func TestReloadOnce(t *testing.T) {
	dir := t.TempDir()
	usb := filepath.Join(dir, "presets.conf")
	cache := filepath.Join(dir, "cache.conf")
	write(t, usb, "WGBH | http://x.example/gbh\n")

	srv := preset.NewServer("http://svc", mustLoad(t, usb, cache))
	last := preset.Fingerprint(mustLoad(t, usb, cache))

	// Unchanged config -> no reload.
	if nl, changed := reloadOnce(srv, usb, cache, last); changed || nl != last {
		t.Errorf("unchanged config should not reload; got (%q, %v)", nl, changed)
	}

	// Edit the stick -> reload, fingerprint advances.
	write(t, usb, "WGBH | http://x.example/gbh\nWMBR | http://x.example/mbr\n")
	nl, changed := reloadOnce(srv, usb, cache, last)
	if !changed {
		t.Fatal("expected reload after editing the stick")
	}
	if nl == last {
		t.Error("fingerprint should change after an edit")
	}

	// Load error (neither usb nor cache) -> keep last, no change.
	empty := t.TempDir()
	if gl, ch := reloadOnce(srv, filepath.Join(empty, "x"), filepath.Join(empty, "y"), nl); ch || gl != nl {
		t.Errorf("on load error expect (last, false); got (%q, %v)", gl, ch)
	}
}
