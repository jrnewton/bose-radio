package preset

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSourceProviders(t *testing.T) {
	srv := NewServer("http://127.0.0.1:8000", testConfig())
	rr := bmxGet(t, srv, http.MethodGet, sourcesPath)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "xml") {
		t.Errorf("content-type = %q, want xml", ct)
	}
	if rr.Header().Get("ETag") == "" {
		t.Error("missing ETag")
	}

	var doc margeSourceProviders
	if err := xml.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("sourceproviders not valid XML: %v", err)
	}
	byID := map[int]string{}
	for _, p := range doc.Providers {
		byID[p.ID] = p.Name
	}
	// The one our presets need, plus a couple the reference's own fixture checks.
	if byID[11] != "LOCAL_INTERNET_RADIO" {
		t.Errorf("provider 11 = %q, want LOCAL_INTERNET_RADIO", byID[11])
	}
	if byID[25] != "TUNEIN" || byID[15] != "SPOTIFY" {
		t.Errorf("expected TUNEIN(25) and SPOTIFY(15); got 25=%q 15=%q", byID[25], byID[15])
	}
}

func TestSourceProvidersConditional(t *testing.T) {
	srv := NewServer("http://127.0.0.1:8000", testConfig())
	rr1 := bmxGet(t, srv, http.MethodGet, sourcesPath)
	etag := rr1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	req := httptest.NewRequest(http.MethodGet, sourcesPath, nil)
	req.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2, req)
	if rr2.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", rr2.Code)
	}
}

func TestPowerOn(t *testing.T) {
	srv := NewServer("http://127.0.0.1:8000", testConfig())
	req := httptest.NewRequest(http.MethodPost, powerOnPath, strings.NewReader("<powerOn/>"))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("power_on status = %d, want 200", rr.Code)
	}
}

func TestBlacklist(t *testing.T) {
	srv := NewServer("http://127.0.0.1:8000", testConfig())
	rr := bmxGet(t, srv, http.MethodGet, "/v1/blacklist/B0D5CC1918A7")
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("blacklist status = %d, want 405 (upstream behavior)", rr.Code)
	}
}
