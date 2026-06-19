package preset

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testConfig() *Config {
	return &Config{Stations: []Station{
		{Name: "WCUW", StreamURL: "https://peridot.streamguys1.com:5495/live"},
		{Name: "WGBH", StreamURL: "http://wgbh-live.streamguys1.com/wgbh"},
		{Name: "WHRB", StreamURL: "http://stream.whrb.org/whrb-mp3"},
	}}
}

func TestHandlePresets(t *testing.T) {
	srv := NewServer("http://svc", testConfig())
	req := httptest.NewRequest(http.MethodGet, "/streaming/account/1234567/device/AABBCCDDEEFF/presets", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "xml") {
		t.Errorf("content-type = %q, want xml", ct)
	}
	if rr.Header().Get("ETag") == "" {
		t.Error("missing ETag header")
	}

	var doc xmlPresets
	if err := xml.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal presets: %v\nbody: %s", err, rr.Body.String())
	}
	if doc.DeviceID != "AABBCCDDEEFF" {
		t.Errorf("deviceID = %q, want echoed AABBCCDDEEFF", doc.DeviceID)
	}
	if len(doc.Presets) != 3 {
		t.Fatalf("got %d presets, want 3", len(doc.Presets))
	}
	for i, p := range doc.Presets {
		if p.ID != i+1 {
			t.Errorf("preset %d id = %d, want %d", i, p.ID, i+1)
		}
		ci := p.ContentItem
		if ci.Source != sourceLocalRadio {
			t.Errorf("preset %d source = %q, want %q", i, ci.Source, sourceLocalRadio)
		}
		if !ci.IsPresetable {
			t.Errorf("preset %d not presetable", i)
		}
		wantPrefix := "http://svc" + stationPath + "?data="
		if !strings.HasPrefix(ci.Location, wantPrefix) {
			t.Fatalf("preset %d location = %q, want prefix %q", i, ci.Location, wantPrefix)
		}
		// The embedded data blob must round-trip back to this station.
		u, err := url.Parse(ci.Location)
		if err != nil {
			t.Fatalf("preset %d bad location: %v", i, err)
		}
		sd, err := decodeStationData(u.Query().Get("data"))
		if err != nil {
			t.Fatalf("preset %d decode data: %v", i, err)
		}
		if sd.StreamURL != testConfig().Stations[i].StreamURL {
			t.Errorf("preset %d streamUrl = %q, want %q", i, sd.StreamURL, testConfig().Stations[i].StreamURL)
		}
		if ci.ItemName != testConfig().Stations[i].Name {
			t.Errorf("preset %d itemName = %q, want %q", i, ci.ItemName, testConfig().Stations[i].Name)
		}
	}
}

func TestHandlePresetsConditional(t *testing.T) {
	srv := NewServer("http://svc", testConfig())
	path := "/streaming/account/1/device/DEV/presets"

	rr1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr1, httptest.NewRequest(http.MethodGet, path, nil))
	etag := rr1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on first fetch")
	}

	req2 := httptest.NewRequest(http.MethodGet, path, nil)
	req2.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", rr2.Code)
	}
	if rr2.Body.Len() != 0 {
		t.Errorf("304 body should be empty, got %d bytes", rr2.Body.Len())
	}
}

func TestHandlePresetsNoConfig(t *testing.T) {
	srv := NewServer("http://svc", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/streaming/account/1/device/DEV/presets", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestHandleStation(t *testing.T) {
	srv := NewServer("http://svc", testConfig())
	blob, err := encodeStationData(Station{Name: "WOMR", StreamURL: "http://womr.streamguys1.com/live"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, stationPath+"?data="+blob, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var pr playbackResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &pr); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, rr.Body.String())
	}
	if pr.Audio.StreamURL != "http://womr.streamguys1.com/live" {
		t.Errorf("streamUrl = %q", pr.Audio.StreamURL)
	}
	if pr.Name != "WOMR" {
		t.Errorf("name = %q, want WOMR", pr.Name)
	}
	if pr.StreamType != "liveRadio" {
		t.Errorf("streamType = %q, want liveRadio", pr.StreamType)
	}
	if !pr.Audio.HasPlaylist || !pr.Audio.IsRealtime {
		t.Errorf("audio flags = %+v, want both true", pr.Audio)
	}
	// The response must carry a streams[] array mirroring streamUrl (the
	// firmware reads streams[] for the playable list).
	if len(pr.Audio.Streams) != 1 || pr.Audio.Streams[0].StreamURL != "http://womr.streamguys1.com/live" {
		t.Errorf("audio.streams = %+v, want one entry mirroring streamUrl", pr.Audio.Streams)
	}
}

// TestHandleStationBoseFixture replays the reference repo's exact Orion request
// (get_orion_station.http) and asserts the documented contract: the response
// echoes the decoded streamUrl and name.
func TestHandleStationBoseFixture(t *testing.T) {
	const fixture = "eyJuYW1lIjoiRG9jIFJhZGlvIiwiaW1hZ2VVcmwiOiIiLCJzdHJlYW1VcmwiOiJodHRwOi8vMTkyLjAuMi4xMDo4MDAwL3N0cmVhbSJ9"
	srv := NewServer("http://svc", testConfig())
	req := httptest.NewRequest(http.MethodGet, stationPath+"?data="+fixture, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var pr playbackResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &pr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pr.Audio.StreamURL != "http://192.0.2.10:8000/stream" {
		t.Errorf("streamUrl = %q, want fixture stream", pr.Audio.StreamURL)
	}
	if pr.Name != "Doc Radio" {
		t.Errorf("name = %q, want Doc Radio", pr.Name)
	}
}

func TestHandleStationBadRequests(t *testing.T) {
	srv := NewServer("http://svc", testConfig())
	cases := map[string]string{
		"missing data": stationPath,
		"bad base64":   stationPath + "?data=!!!",
		"not json":     stationPath + "?data=Zm9v",
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rr.Code)
			}
		})
	}
}

func TestHandleTelemetry(t *testing.T) {
	srv := NewServer("http://svc", testConfig())
	req := httptest.NewRequest(http.MethodPost, "/v1/scmudc/AABBCCDDEEFF", strings.NewReader(`{"event":"preset"}`))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("telemetry status = %d, want 200", rr.Code)
	}
}
