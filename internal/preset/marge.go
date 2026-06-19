package preset

import (
	"encoding/xml"
	"io"
	"net/http"
)

// Minimal marge surface.
//
// At boot the speaker calls several marge (streaming) endpoints to establish its
// account state before it will start the Orion token/station playback flow. The
// critical one is /streaming/sourceproviders: until the account's provider
// catalog lists LOCAL_INTERNET_RADIO (id 11), the speaker validates the preset's
// source type via the BMX registry (-> PLAY_STATE) but never resolves the actual
// stream. We also answer the power_on and blacklist calls the device makes, so
// they don't 404. Shapes mirror the reference repo (handlers_marge.go,
// constants.StaticProviders).
const (
	powerOnPath   = "/streaming/support/power_on"
	sourcesPath   = "/streaming/sourceproviders"
	blacklistPath = "/v1/blacklist/{deviceId}"
)

type margeSourceProvider struct {
	ID        int    `xml:"id,attr"`
	CreatedOn string `xml:"createdOn"`
	Name      string `xml:"name"`
	UpdatedOn string `xml:"updatedOn"`
}

type margeSourceProviders struct {
	XMLName   xml.Name              `xml:"sourceProviders"`
	Providers []margeSourceProvider `xml:"sourceprovider"`
}

// staticProviders is the canonical Bose source-provider catalog, transcribed
// from the reference repo's constants.StaticProviders (id, name, timestamps).
// LOCAL_INTERNET_RADIO (11) is the one our presets need; the rest are included
// so the speaker sees the full catalog it expects.
var staticProviders = []margeSourceProvider{
	{1, "2012-09-19T12:43:00.000+00:00", "PANDORA", "2012-09-19T12:43:00.000+00:00"},
	{2, "2012-09-19T12:43:00.000+00:00", "INTERNET_RADIO", "2012-09-19T12:43:00.000+00:00"},
	{3, "2012-10-22T16:03:00.000+00:00", "OFF", "2012-10-22T16:03:00.000+00:00"},
	{4, "2012-10-22T16:04:00.000+00:00", "LOCAL", "2012-10-22T16:04:00.000+00:00"},
	{5, "2012-10-22T16:04:00.000+00:00", "AIRPLAY", "2012-10-22T16:04:00.000+00:00"},
	{6, "2012-10-22T16:04:00.000+00:00", "CURRATED_RADIO", "2012-10-22T16:04:00.000+00:00"},
	{7, "2012-10-22T16:04:00.000+00:00", "STORED_MUSIC", "2012-10-22T16:04:00.000+00:00"},
	{8, "2012-10-22T16:04:00.000+00:00", "SLAVE_SOURCE", "2012-10-22T16:04:00.000+00:00"},
	{9, "2012-10-22T16:04:00.000+00:00", "AUX", "2012-10-22T16:04:00.000+00:00"},
	{10, "2013-01-10T09:45:00.000+00:00", "RECOMMENDED_INTERNET_RADIO", "2013-01-10T09:45:00.000+00:00"},
	{11, "2013-01-10T09:45:00.000+00:00", "LOCAL_INTERNET_RADIO", "2013-01-10T09:45:00.000+00:00"},
	{12, "2013-01-10T09:45:00.000+00:00", "GLOBAL_INTERNET_RADIO", "2013-01-10T09:45:00.000+00:00"},
	{13, "2014-03-17T15:30:07.000+00:00", "HELLO", "2014-03-17T15:30:07.000+00:00"},
	{14, "2014-03-17T15:30:27.000+00:00", "DEEZER", "2014-03-17T15:30:27.000+00:00"},
	{15, "2014-03-17T15:30:27.000+00:00", "SPOTIFY", "2014-03-17T15:30:27.000+00:00"},
	{16, "2014-03-17T15:30:27.000+00:00", "IHEART", "2014-03-17T15:30:27.000+00:00"},
	{17, "2014-12-04T19:49:55.000+00:00", "SIRIUSXM", "2014-12-04T19:49:55.000+00:00"},
	{18, "2014-12-04T19:49:55.000+00:00", "GOOGLE_PLAY_MUSIC", "2014-12-04T19:49:55.000+00:00"},
	{19, "2014-12-04T19:49:55.000+00:00", "QQMUSIC", "2014-12-04T19:49:55.000+00:00"},
	{20, "2014-12-04T19:49:55.000+00:00", "AMAZON", "2014-12-04T19:49:55.000+00:00"},
	{21, "2015-07-13T12:00:00.000+00:00", "LOCAL_MUSIC", "2015-07-13T12:00:00.000+00:00"},
	{22, "2016-04-08T17:27:21.000+00:00", "WBMX", "2016-04-08T17:27:21.000+00:00"},
	{23, "2016-04-08T17:27:21.000+00:00", "SOUNDCLOUD", "2016-04-08T17:27:21.000+00:00"},
	{24, "2016-04-08T17:27:21.000+00:00", "TIDAL", "2016-04-08T17:27:21.000+00:00"},
	{25, "2016-04-08T17:27:21.000+00:00", "TUNEIN", "2016-04-08T17:27:21.000+00:00"},
	{26, "2016-06-17T18:00:54.000+00:00", "QPLAY", "2016-06-17T18:00:54.000+00:00"},
	{27, "2016-08-01T13:53:40.000+00:00", "JUKE", "2016-08-01T13:53:40.000+00:00"},
	{28, "2016-08-01T13:53:40.000+00:00", "BBC", "2016-08-01T13:53:40.000+00:00"},
	{29, "2016-08-01T13:53:40.000+00:00", "DARFM", "2016-08-01T13:53:40.000+00:00"},
	{30, "2016-08-01T13:53:40.000+00:00", "7DIGITAL", "2016-08-01T13:53:40.000+00:00"},
	{31, "2016-08-01T13:53:40.000+00:00", "SAAVN", "2016-08-01T13:53:40.000+00:00"},
	{32, "2016-08-01T13:53:40.000+00:00", "RDIO", "2016-08-01T13:53:40.000+00:00"},
	{33, "2016-10-26T14:42:49.000+00:00", "PHONE_MUSIC", "2016-10-26T14:42:49.000+00:00"},
	{34, "2017-12-04T19:18:47.000+00:00", "ALEXA", "2017-12-04T19:18:47.000+00:00"},
	{35, "2019-05-28T18:21:20.000+00:00", "RADIOPLAYER", "2019-05-28T18:21:20.000+00:00"},
	{36, "2019-05-28T18:21:41.000+00:00", "RADIO.COM", "2019-05-28T18:21:41.000+00:00"},
	{37, "2019-06-13T17:30:47.000+00:00", "RADIO_COM", "2019-06-13T17:30:47.000+00:00"},
	{38, "2019-11-25T18:00:33.000+00:00", "SIRIUSXM_EVEREST", "2019-11-25T18:00:33.000+00:00"},
	{39, "2026-03-14T22:47:00.000+00:00", "RADIO_BROWSER", "2026-03-14T22:47:00.000+00:00"},
	{40, "2012-10-22T16:04:00.000+00:00", "BLUETOOTH", "2012-10-22T16:04:00.000+00:00"},
	{41, "2012-10-22T16:04:00.000+00:00", "BMX", "2012-10-22T16:04:00.000+00:00"},
	{42, "2012-10-22T16:04:00.000+00:00", "NOTIFICATION", "2012-10-22T16:04:00.000+00:00"},
	{43, "2012-10-22T16:04:00.000+00:00", "AUX_IN", "2012-10-22T16:04:00.000+00:00"},
}

// sourceProvidersBody/ETag are precomputed once — the catalog is static.
var (
	sourceProvidersBody = renderSourceProviders()
	sourceProvidersETag = makeETag(sourceProvidersBody)
)

func renderSourceProviders() []byte {
	b, err := xml.Marshal(margeSourceProviders{Providers: staticProviders})
	if err != nil {
		return []byte(xml.Header + "<sourceProviders/>")
	}
	return append([]byte(xml.Header), b...)
}

func (s *Server) handleSourceProviders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", contentTypePresets)
	w.Header().Set("ETag", sourceProvidersETag)
	if r.Header.Get("If-None-Match") == sourceProvidersETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(sourceProvidersBody)
}

func (s *Server) handlePowerOn(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	w.WriteHeader(http.StatusOK)
}

// handleBlacklist mirrors upstream Bose, which answers GET /v1/blacklist/{id}
// with 405 Method Not Allowed.
func (s *Server) handleBlacklist(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusMethodNotAllowed)
}
