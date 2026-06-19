package preset

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestEndToEnd drives the full device flow over real HTTP through the actual
// ServeMux: the speaker fetches its presets, then "presses" a button by
// fetching that preset's ContentItem location, and must receive playback JSON
// whose streamUrl is the one configured for that button.
func TestEndToEnd(t *testing.T) {
	cfg := &Config{Stations: []Station{
		{Name: "WCUW", StreamURL: "https://peridot.streamguys1.com:5495/live"},
		{Name: "WGBH", StreamURL: "http://wgbh-live.streamguys1.com/wgbh"},
		{Name: "WHRB", StreamURL: "http://stream.whrb.org/whrb-mp3"},
	}}

	srv := NewServer("http://placeholder", cfg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	// Locations must point back at the live test server.
	srv.SetBaseURL(ts.URL)

	// Step 1: speaker fetches presets from the marge endpoint.
	resp, err := http.Get(ts.URL + "/streaming/account/1234567/device/AABBCCDDEEFF/presets")
	if err != nil {
		t.Fatalf("GET presets: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presets status = %d", resp.StatusCode)
	}
	var doc xmlPresets
	if err := xml.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode presets: %v", err)
	}
	if len(doc.Presets) != len(cfg.Stations) {
		t.Fatalf("got %d presets, want %d", len(doc.Presets), len(cfg.Stations))
	}

	// Step 2: press each button -> fetch its location -> expect the right stream.
	for i, p := range doc.Presets {
		loc := p.ContentItem.Location
		r2, err := http.Get(loc)
		if err != nil {
			t.Fatalf("button %d GET %s: %v", i+1, loc, err)
		}
		body, _ := io.ReadAll(r2.Body)
		r2.Body.Close()
		if r2.StatusCode != http.StatusOK {
			t.Fatalf("button %d status = %d", i+1, r2.StatusCode)
		}
		var pr playbackResponse
		if err := json.Unmarshal(body, &pr); err != nil {
			t.Fatalf("button %d decode: %v\nbody: %s", i+1, err, body)
		}
		if pr.Audio.StreamURL != cfg.Stations[i].StreamURL {
			t.Errorf("button %d streamUrl = %q, want %q", i+1, pr.Audio.StreamURL, cfg.Stations[i].StreamURL)
		}
		if pr.Name != cfg.Stations[i].Name {
			t.Errorf("button %d name = %q, want %q", i+1, pr.Name, cfg.Stations[i].Name)
		}
	}
}

// TestEndToEndConditionalPoll verifies the speaker's re-poll gets a 304 when the
// preset list is unchanged.
func TestEndToEndConditionalPoll(t *testing.T) {
	srv := NewServer("http://placeholder", testConfig())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	srv.SetBaseURL(ts.URL)

	url := ts.URL + "/streaming/account/1/device/DEV/presets"
	r1, err := http.Get(url)
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	io.Copy(io.Discard, r1.Body)
	r1.Body.Close()
	etag := r1.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("If-None-Match", etag)
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("conditional GET: %v", err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", r2.StatusCode)
	}
}
