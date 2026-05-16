package main

import "testing"

func TestDefaultConfigIsOnDeviceReady(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Port != 8000 {
		t.Fatalf("port = %d, want 8000", cfg.Port)
	}
	if cfg.ProxyStreams {
		t.Fatal("proxy_streams should default false")
	}
	if !cfg.ProxyHTTPSStreams {
		t.Fatal("proxy_https_streams should default true")
	}
	if !cfg.InsecureHTTPS {
		t.Fatal("insecure_https should default true")
	}
	if len(cfg.Presets) != 0 {
		t.Fatalf("presets = %d, want 0", len(cfg.Presets))
	}
}

func TestConfigNormalizeAllowsLocalInternetRadioStreamURL(t *testing.T) {
	cfg := &Config{
		AccountID: "7654321",
		Port:      8001,
		Presets: []PresetConfig{{
			Slot:      1,
			Name:      "Local Stream",
			StreamURL: "http://radio.example/stream.mp3",
		}},
	}

	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}

	p := cfg.Presets[0]
	if p.Source != sourceLocalInternetRadio {
		t.Fatalf("source = %q, want %q", p.Source, sourceLocalInternetRadio)
	}
	if p.Location != "" {
		t.Fatalf("location = %q, want dynamic empty location", p.Location)
	}
}

func TestConfigNormalizeRejectsDuplicateSlots(t *testing.T) {
	cfg := &Config{
		Presets: []PresetConfig{
			{Slot: 1, Name: "A", Source: sourceTuneIn, Location: "s1"},
			{Slot: 1, Name: "B", Source: sourceTuneIn, Location: "s2"},
		},
	}
	if err := cfg.normalize(); err == nil {
		t.Fatal("expected duplicate slot error")
	}
}

func TestDecodeCustomStream(t *testing.T) {
	encoded := encodeCustomStream("http://radio.example/live", "http://radio.example/art.png", "Radio")
	streamURL, imageURL, name, err := decodeCustomStream(encoded)
	if err != nil {
		t.Fatalf("decodeCustomStream: %v", err)
	}
	if streamURL != "http://radio.example/live" || imageURL != "http://radio.example/art.png" || name != "Radio" {
		t.Fatalf("decoded = %q %q %q", streamURL, imageURL, name)
	}
}
