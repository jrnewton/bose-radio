package preset

import (
	"encoding/base64"
	"encoding/json"
)

// stationData is the JSON payload carried (base64-encoded) in the "data" query
// parameter of a preset's Orion station URL, and echoed back by the station
// handler as playback metadata. Field names match Bose's BMX Orion adapter.
type stationData struct {
	Name      string `json:"name"`
	ImageURL  string `json:"imageUrl"`
	StreamURL string `json:"streamUrl"`
}

// encodeStationData renders a station into the base64 "data" blob embedded in
// its preset location URL.
//
// We use URL-safe base64 (alphabet without '+' or '/', no '=' padding) so the
// value survives query parsing unharmed: net/url decodes '+' as a space, which
// would corrupt standard base64. We control both the encode (here) and decode
// (decodeStationData) sides, and the speaker relays the blob verbatim.
func encodeStationData(s Station) (string, error) {
	b, err := json.Marshal(stationData{Name: s.Name, ImageURL: s.ImageURL, StreamURL: s.StreamURL})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeStationData parses a "data" blob back into a stationData. It tolerates
// all four standard base64 alphabets/paddings, so it also accepts blobs minted
// in Bose's original standard-base64 Orion format, not just our own URL-safe one.
func decodeStationData(data string) (stationData, error) {
	var (
		raw []byte
		err error
	)
	for _, enc := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		if raw, err = enc.DecodeString(data); err == nil {
			break
		}
	}
	if err != nil {
		return stationData{}, err
	}
	var sd stationData
	if err := json.Unmarshal(raw, &sd); err != nil {
		return stationData{}, err
	}
	return sd, nil
}
