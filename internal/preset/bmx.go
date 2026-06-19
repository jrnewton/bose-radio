package preset

import (
	"io"
	"net/http"
	"strings"
)

// BMX registry surface.
//
// Before a SoundTouch speaker will play a preset whose ContentItem source is
// LOCAL_INTERNET_RADIO, it validates that source against the BMX service
// registry it fetched from bmxRegistryUrl. If the registry doesn't advertise the
// Orion adapter, the speaker reports source="INVALID_SOURCE" and never follows
// the preset's location. So we serve a minimal registry that advertises only the
// Orion (LOCAL_INTERNET_RADIO) adapter, pointed back at this service.
//
// The flow when a LOCAL_INTERNET_RADIO button is pressed:
//  1. (at boot) GET  /bmx/registry/v1/services            -> learns the adapter
//  2. POST {orionBase}/token                    -> anonymous token
//  3. GET  {preset location} = {orionBase}/station?data=... -> stream
//
// Paths and JSON shapes mirror the reference repo's bmx_services.json and
// handlers_bmx_orion.go. {BMX_SERVER}/{MEDIA_SERVER} are substituted with this
// service's base URL at request time.
const (
	bmxRegistryServicesPath     = "/bmx/registry/v1/services"
	bmxRegistryAvailabilityPath = "/bmx/registry/v1/servicesAvailability"

	// orionBasePath is the single source of truth for the Orion adapter path.
	// The station/token/self routes (here and stationPath in server.go) and the
	// registry's advertised baseUrl all derive from it, so they can't drift apart.
	orionBasePath    = "/core02/svc-bmx-adapter-orion/prod/orion"
	orionServicePath = orionBasePath            // adapter "self" descriptor
	orionTokenPath   = orionBasePath + "/token" // anonymous token
)

// orionServiceJSON is the LOCAL_INTERNET_RADIO service descriptor (one entry of
// the BMX registry's bmx_services array), taken from the reference repo.
const orionServiceJSON = `{
    "_links": { "bmx_token": { "href": "/token" }, "self": { "href": "/" } },
    "askAdapter": false,
    "assets": {
      "color": "#000000",
      "description": "Custom radio stations with BMX.",
      "icons": {
        "largeSvg": "{MEDIA_SERVER}/bmx-icons/orion/monochrome.svg",
        "monochromePng": "{MEDIA_SERVER}/bmx-icons/orion/monochrome_v2.png",
        "monochromeSvg": "{MEDIA_SERVER}/bmx-icons/orion/monochrome.svg",
        "smallSvg": "{MEDIA_SERVER}/bmx-icons/orion/monochrome.svg"
      },
      "name": "Custom Stations"
    },
    "authenticationModel": { "anonymousAccount": { "autoCreate": true, "enabled": true } },
    "baseUrl": "{BMX_SERVER}` + orionBasePath + `",
    "id": { "name": "LOCAL_INTERNET_RADIO", "value": 11 },
    "streamTypes": [ "liveRadio" ]
  }`

// bmxRegistryJSON is the full registry response advertising only Orion.
const bmxRegistryJSON = `{
  "_links": { "bmx_services_availability": { "href": "../servicesAvailability" } },
  "askAgainAfter": 1230482,
  "bmx_services": [ ` + orionServiceJSON + ` ]
}`

// bmxAvailabilityJSON lists LOCAL_INTERNET_RADIO as an available service. No
// template placeholders, so it is served verbatim.
const bmxAvailabilityJSON = `{ "services": [ { "canAdd": true, "canRemove": false, "service": "LOCAL_INTERNET_RADIO" } ] }`

// orionToken is a fixed opaque anonymous token. The speaker stores it and sends
// it back as a Bearer header; the station handler does not validate it (matching
// the reference, whose auth gate is disabled), so any non-empty value works.
const orionToken = "c3QtcHJlc2V0LXNlcnZlci1hbm9u"

const orionTokenJSON = `{"_embedded":{"bmx_account":{"displayName":"","username":""}},"access_token":"` + orionToken + `","refresh_token":"` + orionToken + `"}`

// applyBMXTemplate substitutes the registry's host placeholders with this
// service's base URL.
func (s *Server) applyBMXTemplate(content string) string {
	s.mu.RLock()
	base := s.baseURL
	s.mu.RUnlock()
	content = strings.ReplaceAll(content, "{BMX_SERVER}", base)
	content = strings.ReplaceAll(content, "{MEDIA_SERVER}", base+"/media")
	return content
}

func (s *Server) handleBMXRegistry(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, s.applyBMXTemplate(bmxRegistryJSON))
}

func (s *Server) handleBMXAvailability(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, bmxAvailabilityJSON)
}

func (s *Server) handleOrionService(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, s.applyBMXTemplate(orionServiceJSON))
}

func (s *Server) handleOrionToken(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, orionTokenJSON)
}

// handleMedia serves a tiny placeholder for the BMX icon assets the registry
// advertises (under {MEDIA_SERVER}/bmx-icons/...). They're cosmetic — the
// source's glyph in the app UI — so a 1x1 SVG is enough to keep them out of the
// boot log as 404s.
const placeholderSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>`

func (s *Server) handleMedia(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = io.WriteString(w, placeholderSVG)
}
