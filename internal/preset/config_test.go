package preset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseConfigValid(t *testing.T) {
	in := `
# leading comment
WCUW | https://peridot.streamguys1.com:5495/live

WGBH | http://wgbh-live.streamguys1.com/wgbh | http://art.example/wgbh.png
   WOMR    |   http://womr.streamguys1.com/live
`
	cfg, err := ParseConfig(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.Stations) != 3 {
		t.Fatalf("got %d stations, want 3", len(cfg.Stations))
	}
	want := []Station{
		{Name: "WCUW", StreamURL: "https://peridot.streamguys1.com:5495/live"},
		{Name: "WGBH", StreamURL: "http://wgbh-live.streamguys1.com/wgbh", ImageURL: "http://art.example/wgbh.png"},
		{Name: "WOMR", StreamURL: "http://womr.streamguys1.com/live"},
	}
	for i, w := range want {
		if cfg.Stations[i] != w {
			t.Errorf("station %d = %+v, want %+v", i, cfg.Stations[i], w)
		}
	}
}

func TestParseConfigErrors(t *testing.T) {
	cases := map[string]string{
		"missing url": "WCUW",
		"empty name":  " | http://x.example/s",
		"bad scheme":  "WCUW | ftp://x.example/s",
		"no host":     "WCUW | http:///s",
		"too many":    "a|http://x/1\nb|http://x/2\nc|http://x/3\nd|http://x/4\ne|http://x/5\nf|http://x/6\ng|http://x/7",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig(strings.NewReader(in)); err == nil {
				t.Fatalf("expected error for %q, got nil", in)
			}
		})
	}
}

func TestParseConfigExactlyMax(t *testing.T) {
	var b strings.Builder
	for range MaxPresets {
		b.WriteString("s | http://x.example/s\n")
	}
	cfg, err := ParseConfig(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.Stations) != MaxPresets {
		t.Fatalf("got %d, want %d", len(cfg.Stations), MaxPresets)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadUSBValidWritesCache(t *testing.T) {
	dir := t.TempDir()
	usb := filepath.Join(dir, "usb.conf")
	cache := filepath.Join(dir, "cache.conf")
	writeFile(t, usb, "WCUW | http://x.example/cuw\n")

	cfg, src, err := Load(usb, cache)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if src != SourceUSB {
		t.Errorf("source = %q, want usb", src)
	}
	if len(cfg.Stations) != 1 || cfg.Stations[0].Name != "WCUW" {
		t.Errorf("unexpected config: %+v", cfg.Stations)
	}
	// USB load must refresh the persistent cache.
	if _, err := os.Stat(cache); err != nil {
		t.Errorf("cache not written: %v", err)
	}
}

func TestLoadUSBInvalidFallsBackToCache(t *testing.T) {
	dir := t.TempDir()
	usb := filepath.Join(dir, "usb.conf")
	cache := filepath.Join(dir, "cache.conf")
	writeFile(t, cache, "WGBH | http://x.example/gbh\n")
	writeFile(t, usb, "this is not valid\n") // single field -> parse error

	cfg, src, err := Load(usb, cache)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if src != SourceCache {
		t.Errorf("source = %q, want cache", src)
	}
	if cfg.Stations[0].Name != "WGBH" {
		t.Errorf("expected cached WGBH, got %+v", cfg.Stations)
	}
	// The good cache must be left untouched by an invalid USB file.
	data, _ := os.ReadFile(cache)
	if !strings.Contains(string(data), "WGBH") {
		t.Errorf("cache was clobbered: %q", data)
	}
}

func TestLoadUSBAbsentUsesCache(t *testing.T) {
	dir := t.TempDir()
	usb := filepath.Join(dir, "absent.conf")
	cache := filepath.Join(dir, "cache.conf")
	writeFile(t, cache, "WHRB | http://stream.whrb.org/whrb-mp3\n")

	cfg, src, err := Load(usb, cache)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if src != SourceCache {
		t.Errorf("source = %q, want cache", src)
	}
	if cfg.Stations[0].Name != "WHRB" {
		t.Errorf("got %+v", cfg.Stations)
	}
}

func TestLoadNothingIsError(t *testing.T) {
	dir := t.TempDir()
	_, _, err := Load(filepath.Join(dir, "no-usb"), filepath.Join(dir, "no-cache"))
	if err == nil {
		t.Fatal("expected error when neither usb nor cache exists")
	}
}

func TestWaitForFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "appears.conf")
	go func() {
		time.Sleep(80 * time.Millisecond)
		writeFile(t, path, "x | http://x.example/x\n")
	}()
	if !WaitForFile(path, 2*time.Second, 10*time.Millisecond) {
		t.Error("WaitForFile returned false for a file that appears")
	}
	if WaitForFile(filepath.Join(dir, "never"), 60*time.Millisecond, 10*time.Millisecond) {
		t.Error("WaitForFile returned true for a file that never appears")
	}
}

func TestFingerprintChanges(t *testing.T) {
	a := &Config{Stations: []Station{{Name: "A", StreamURL: "http://x/a"}}}
	b := &Config{Stations: []Station{{Name: "A", StreamURL: "http://x/b"}}}
	if Fingerprint(a) == Fingerprint(b) {
		t.Error("fingerprints should differ when stream URL differs")
	}
	a2 := &Config{Stations: []Station{{Name: "A", StreamURL: "http://x/a"}}}
	if Fingerprint(a) != Fingerprint(a2) {
		t.Error("fingerprint should be stable for equal configs")
	}
}
