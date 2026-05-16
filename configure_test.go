package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTuneInPresetXML(t *testing.T) {
	body, err := tuneInPresetXML(3, tuneInStation{
		Name:     "Radio Test",
		GuideID:  "s123",
		ImageURL: "http://example/logo.png",
	})
	if err != nil {
		t.Fatalf("tuneInPresetXML: %v", err)
	}
	for _, want := range []string{
		`<preset id="3"`,
		`<ContentItem source="TUNEIN" type="stationurl" location="/v1/playback/station/s123" sourceAccount="" isPresetable="true">`,
		`<itemName>Radio Test</itemName>`,
		`<containerArt>http://example/logo.png</containerArt>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in\n%s", want, body)
		}
	}
}

func TestFetchDevicePresets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/presets" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`<presets><preset id="2"><ContentItem source="TUNEIN" type="stationurl" location="/v1/playback/station/s1" sourceAccount="" isPresetable="true"><itemName>Station</itemName></ContentItem></preset></presets>`))
	}))
	defer server.Close()

	presets, err := fetchDevicePresets(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchDevicePresets: %v", err)
	}
	if len(presets.Presets) != 1 {
		t.Fatalf("presets = %d", len(presets.Presets))
	}
	if presets.Presets[0].ID != 2 || presets.Presets[0].ContentItem.ItemName != "Station" {
		t.Fatalf("preset = %+v", presets.Presets[0])
	}
}

func TestStoreTuneInPresetPostsDeviceAPI(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/storePreset" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := storeTuneInPreset(server.Client(), server.URL, 4, tuneInStation{Name: "Station", GuideID: "s42"})
	if err != nil {
		t.Fatalf("storeTuneInPreset: %v", err)
	}
	if !strings.Contains(body, `id="4"`) || !strings.Contains(body, `/v1/playback/station/s42`) {
		t.Fatalf("body = %s", body)
	}
}

func TestSearchTuneInStationsParsesStations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
  "Items": [
    {
      "Type": "Container",
      "ContainerType": "Stations",
      "Children": [
        {"Type": "Station", "GuideId": "s1", "Title": "One", "Subtitle": "FM", "Image": "http://img/1"},
        {"Type": "Station", "GuideId": "s1", "Title": "One duplicate"},
        {"Type": "Topic", "GuideId": "p1", "Title": "Podcast"}
      ]
    }
  ]
}`))
	}))
	defer server.Close()

	oldSearch := tuneInSearch
	tuneInSearch = server.URL + "?q=%s"
	defer func() { tuneInSearch = oldSearch }()

	stations, err := searchTuneInStations(server.Client(), "one")
	if err != nil {
		t.Fatalf("searchTuneInStations: %v", err)
	}
	if len(stations) != 1 {
		t.Fatalf("stations = %+v", stations)
	}
	if stations[0].GuideID != "s1" || stations[0].Name != "One" || stations[0].Subtitle != "FM" {
		t.Fatalf("station = %+v", stations[0])
	}
}
