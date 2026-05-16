package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDedupeWiFiNetworksKeepsStrongest(t *testing.T) {
	got := dedupeWiFiNetworks([]wirelessSurveyItem{
		{SSID: "A", SignalStrength: -80, Secure: true, SecurityTypes: []string{defaultWiFiSecurity}},
		{SSID: "B", SignalStrength: -50, Secure: false},
		{SSID: "A", SignalStrength: -40, Secure: true, SecurityTypes: []string{defaultWiFiSecurity}},
		{SSID: "", SignalStrength: -10},
	})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(got), got)
	}
	if got[0].SSID != "A" || got[0].SignalStrength != -40 {
		t.Fatalf("first = %#v, want strongest A", got[0])
	}
	if got[1].SSID != "B" {
		t.Fatalf("second = %#v, want B", got[1])
	}
}

func TestWiFiNonInteractivePostsHiddenNetwork(t *testing.T) {
	var posted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/addWirelessProfile":
			data, _ := io.ReadAll(r.Body)
			posted = string(data)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8" ?><AddWirelessProfileResponse />`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldClient := wifiHTTPClient
	wifiHTTPClient = server.Client()
	defer func() { wifiHTTPClient = oldClient }()

	var out bytes.Buffer
	err := runWiFi(wifiOptions{
		Device:   server.URL,
		SSID:     `hidden "net"`,
		Password: `pass<&>'"`,
		Security: defaultWiFiSecurity,
	}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("runWiFi: %v", err)
	}
	for _, want := range []string{
		`ssid="hidden &#34;net&#34;"`,
		`password="pass&lt;&amp;&gt;&#39;&#34;"`,
		`securityType="wpa_or_wpa2"`,
	} {
		if !strings.Contains(posted, want) {
			t.Fatalf("posted body missing %q in %s", want, posted)
		}
	}
}

func TestWiFiInteractiveScanSelection(t *testing.T) {
	var posted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getActiveWirelessProfile":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8" ?><GetActiveWirelessProfileResponse><ssid>OldNet</ssid></GetActiveWirelessProfileResponse>`))
		case "/performWirelessSiteSurvey":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8" ?><PerformWirelessSiteSurveyResponse error="none"><items><item ssid="Weak" signalStrength="-80" secure="true"><securityTypes><type>wpa_or_wpa2</type></securityTypes></item><item ssid="Strong" signalStrength="-40" secure="true"><securityTypes><type>wpa_or_wpa2</type></securityTypes></item></items></PerformWirelessSiteSurveyResponse>`))
		case "/addWirelessProfile":
			data, _ := io.ReadAll(r.Body)
			posted = string(data)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8" ?><AddWirelessProfileResponse />`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldClient := wifiHTTPClient
	wifiHTTPClient = &http.Client{Timeout: time.Second, Transport: server.Client().Transport}
	defer func() { wifiHTTPClient = oldClient }()

	var out bytes.Buffer
	err := runWiFi(wifiOptions{Device: server.URL}, strings.NewReader("1\nsecret\n"), &out)
	if err != nil {
		t.Fatalf("runWiFi: %v", err)
	}
	if !strings.Contains(posted, `ssid="Strong"`) || !strings.Contains(posted, `password="secret"`) {
		t.Fatalf("posted body = %s", posted)
	}
	if !strings.Contains(out.String(), "Current WiFi: OldNet") {
		t.Fatalf("output missing current WiFi: %s", out.String())
	}
}
