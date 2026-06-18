package preset

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func bmxGet(t *testing.T, srv *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(method, path, nil))
	return rr
}

func TestBMXRegistryAdvertisesOrion(t *testing.T) {
	srv := NewServer("http://127.0.0.1:8000", testConfig())
	rr := bmxGet(t, srv, http.MethodGet, bmxRegistryServicesPath)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	// Placeholders must be substituted with our base URL.
	if strings.Contains(body, "{BMX_SERVER}") || strings.Contains(body, "{MEDIA_SERVER}") {
		t.Errorf("unsubstituted template placeholder in registry response")
	}

	var reg struct {
		Services []struct {
			BaseURL string `json:"baseUrl"`
			ID      struct {
				Name string `json:"name"`
			} `json:"id"`
		} `json:"bmx_services"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &reg); err != nil {
		t.Fatalf("registry is not valid JSON: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("got %d services, want 1 (orion)", len(reg.Services))
	}
	if reg.Services[0].ID.Name != "LOCAL_INTERNET_RADIO" {
		t.Errorf("service name = %q, want LOCAL_INTERNET_RADIO", reg.Services[0].ID.Name)
	}
	wantBase := "http://127.0.0.1:8000/core02/svc-bmx-adapter-orion/prod/orion"
	if reg.Services[0].BaseURL != wantBase {
		t.Errorf("baseUrl = %q, want %q", reg.Services[0].BaseURL, wantBase)
	}
}

func TestBMXAvailability(t *testing.T) {
	srv := NewServer("http://127.0.0.1:8000", testConfig())
	rr := bmxGet(t, srv, http.MethodGet, bmxRegistryAvailabilityPath)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var avail struct {
		Services []struct {
			Service string `json:"service"`
		} `json:"services"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &avail); err != nil {
		t.Fatalf("availability is not valid JSON: %v", err)
	}
	if len(avail.Services) != 1 || avail.Services[0].Service != "LOCAL_INTERNET_RADIO" {
		t.Errorf("availability = %+v, want one LOCAL_INTERNET_RADIO entry", avail.Services)
	}
}

func TestOrionToken(t *testing.T) {
	srv := NewServer("http://127.0.0.1:8000", testConfig())
	rr := bmxGet(t, srv, http.MethodPost, orionTokenPath)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &tok); err != nil {
		t.Fatalf("token is not valid JSON: %v", err)
	}
	if tok.AccessToken == "" {
		t.Error("access_token is empty; speaker needs a non-empty anonymous token")
	}
}

// TestBMXRouteDisambiguation pins the overlapping Orion paths: /station must
// reach the playback handler, /orion (self) the descriptor, and /orion/token the
// token handler — the routes must not shadow each other.
func TestBMXRouteDisambiguation(t *testing.T) {
	srv := NewServer("http://127.0.0.1:8000", testConfig())

	// /orion/station -> playback (has streamUrl, is NOT the registry descriptor).
	blob, err := encodeStationData(Station{Name: "X", StreamURL: "http://x.example/s"})
	if err != nil {
		t.Fatal(err)
	}
	rr := bmxGet(t, srv, http.MethodGet, stationPath+"?data="+blob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/station status = %d, want 200", rr.Code)
	}
	if body := rr.Body.String(); !strings.Contains(body, "streamUrl") || strings.Contains(body, "LOCAL_INTERNET_RADIO") {
		t.Errorf("/station routed to the wrong handler: %s", body)
	}

	// /orion (self) -> descriptor (contains LOCAL_INTERNET_RADIO), not playback.
	rr = bmxGet(t, srv, http.MethodGet, orionServicePath)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "LOCAL_INTERNET_RADIO") {
		t.Errorf("/orion self descriptor wrong: %d %s", rr.Code, rr.Body.String())
	}

	// /orion/token -> token via BOTH POST and GET (registered any-method).
	for _, m := range []string{http.MethodPost, http.MethodGet} {
		rr = bmxGet(t, srv, m, orionTokenPath)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "access_token") {
			t.Errorf("%s /token wrong: %d %s", m, rr.Code, rr.Body.String())
		}
	}
}

// TestBMXMethodGating confirms the GET-only registry routes reject other methods
// rather than silently serving the body.
func TestBMXMethodGating(t *testing.T) {
	srv := NewServer("http://127.0.0.1:8000", testConfig())
	for _, path := range []string{bmxRegistryServicesPath, bmxRegistryAvailabilityPath, orionServicePath} {
		rr := bmxGet(t, srv, http.MethodPost, path)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405 (GET-only route)", path, rr.Code)
		}
	}
}

func TestOrionServiceDescriptor(t *testing.T) {
	srv := NewServer("http://127.0.0.1:8000", testConfig())
	rr := bmxGet(t, srv, http.MethodGet, orionServicePath)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "{BMX_SERVER}") {
		t.Errorf("unsubstituted placeholder in orion service descriptor")
	}
	var svc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &svc); err != nil {
		t.Fatalf("orion descriptor is not valid JSON: %v", err)
	}
	if !strings.Contains(body, "LOCAL_INTERNET_RADIO") {
		t.Errorf("orion descriptor missing LOCAL_INTERNET_RADIO")
	}
}
