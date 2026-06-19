package preset

import (
	"encoding/xml"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// The marge account document (`/streaming/account/{account}/full`).
//
// At boot the speaker fetches /full and reconciles its whole account state
// (sources + presets) against it. The XML body is decoded into a protobuf-backed
// model with REQUIRED fields; a missing required element aborts the sync and
// wipes the speaker's local presets (reference GH-269). So every field the
// reference emits is emitted here, non-empty — note the deliberate absence of
// `omitempty` on the struct fields below.
//
// We populate the 6 presets (each a well-formed LOCAL_INTERNET_RADIO block, the
// same source we serve in <sources>) so /full is authoritative and consistent —
// avoiding the ambiguity of whether an empty <presets> means "keep local" or
// "reconcile to zero". Built bodies are cached (stable ETag) until the config or
// requested account changes.

const fullXMLHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// Identity holds the device-specific values embedded in /full. Resolved lazily
// from the device's own :8090/info on the first /full request (by which time the
// speaker's app is necessarily up, so there's no boot race), falling back to the
// configured defaults if that fetch fails.
type Identity struct {
	DeviceID     string
	Name         string
	Serial       string
	Firmware     string
	ProductCode  string
	ProductLabel string
	IPAddress    string
}

// SetIdentity configures the /full device identity: defaults (flag fallbacks),
// the device-info URL to lazily fetch real values from ("" disables the fetch,
// e.g. in tests), and where to persist the last served body ("" disables).
func (s *Server) SetIdentity(defaults Identity, infoURL, lastFullPath string) {
	s.mu.Lock()
	s.idDefaults = defaults
	s.infoURL = infoURL
	s.lastFullPath = lastFullPath
	s.mu.Unlock()
}

func (s *Server) identity() Identity {
	s.idOnce.Do(func() {
		s.mu.RLock()
		id, infoURL := s.idDefaults, s.infoURL
		s.mu.RUnlock()
		if infoURL != "" {
			if fetched, ok := fetchDeviceInfo(infoURL); ok {
				id = mergeIdentity(id, fetched)
			}
		}
		s.idResolved = id
	})
	return s.idResolved
}

func mergeIdentity(base, over Identity) Identity {
	pick := func(a, b string) string {
		if b != "" {
			return b
		}
		return a
	}
	return Identity{
		DeviceID:     pick(base.DeviceID, over.DeviceID),
		Name:         pick(base.Name, over.Name),
		Serial:       pick(base.Serial, over.Serial),
		Firmware:     pick(base.Firmware, over.Firmware),
		ProductCode:  pick(base.ProductCode, over.ProductCode),
		ProductLabel: pick(base.ProductLabel, over.ProductLabel),
		IPAddress:    pick(base.IPAddress, over.IPAddress),
	}
}

// fetchDeviceInfo reads the speaker's :8090/info and maps it to an Identity.
func fetchDeviceInfo(url string) (Identity, bool) {
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return Identity{}, false
	}
	defer resp.Body.Close()

	var info struct {
		DeviceID   string `xml:"deviceID,attr"`
		Name       string `xml:"name"`
		Type       string `xml:"type"`
		Components []struct {
			Category        string `xml:"componentCategory"`
			SoftwareVersion string `xml:"softwareVersion"`
			SerialNumber    string `xml:"serialNumber"`
		} `xml:"components>component"`
		NetworkInfo []struct {
			IPAddress string `xml:"ipAddress"`
		} `xml:"networkInfo"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&info); err != nil {
		return Identity{}, false
	}

	id := Identity{
		DeviceID:     info.DeviceID,
		Name:         info.Name,
		ProductCode:  info.Type,
		ProductLabel: info.Type,
	}
	for _, c := range info.Components {
		switch c.Category {
		case "SCM":
			if f := strings.Fields(c.SoftwareVersion); len(f) > 0 {
				id.Firmware = f[0]
			}
		case "PackagedProduct":
			id.Serial = c.SerialNumber
		}
	}
	if len(info.NetworkInfo) > 0 {
		id.IPAddress = info.NetworkInfo[0].IPAddress
	}
	return id, true
}

// --- /full XML model (no omitempty on required fields) ---

type fullAccount struct {
	XMLName           xml.Name     `xml:"account"`
	ID                string       `xml:"id,attr"`
	AccountStatus     string       `xml:"accountStatus"`
	Devices           []fullDevice `xml:"devices>device"`
	Mode              string       `xml:"mode"`
	PreferredLanguage string       `xml:"preferredLanguage"`
	Sources           []fullSource `xml:"sources>source"`
}

type fullDevice struct {
	DeviceID        string       `xml:"deviceid,attr"`
	AttachedProduct fullAttached `xml:"attachedProduct"`
	CreatedOn       string       `xml:"createdOn"`
	FirmwareVersion string       `xml:"firmwareVersion"`
	IPAddress       string       `xml:"ipaddress"`
	Name            string       `xml:"name"`
	Presets         []fullPreset `xml:"presets>preset"`
	SerialNumber    string       `xml:"serialNumber"`
	UpdatedOn       string       `xml:"updatedOn"`
}

type fullAttached struct {
	ProductCode  string   `xml:"product_code,attr"`
	Components   struct{} `xml:"components"`
	ProductLabel string   `xml:"productlabel"`
	SerialNumber string   `xml:"serialnumber"`
	UpdatedOn    string   `xml:"updatedOn"`
}

type fullCredential struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type fullSource struct {
	ID               string         `xml:"id,attr"`
	Type             string         `xml:"type,attr"`
	CreatedOn        string         `xml:"createdOn"`
	Credential       fullCredential `xml:"credential"`
	Name             string         `xml:"name"`
	SourceProviderID string         `xml:"sourceproviderid"`
	SourceName       string         `xml:"sourcename"`
	SourceSettings   string         `xml:"sourceSettings"`
	UpdatedOn        string         `xml:"updatedOn"`
	Username         string         `xml:"username"`
}

type fullPreset struct {
	ButtonNumber    string     `xml:"buttonNumber,attr"`
	ContainerArt    string     `xml:"containerArt"`
	ContentItemType string     `xml:"contentItemType"`
	CreatedOn       string     `xml:"createdOn"`
	Location        string     `xml:"location"`
	Name            string     `xml:"name"`
	Source          fullSource `xml:"source"`
	UpdatedOn       string     `xml:"updatedOn"`
	Username        string     `xml:"username"`
}

// localRadioSource is the canonical LOCAL_INTERNET_RADIO (providerid 11) source
// block, used both account-level and nested in each preset.
func localRadioSource(now string) fullSource {
	return fullSource{
		ID:               "10003",
		Type:             "Audio",
		CreatedOn:        now,
		Credential:       fullCredential{Type: "token", Value: orionToken},
		Name:             "LOCAL_INTERNET_RADIO",
		SourceProviderID: "11",
		SourceName:       "",
		SourceSettings:   "",
		UpdatedOn:        now,
		Username:         "",
	}
}

func nowBose() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000") + "+00:00"
}

func (s *Server) buildFull(account string, cfg *Config, baseURL string, id Identity, now string) ([]byte, error) {
	dev := fullDevice{
		DeviceID: id.DeviceID,
		AttachedProduct: fullAttached{
			ProductCode:  id.ProductCode,
			ProductLabel: id.ProductLabel,
			SerialNumber: id.Serial,
			UpdatedOn:    now,
		},
		CreatedOn:       now,
		FirmwareVersion: id.Firmware,
		IPAddress:       id.IPAddress,
		Name:            id.Name,
		SerialNumber:    id.Serial,
		UpdatedOn:       now,
	}
	for i, st := range cfg.Stations {
		data, err := encodeStationData(st)
		if err != nil {
			return nil, err
		}
		dev.Presets = append(dev.Presets, fullPreset{
			ButtonNumber:    strconv.Itoa(i + 1),
			ContainerArt:    st.ImageURL,
			ContentItemType: "stationurl",
			CreatedOn:       now,
			Location:        baseURL + stationPath + "?data=" + data,
			Name:            st.Name,
			Source:          localRadioSource(now),
			UpdatedOn:       now,
			Username:        "",
		})
	}
	doc := fullAccount{
		ID:                account,
		AccountStatus:     "OK",
		Devices:           []fullDevice{dev},
		Mode:              "global",
		PreferredLanguage: "en",
		Sources:           []fullSource{localRadioSource(now)},
	}
	body, err := xml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append([]byte(fullXMLHeader), body...), nil
}

func (s *Server) handleAccountFull(w http.ResponseWriter, r *http.Request) {
	cfg, baseURL := s.snapshot()
	if cfg == nil {
		http.Error(w, "no config loaded", http.StatusServiceUnavailable)
		return
	}
	account := r.PathValue("account")
	fp := Fingerprint(cfg)

	// Cache the built body so the ETag is stable across polls (the timestamps
	// would otherwise change every request and force a re-sync each time).
	// Rebuild only when the requested account or the config changes.
	s.fullMu.Lock()
	if s.fullBody == nil || s.fullAccount != account || s.fullFP != fp {
		body, err := s.buildFull(account, cfg, baseURL, s.identity(), nowBose())
		if err != nil {
			s.fullMu.Unlock()
			http.Error(w, "failed to build full", http.StatusInternalServerError)
			return
		}
		s.fullAccount, s.fullFP, s.fullBody, s.fullETag = account, fp, body, makeETag(body)
		if s.lastFullPath != "" {
			_ = os.WriteFile(s.lastFullPath, body, 0o644)
		}
	}
	body, etag := s.fullBody, s.fullETag
	s.fullMu.Unlock()

	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", contentTypePresets)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(body)
}
