package preset

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fullTestServer() *Server {
	srv := NewServer("http://127.0.0.1:8000", testConfig())
	// infoURL "" disables the lazy :8090/info fetch; lastFullPath "" disables the
	// on-device body dump — so tests use these deterministic identity values.
	srv.SetIdentity(Identity{
		DeviceID:     "B0D5CC1918A7",
		Name:         "TestSpeaker",
		Serial:       "069234P62650386AE",
		Firmware:     "27.0.6.46330.5043500",
		ProductCode:  "SoundTouch 10",
		ProductLabel: "SoundTouch 10",
		IPAddress:    "192.168.4.30",
	}, "", "")
	return srv
}

func TestAccountFullStructure(t *testing.T) {
	srv := fullTestServer()
	rr := bmxGet(t, srv, http.MethodGet, "/streaming/account/5740317/full")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "streaming-v1.2+xml") {
		t.Errorf("content-type = %q, want vnd.bose.streaming-v1.2+xml", ct)
	}

	var acc fullAccount
	if err := xml.Unmarshal(rr.Body.Bytes(), &acc); err != nil {
		t.Fatalf("/full is not valid XML: %v\n%s", err, rr.Body.String())
	}
	if acc.ID != "5740317" {
		t.Errorf("account id = %q, want echoed 5740317", acc.ID)
	}
	if acc.AccountStatus != "OK" || acc.Mode != "global" || acc.PreferredLanguage != "en" {
		t.Errorf("envelope = status:%q mode:%q lang:%q", acc.AccountStatus, acc.Mode, acc.PreferredLanguage)
	}
	if len(acc.Devices) != 1 {
		t.Fatalf("got %d devices, want 1", len(acc.Devices))
	}
	dev := acc.Devices[0]
	if dev.DeviceID != "B0D5CC1918A7" {
		t.Errorf("deviceid = %q", dev.DeviceID)
	}
	// One preset per configured station, each a well-formed LOCAL_INTERNET_RADIO.
	if len(dev.Presets) != len(testConfig().Stations) {
		t.Fatalf("got %d presets, want %d", len(dev.Presets), len(testConfig().Stations))
	}
	for i, p := range dev.Presets {
		if p.Source.SourceProviderID != "11" || p.Source.Name != "LOCAL_INTERNET_RADIO" {
			t.Errorf("preset %d source = providerid:%q name:%q", i, p.Source.SourceProviderID, p.Source.Name)
		}
		if !strings.Contains(p.Location, "/orion/station?data=") {
			t.Errorf("preset %d location = %q, want an Orion station URL", i, p.Location)
		}
	}
	// Exactly one account-level source, LOCAL_INTERNET_RADIO / providerid 11.
	if len(acc.Sources) != 1 || acc.Sources[0].SourceProviderID != "11" {
		t.Fatalf("account sources = %+v, want one providerid=11", acc.Sources)
	}
}

// TestAccountFullRequiredElements pins the wire format: every element the speaker
// decodes as a required protobuf field must be PRESENT (even when empty). A
// missing required element is exactly what aborts the sync and wipes presets, so
// this guards against a refactor silently dropping one.
func TestAccountFullRequiredElements(t *testing.T) {
	srv := fullTestServer()
	body := bmxGet(t, srv, http.MethodGet, "/streaming/account/5740317/full").Body.String()

	required := []string{
		`<accountStatus>OK</accountStatus>`,
		`<mode>global</mode>`,
		`<preferredLanguage>en</preferredLanguage>`,
		`deviceid="B0D5CC1918A7"`,
		`<attachedProduct product_code="SoundTouch 10">`,
		`<components></components>`,
		`<productlabel>`,
		`<serialnumber>`,
		`<firmwareVersion>27.0.6.46330.5043500</firmwareVersion>`,
		`<ipaddress>`,
		`<serialNumber>`,
		`<sourceproviderid>11</sourceproviderid>`,
		`<name>LOCAL_INTERNET_RADIO</name>`,
		`<credential type="token">`,
		`<sourcename>`,     // present even though empty
		`<sourceSettings>`, // present even though empty
		`<username>`,       // present even though empty
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Errorf("missing required element %q in /full body:\n%s", want, body)
		}
	}
}

func TestAccountFullConditional(t *testing.T) {
	srv := fullTestServer()
	path := "/streaming/account/5740317/full"

	rr1 := bmxGet(t, srv, http.MethodGet, path)
	etag := rr1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on /full")
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2, req)
	if rr2.Code != http.StatusNotModified {
		t.Fatalf("conditional /full status = %d, want 304", rr2.Code)
	}
}
