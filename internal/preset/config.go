// Package preset implements a minimal, dependency-free service that makes the
// six physical preset buttons on a Bose SoundTouch 10 play hardcoded internet
// radio streams after Bose's cloud shutdown.
//
// The speaker fetches its presets from the (now-dead) Bose "marge" streaming
// endpoint and, when a button is pressed, resolves the preset's ContentItem
// against a Bose "Orion" BMX adapter URL. This package emulates just those two
// endpoints over plain HTTP, serving stations from a small operator-editable
// config file. See server.go for the protocol details.
package preset

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

// MaxPresets is the number of physical preset buttons on the SoundTouch 10.
const MaxPresets = 6

// Station maps one preset button to one stream.
type Station struct {
	Name      string // display name, e.g. "WGBH"
	StreamURL string // direct audio stream the speaker will open
	ImageURL  string // optional artwork URL ("" if unset)
}

// Config is the ordered set of button mappings. Index 0 is button 1.
type Config struct {
	Stations []Station
}

// ParseConfig reads the pipe-delimited config format:
//
//	# comment
//	WGBH | http://stream.example/wgbh.mp3
//	WMBR | http://stream.example/wmbr.mp3 | http://art.example/wmbr.png
//
// Rules:
//   - One station per non-empty, non-comment line; button order = line order.
//   - Fields are separated by '|' and trimmed of surrounding whitespace.
//   - The first field (name) and second field (stream URL) are required; the
//     third field (image URL) is optional.
//   - More than MaxPresets stations is an error, so a stray line can't silently
//     shift or drop a button.
func ParseConfig(r io.Reader) (*Config, error) {
	var cfg Config
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		fields := strings.Split(raw, "|")
		if len(fields) < 2 {
			return nil, fmt.Errorf("line %d: expected \"name | stream-url\", got %q", line, raw)
		}
		st := Station{
			Name:      strings.TrimSpace(fields[0]),
			StreamURL: strings.TrimSpace(fields[1]),
		}
		if len(fields) >= 3 {
			st.ImageURL = strings.TrimSpace(fields[2])
		}
		if st.Name == "" {
			return nil, fmt.Errorf("line %d: empty station name", line)
		}
		if err := validateStreamURL(st.StreamURL); err != nil {
			return nil, fmt.Errorf("line %d (%s): %w", line, st.Name, err)
		}
		cfg.Stations = append(cfg.Stations, st)
		if len(cfg.Stations) > MaxPresets {
			return nil, fmt.Errorf("line %d: more than %d stations defined", line, MaxPresets)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validateStreamURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("invalid stream URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("stream URL must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("stream URL has no host")
	}
	return nil
}

// Source records where a loaded Config came from, for logging.
type Source string

const (
	SourceUSB   Source = "usb"
	SourceCache Source = "cache"
	SourceNone  Source = "none"
)

// Load reads the config, preferring the USB stick at usbPath and falling back
// to the persistent cache at cachePath.
//
// When the USB file is present and valid, it is copied to cachePath so the most
// recent good config survives the stick being removed: the stick is an optional
// config-injection medium, not a hard runtime dependency. If the USB file is
// present but invalid, the last known-good cache is used and left untouched.
func Load(usbPath, cachePath string) (*Config, Source, error) {
	if data, err := os.ReadFile(usbPath); err == nil {
		if cfg, perr := ParseConfig(strings.NewReader(string(data))); perr == nil {
			// Best-effort cache refresh; a write failure must not break startup.
			_ = os.WriteFile(cachePath, data, 0o644)
			return cfg, SourceUSB, nil
		}
		// USB present but invalid: fall back to the last known-good cache.
	}
	return loadCache(cachePath)
}

func loadCache(cachePath string) (*Config, Source, error) {
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, SourceNone, fmt.Errorf("no config on usb or cache: %w", err)
	}
	cfg, err := ParseConfig(strings.NewReader(string(data)))
	if err != nil {
		return nil, SourceNone, fmt.Errorf("cache config invalid: %w", err)
	}
	return cfg, SourceCache, nil
}

// WaitForFile polls for path until it exists or timeout elapses, returning true
// if it appeared. The USB mount at /media/sda1 is performed asynchronously by
// udev and races service startup, so we give it time to settle before reading.
func WaitForFile(path string, timeout, interval time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}

// Fingerprint returns a stable string identifying the config contents, for
// cheap change detection across reloads.
func Fingerprint(c *Config) string {
	var b strings.Builder
	for _, s := range c.Stations {
		b.WriteString(s.Name)
		b.WriteByte('|')
		b.WriteString(s.StreamURL)
		b.WriteByte('|')
		b.WriteString(s.ImageURL)
		b.WriteByte('\n')
	}
	return b.String()
}
