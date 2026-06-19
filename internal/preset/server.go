package preset

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Bose protocol constants (frozen in FW 27.x).
const (
	// presetsPathPattern is the marge/streaming endpoint the speaker polls for
	// its preset list. accountId and deviceId are wildcards we ignore/echo.
	presetsPathPattern = "GET /streaming/account/{account}/device/{device}/presets"
	// stationPath is the Orion BMX adapter route a LOCAL_INTERNET_RADIO preset
	// replays when its button is pressed; we decode its ?data= blob. Derived from
	// orionBasePath (bmx.go) so the route and the advertised baseUrl stay in sync.
	stationPath = orionBasePath + "/station"
	// telemetryPrefix swallows SCMUDC telemetry (preset-press events, volume,
	// etc.) so a failed POST can't stall playback.
	telemetryPrefix = "/v1/scmudc/"

	contentTypePresets = "application/vnd.bose.streaming-v1.2+xml"
	sourceLocalRadio   = "LOCAL_INTERNET_RADIO"
)

// Server emulates the two Bose cloud endpoints the preset flow touches. The
// presets body (minus the per-request deviceID) and its ETag are precomputed
// whenever the config or base URL changes, so request handling stays cheap on
// the embedded core. It is safe for concurrent use; SetConfig/SetBaseURL may be
// called while serving (e.g. after a USB config reload).
type Server struct {
	mu      sync.RWMutex
	cfg     *Config
	baseURL string // absolute base the speaker reaches us at, e.g. http://127.0.0.1:8000
	items   []byte // precomputed <preset>... elements for cfg+baseURL
	etag    string // precomputed ETag over items

	// /full device identity (see full.go): defaults + lazy :8090/info fetch.
	idDefaults   Identity
	infoURL      string
	lastFullPath string
	idOnce       sync.Once
	idResolved   Identity

	// /full cached body (stable ETag across polls; rebuilt on cfg/account change).
	fullMu      sync.Mutex
	fullAccount string
	fullFP      string
	fullBody    []byte
	fullETag    string

	// logReq gates per-request access logging (off by default — see SetLogRequests).
	logReq atomic.Bool
}

// SetLogRequests enables/disables per-request access logging. Off by default to
// keep the device syslog quiet; enable it to trace the boot/preset-press flow.
func (s *Server) SetLogRequests(on bool) { s.logReq.Store(on) }

// NewServer builds a Server. baseURL is the absolute address the speaker uses
// to reach this service; it is embedded in each preset's location URL.
func NewServer(baseURL string, cfg *Config) *Server {
	s := &Server{baseURL: strings.TrimRight(baseURL, "/")}
	s.SetConfig(cfg)
	return s
}

// SetConfig swaps the served config and re-renders the cached presets body.
func (s *Server) SetConfig(cfg *Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
	s.rerenderLocked()
}

// SetBaseURL updates the base URL embedded in preset locations and re-renders.
func (s *Server) SetBaseURL(baseURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.baseURL = strings.TrimRight(baseURL, "/")
	s.rerenderLocked()
}

func (s *Server) rerenderLocked() {
	items, err := renderPresetItems(s.cfg, s.baseURL)
	if err != nil {
		// encodeStationData only fails if JSON marshaling does, which it won't
		// for plain strings; degrade to empty rather than panic.
		items = nil
	}
	s.items = items
	s.etag = makeETag(items)
}

// snapshot returns the current config and base URL under the read lock.
func (s *Server) snapshot() (*Config, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, s.baseURL
}

// Handler returns the HTTP routes for the service.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(presetsPathPattern, s.handlePresets)
	mux.HandleFunc("GET "+stationPath, s.handleStation)
	// BMX registry + Orion adapter: makes LOCAL_INTERNET_RADIO a valid source so
	// the speaker will actually follow a preset's Orion location (see bmx.go).
	mux.HandleFunc("GET "+bmxRegistryServicesPath, s.handleBMXRegistry)
	mux.HandleFunc("GET "+bmxRegistryAvailabilityPath, s.handleBMXAvailability)
	mux.HandleFunc("GET "+orionServicePath, s.handleOrionService)
	mux.HandleFunc(orionTokenPath, s.handleOrionToken) // any method
	// Minimal marge surface the speaker calls at boot before it will resolve a
	// LOCAL_INTERNET_RADIO preset (see marge.go).
	mux.HandleFunc("GET "+sourcesPath, s.handleSourceProviders)
	mux.HandleFunc("GET /streaming/account/{account}/full", s.handleAccountFull)
	mux.HandleFunc("POST "+powerOnPath, s.handlePowerOn)
	mux.HandleFunc("GET "+blacklistPath, s.handleBlacklist)
	// Non-blocking boot-handshake calls, stubbed so they don't 404 (see marge.go).
	mux.HandleFunc("GET /streaming/device/{device}/streaming_token", s.handleStreamingToken)
	mux.HandleFunc("GET /streaming/account/{account}/provider_settings", s.handleProviderSettings)
	mux.HandleFunc("GET /streaming/account/{account}/device/{device}/group", s.handleDeviceGroup)
	mux.HandleFunc("GET /streaming/account/{account}/device/{device}/group/", s.handleDeviceGroup)
	mux.HandleFunc("GET /media/", s.handleMedia)       // placeholder icons (see bmx.go)
	mux.HandleFunc(telemetryPrefix, s.handleTelemetry) // subtree, any method
	mux.HandleFunc("/v1/scmudc", s.handleTelemetry)    // exact, any method
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok\n")
	})
	return s.logRequests(mux)
}

// statusRecorder captures the response status for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logRequests, when enabled via SetLogRequests, logs one line per request
// (method, path, status, and whether an Authorization header was present) to the
// device syslog (logread | grep preset-server) — invaluable for tracing the boot
// and preset-press flow. Disabled by default to avoid telemetry chatter; the
// recorder/log work is skipped entirely when off.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.logReq.Load() || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		auth := ""
		if r.Header.Get("Authorization") != "" {
			auth = " auth"
		}
		log.Printf("%s %s -> %d%s", r.Method, r.URL.Path, rec.status, auth)
	})
}

// --- presets endpoint ---

// xmlPresets is the response envelope. The handler composes the wrapper by hand
// (to inject the per-request deviceID cheaply over the precomputed items); this
// type mirrors that shape for decoding in tests.
type xmlPresets struct {
	XMLName  xml.Name    `xml:"presets"`
	DeviceID string      `xml:"deviceID,attr,omitempty"`
	Presets  []xmlPreset `xml:"preset"`
}

type xmlPreset struct {
	XMLName     xml.Name       `xml:"preset"`
	ID          int            `xml:"id,attr"`
	CreatedOn   int64          `xml:"createdOn,attr"`
	UpdatedOn   int64          `xml:"updatedOn,attr"`
	ContentItem xmlContentItem `xml:"ContentItem"`
}

type xmlContentItem struct {
	Source       string `xml:"source,attr"`
	Type         string `xml:"type,attr"`
	Location     string `xml:"location,attr"`
	IsPresetable bool   `xml:"isPresetable,attr"`
	ItemName     string `xml:"itemName"`
}

// renderPresetItems marshals the <preset> elements for a config, without the
// surrounding <presets> wrapper (which carries the per-request deviceID).
func renderPresetItems(cfg *Config, baseURL string) ([]byte, error) {
	if cfg == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	for i, st := range cfg.Stations {
		data, err := encodeStationData(st)
		if err != nil {
			return nil, err
		}
		b, err := xml.Marshal(xmlPreset{
			ID:        i + 1,
			CreatedOn: 0,
			UpdatedOn: 0,
			ContentItem: xmlContentItem{
				Source:       sourceLocalRadio,
				Type:         "stationurl",
				Location:     baseURL + stationPath + "?data=" + data,
				IsPresetable: true,
				ItemName:     st.Name,
			},
		})
		if err != nil {
			return nil, err
		}
		buf.Write(b)
	}
	return buf.Bytes(), nil
}

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg, items, etag := s.cfg, s.items, s.etag
	s.mu.RUnlock()

	if cfg == nil {
		http.Error(w, "no config loaded", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", contentTypePresets)
	// The speaker re-polls with its last ETag; answer 304 when unchanged.
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	buf.WriteString("<presets")
	if device := r.PathValue("device"); device != "" {
		buf.WriteString(` deviceID="`)
		_ = xml.EscapeText(&buf, []byte(device))
		buf.WriteString(`"`)
	}
	buf.WriteString(">")
	buf.Write(items)
	buf.WriteString("</presets>")
	_, _ = w.Write(buf.Bytes())
}

func makeETag(b []byte) string {
	h := fnv.New32a()
	_, _ = h.Write(b)
	return `"` + strconv.FormatUint(uint64(h.Sum32()), 16) + `"`
}

// --- station (Orion) endpoint ---

type playbackResponse struct {
	Audio      playbackAudio `json:"audio"`
	Name       string        `json:"name"`
	ImageURL   string        `json:"imageUrl"`
	StreamType string        `json:"streamType"`
}

type playbackAudio struct {
	HasPlaylist bool     `json:"hasPlaylist"`
	IsRealtime  bool     `json:"isRealtime"`
	StreamURL   string   `json:"streamUrl"`
	Streams     []stream `json:"streams"`
}

type stream struct {
	HasPlaylist bool   `json:"hasPlaylist"`
	IsRealtime  bool   `json:"isRealtime"`
	StreamURL   string `json:"streamUrl"`
}

func (s *Server) handleStation(w http.ResponseWriter, r *http.Request) {
	data := r.URL.Query().Get("data")
	if data == "" {
		http.Error(w, "missing data parameter", http.StatusBadRequest)
		return
	}
	sd, err := decodeStationData(data)
	if err != nil {
		http.Error(w, "invalid data parameter", http.StatusBadRequest)
		return
	}
	if sd.StreamURL == "" {
		http.Error(w, "station has no streamUrl", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Mirror Bose's BMX playback shape: a top-level streamUrl plus a parallel
	// streams[] array. The reference always populates streams[] (no omitempty),
	// and the firmware reads it for the playable list, so we include it too.
	_ = json.NewEncoder(w).Encode(playbackResponse{
		Audio: playbackAudio{
			HasPlaylist: true,
			IsRealtime:  true,
			StreamURL:   sd.StreamURL,
			Streams: []stream{{
				HasPlaylist: true,
				IsRealtime:  true,
				StreamURL:   sd.StreamURL,
			}},
		},
		Name:       sd.Name,
		ImageURL:   sd.ImageURL,
		StreamType: "liveRadio",
	})
}

// --- telemetry sink ---

func (s *Server) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	w.WriteHeader(http.StatusOK)
}
