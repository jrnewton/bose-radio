package preset

import (
	"encoding/json"
	"encoding/xml"
	"hash/fnv"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Bose protocol constants (frozen in FW 27.x).
const (
	// presetsPathPattern is the marge/streaming endpoint the speaker polls for
	// its preset list. accountId and deviceId are wildcards we ignore/echo.
	presetsPathPattern = "GET /streaming/account/{account}/device/{device}/presets"
	// stationPath is the Orion BMX adapter route a LOCAL_INTERNET_RADIO preset
	// replays when its button is pressed; we decode its ?data= blob.
	stationPath = "/core02/svc-bmx-adapter-orion/prod/orion/station"
	// telemetryPrefix swallows SCMUDC telemetry (preset-press events, volume,
	// etc.) so a failed POST can't stall playback.
	telemetryPrefix = "/v1/scmudc/"

	contentTypePresets = "application/vnd.bose.streaming-v1.2+xml"
	sourceLocalRadio   = "LOCAL_INTERNET_RADIO"
)

// Server emulates the two Bose cloud endpoints the preset flow touches. It is
// safe for concurrent use; SetConfig/SetBaseURL may be called while serving
// (e.g. after a USB config reload).
type Server struct {
	mu      sync.RWMutex
	cfg     *Config
	baseURL string // absolute base the speaker reaches us at, e.g. http://127.0.0.1:8000
}

// NewServer builds a Server. baseURL is the absolute address the speaker uses
// to reach this service; it is embedded in each preset's location URL.
func NewServer(baseURL string, cfg *Config) *Server {
	return &Server{cfg: cfg, baseURL: strings.TrimRight(baseURL, "/")}
}

// SetConfig swaps the served config (used by the reload loop).
func (s *Server) SetConfig(cfg *Config) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

// SetBaseURL updates the base URL embedded in preset locations.
func (s *Server) SetBaseURL(baseURL string) {
	s.mu.Lock()
	s.baseURL = strings.TrimRight(baseURL, "/")
	s.mu.Unlock()
}

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
	mux.HandleFunc(telemetryPrefix, s.handleTelemetry) // subtree, any method
	mux.HandleFunc("/v1/scmudc", s.handleTelemetry)    // exact, any method
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}

// --- presets endpoint ---

type xmlPresets struct {
	XMLName  xml.Name    `xml:"presets"`
	DeviceID string      `xml:"deviceID,attr,omitempty"`
	Presets  []xmlPreset `xml:"preset"`
}

type xmlPreset struct {
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

// buildPresetsXML renders the presets resource for the given device. The
// returned bytes include the XML declaration. deviceID is echoed from the path.
func (s *Server) buildPresetsXML(deviceID string, cfg *Config, baseURL string) ([]byte, error) {
	doc := xmlPresets{DeviceID: deviceID}
	for i, st := range cfg.Stations {
		data, err := encodeStationData(st)
		if err != nil {
			return nil, err
		}
		doc.Presets = append(doc.Presets, xmlPreset{
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
	}
	body, err := xml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	cfg, baseURL := s.snapshot()
	if cfg == nil {
		http.Error(w, "no config loaded", http.StatusServiceUnavailable)
		return
	}
	body, err := s.buildPresetsXML(r.PathValue("device"), cfg, baseURL)
	if err != nil {
		http.Error(w, "failed to build presets", http.StatusInternalServerError)
		return
	}
	etag := makeETag(body)
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", contentTypePresets)
	// The speaker re-polls with its last ETag; answer 304 when unchanged.
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(body)
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
	_ = json.NewEncoder(w).Encode(playbackResponse{
		Audio: playbackAudio{
			HasPlaylist: true,
			IsRealtime:  true,
			StreamURL:   sd.StreamURL,
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
