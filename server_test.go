package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testConfig(t *testing.T) *Config {
	t.Helper()
	cfg := &Config{
		AccountID:    "1234567",
		DeviceID:     "AABBCCDDEEFF",
		DeviceName:   "Kitchen",
		Port:         8000,
		ProxyStreams: true,
		Presets: []PresetConfig{{
			Slot:     1,
			Name:     "KEXP",
			Source:   sourceTuneIn,
			Type:     "stationurl",
			Location: "s12345",
			Art:      "http://example/art.png",
		}},
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return cfg
}

func TestAccountFullResponseIncludesDeviceSourcesAndPresets(t *testing.T) {
	s := NewServer(testConfig(t))
	req := httptest.NewRequest(http.MethodGet, "/streaming/account/1234567/full", nil)
	rec := httptest.NewRecorder()

	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<account id="1234567">`,
		`<device deviceid="AABBCCDDEEFF">`,
		`<sourceproviderid>25</sourceproviderid>`,
		`<preset buttonNumber="1">`,
		`<location>/v1/playback/station/s12345</location>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %q:\n%s", want, body)
		}
	}
}

func TestPowerOnLearnsDeviceID(t *testing.T) {
	s := NewServer(defaultConfig())
	body := `<?xml version="1.0"?><device-data><device id="ABCDEF123456"><serialnumber>SERIAL</serialnumber><firmware-version>27.0.6</firmware-version><product product_code="ST20"><serialnumber>PROD</serialnumber></product></device><diagnostic-data><device-landscape><ip-address>192.0.2.10</ip-address></device-landscape></diagnostic-data></device-data>`
	req := httptest.NewRequest(http.MethodPost, "/streaming/support/power_on", strings.NewReader(body))
	rec := httptest.NewRecorder()

	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := s.deviceID(); got != "ABCDEF123456" {
		t.Fatalf("deviceID = %q", got)
	}
}

func TestPowerOnGetIsOKForManualChecks(t *testing.T) {
	s := NewServer(defaultConfig())
	req := httptest.NewRequest(http.MethodGet, "/streaming/support/power_on", nil)
	rec := httptest.NewRecorder()

	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDebugRecentRecordsRequests(t *testing.T) {
	s := NewServer(defaultConfig())
	s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	req := httptest.NewRequest(http.MethodGet, "/debug/recent", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "200 GET /health") {
		t.Fatalf("recent = %q", rec.Body.String())
	}
}

func TestBMXRegistryUsesRequestHost(t *testing.T) {
	cfg := testConfig(t)
	cfg.Port = 8123
	s := NewServer(cfg)
	req := httptest.NewRequest(http.MethodGet, "http://example.test:8123/bmx/registry/v1/services", nil)
	rec := httptest.NewRecorder()

	s.ServeHTTP(rec, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	services := resp["bmx_services"].([]interface{})
	first := services[0].(map[string]interface{})
	if got := first["baseUrl"].(string); got != "http://example.test:8123/bmx/tunein" {
		t.Fatalf("baseUrl = %q", got)
	}
}

func TestOrionStationPlayback(t *testing.T) {
	s := NewServer(testConfig(t))
	encoded := encodeCustomStream("http://radio.example/live", "http://radio.example/art.png", "Radio")
	req := httptest.NewRequest(http.MethodGet, orionPath+"/station?data="+encoded, nil)
	rec := httptest.NewRecorder()

	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp bmxPlayback
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp.Name != "Radio" {
		t.Fatalf("name = %q", resp.Name)
	}
	if !strings.Contains(resp.Audio.StreamURL, "/bmx/stream?url=") {
		t.Fatalf("streamURL = %q", resp.Audio.StreamURL)
	}
}

func TestOrionStationPlaybackCanUseDirectStream(t *testing.T) {
	cfg := testConfig(t)
	cfg.ProxyStreams = false
	s := NewServer(cfg)
	encoded := encodeCustomStream("http://radio.example/live", "http://radio.example/art.png", "Radio")
	req := httptest.NewRequest(http.MethodGet, orionPath+"/station?data="+encoded, nil)
	rec := httptest.NewRecorder()

	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp bmxPlayback
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp.Audio.StreamURL != "http://radio.example/live" {
		t.Fatalf("streamURL = %q", resp.Audio.StreamURL)
	}
}

func TestHTTPSStreamsAreProxiedWhenDirectStreamsAreDisabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.ProxyStreams = false
	cfg.ProxyHTTPSStreams = true
	s := NewServer(cfg)
	encoded := encodeCustomStream("https://radio.example/live", "", "Radio")
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8000"+orionPath+"/station?data="+encoded, nil)
	rec := httptest.NewRecorder()

	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp bmxPlayback
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !strings.HasPrefix(resp.Audio.StreamURL, "http://127.0.0.1:8000/bmx/stream?url=") {
		t.Fatalf("streamURL = %q", resp.Audio.StreamURL)
	}
	escaped := strings.TrimPrefix(resp.Audio.StreamURL, "http://127.0.0.1:8000/bmx/stream?url=")
	raw, err := url.QueryUnescape(escaped)
	if err != nil {
		t.Fatal(err)
	}
	if raw != "https://radio.example/live" {
		t.Fatalf("proxied URL = %q", raw)
	}
}

func TestHTTPStreamsStayDirectWhenOnlyHTTPSProxyingIsEnabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.ProxyStreams = false
	cfg.ProxyHTTPSStreams = true
	s := NewServer(cfg)
	encoded := encodeCustomStream("http://radio.example/live", "", "Radio")
	req := httptest.NewRequest(http.MethodGet, orionPath+"/station?data="+encoded, nil)
	rec := httptest.NewRecorder()

	s.ServeHTTP(rec, req)

	var resp bmxPlayback
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp.Audio.StreamURL != "http://radio.example/live" {
		t.Fatalf("streamURL = %q", resp.Audio.StreamURL)
	}
}

func TestStreamProxyCanSkipTLSVerification(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("audio"))
	}))
	defer upstream.Close()

	cfg := testConfig(t)
	cfg.InsecureHTTPS = true
	s := NewServer(cfg)
	req := httptest.NewRequest(http.MethodGet, "/bmx/stream?url="+url.QueryEscape(upstream.URL), nil)
	rec := httptest.NewRecorder()

	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "audio" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestStreamProxyDoesNotUseMetadataTimeoutClient(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(25 * time.Millisecond)
		_, _ = w.Write([]byte("audio"))
	}))
	defer upstream.Close()

	s := NewServer(testConfig(t))
	s.httpClient = &http.Client{Timeout: time.Nanosecond}
	s.streamClient = upstream.Client()
	req := httptest.NewRequest(http.MethodGet, "/bmx/stream?url="+url.QueryEscape(upstream.URL), nil)
	rec := httptest.NewRecorder()

	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "audio" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestTuneInPlaybackUsesInjectedHTTPClient(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stream":
			_, _ = io.WriteString(w, "http://radio.example/live\n")
		default:
			_, _ = io.WriteString(w, `<opml><body><outline><station name="Station" logo="http://logo"/></outline></body></opml>`)
		}
	}))
	defer upstream.Close()

	oldDescribe := tuneInDescribe
	oldStream := tuneInStream
	tuneInDescribe = upstream.URL + "/describe?id=%s"
	tuneInStream = upstream.URL + "/stream?id=%s"
	defer func() {
		tuneInDescribe = oldDescribe
		tuneInStream = oldStream
	}()

	s := NewServer(testConfig(t))
	req := httptest.NewRequest(http.MethodGet, "/bmx/tunein/v1/playback/station/s123", nil)
	rec := httptest.NewRecorder()

	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp bmxPlayback
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp.Name != "Station" || !strings.Contains(resp.Audio.StreamURL, "/bmx/stream?url=") {
		t.Fatalf("unexpected playback: %+v", resp)
	}
	if !strings.Contains(resp.Links["bmx_reporting"].Href, "listen_id=3432432423") {
		t.Fatalf("reporting href = %q", resp.Links["bmx_reporting"].Href)
	}
}

func TestPreferTuneInStreamURLsAddsDirectAudioVariant(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/station/aac" {
			w.Header().Set("Content-Type", "audio/aac")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	got := preferTuneInStreamURLs(upstream.Client(), []string{upstream.URL + "/station/hls"})

	if len(got) != 2 {
		t.Fatalf("streams = %v, want direct and fallback", got)
	}
	if got[0] != upstream.URL+"/station/aac" {
		t.Fatalf("first stream = %q", got[0])
	}
	if got[1] != upstream.URL+"/station/hls" {
		t.Fatalf("fallback stream = %q", got[1])
	}
}
