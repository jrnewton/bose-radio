package preset

import (
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := Station{
		Name:      "WMBR",
		StreamURL: "https://wmbr.org:8002/hi",
		ImageURL:  "http://art.example/wmbr.png",
	}
	blob, err := encodeStationData(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	sd, err := decodeStationData(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sd.Name != in.Name || sd.StreamURL != in.StreamURL || sd.ImageURL != in.ImageURL {
		t.Errorf("round trip mismatch: got %+v, want %+v", sd, in)
	}
}

func TestEncodeIsQuerySafe(t *testing.T) {
	// URL-safe base64 must avoid '+' (decoded as space by net/url), '/', and '='.
	blob, err := encodeStationData(Station{Name: "x", StreamURL: "http://x.example/" + strings.Repeat("z", 40)})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, bad := range []string{"+", "/", "="} {
		if strings.Contains(blob, bad) {
			t.Errorf("encoded blob contains %q (not query-safe): %s", bad, blob)
		}
	}
}

// TestDecodeBoseFixture pins compatibility with Bose's own standard-base64
// Orion "data" blob, taken verbatim from the reference repo's integration test
// tests/integration/http-client/get_orion_station.http.
func TestDecodeBoseFixture(t *testing.T) {
	const fixture = "eyJuYW1lIjoiRG9jIFJhZGlvIiwiaW1hZ2VVcmwiOiIiLCJzdHJlYW1VcmwiOiJodHRwOi8vMTkyLjAuMi4xMDo4MDAwL3N0cmVhbSJ9"
	sd, err := decodeStationData(fixture)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if sd.Name != "Doc Radio" {
		t.Errorf("name = %q, want %q", sd.Name, "Doc Radio")
	}
	if sd.StreamURL != "http://192.0.2.10:8000/stream" {
		t.Errorf("streamUrl = %q, want %q", sd.StreamURL, "http://192.0.2.10:8000/stream")
	}
	if sd.ImageURL != "" {
		t.Errorf("imageUrl = %q, want empty", sd.ImageURL)
	}
}

func TestDecodeBadData(t *testing.T) {
	if _, err := decodeStationData("!!!not base64!!!"); err == nil {
		t.Error("expected error for non-base64 input")
	}
	if _, err := decodeStationData("Zm9v"); err == nil { // decodes to "foo", not JSON
		t.Error("expected error for non-JSON payload")
	}
}
