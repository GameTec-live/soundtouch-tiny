package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	defaultAccountID = "1234567"
	defaultPort      = 8000

	sourceAux                = "AUX"
	sourceInternetRadio      = "INTERNET_RADIO"
	sourceLocalInternetRadio = "LOCAL_INTERNET_RADIO"
	sourceTuneIn             = "TUNEIN"

	orionPath = "/core02/svc-bmx-adapter-orion/prod/orion"
)

type Config struct {
	AccountID           string         `json:"account_id"`
	ServerURL           string         `json:"server_url"`
	DeviceID            string         `json:"device_id"`
	DeviceName          string         `json:"device_name"`
	IPAddress           string         `json:"ip_address"`
	ProductCode         string         `json:"product_code"`
	DeviceSerialNumber  string         `json:"device_serial_number"`
	ProductSerialNumber string         `json:"product_serial_number"`
	FirmwareVersion     string         `json:"firmware_version"`
	Port                int            `json:"port"`
	ProxyStreams        bool           `json:"proxy_streams"`
	ProxyHTTPSStreams   bool           `json:"proxy_https_streams"`
	InsecureHTTPS       bool           `json:"insecure_https"`
	Presets             []PresetConfig `json:"presets"`
}

type PresetConfig struct {
	Slot          int    `json:"slot"`
	Name          string `json:"name"`
	Source        string `json:"source"`
	Type          string `json:"type"`
	Location      string `json:"location"`
	StreamURL     string `json:"stream_url"`
	Art           string `json:"art"`
	SourceAccount string `json:"source_account"`
}

func loadConfig(path string) (*Config, error) {
	cfg := defaultConfig()
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, cfg.normalize()
}

func defaultConfig() *Config {
	cfg := &Config{
		AccountID:         defaultAccountID,
		DeviceName:        "SoundTouch",
		ProductCode:       "SoundTouch",
		FirmwareVersion:   "27.0.6",
		Port:              defaultPort,
		ProxyHTTPSStreams: true,
		InsecureHTTPS:     true,
	}
	_ = cfg.normalize()
	return cfg
}

func (c *Config) normalize() error {
	if c.AccountID == "" {
		c.AccountID = defaultAccountID
	}
	if c.DeviceName == "" {
		c.DeviceName = "SoundTouch"
	}
	if c.ProductCode == "" {
		c.ProductCode = "SoundTouch"
	}
	if c.FirmwareVersion == "" {
		c.FirmwareVersion = "27.0.6"
	}
	if c.Port == 0 {
		c.Port = defaultPort
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port %d out of range", c.Port)
	}

	seen := make(map[int]bool)
	for i := range c.Presets {
		p := &c.Presets[i]
		if p.Slot < 1 || p.Slot > 6 {
			return fmt.Errorf("preset slot %d out of range 1..6", p.Slot)
		}
		if seen[p.Slot] {
			return fmt.Errorf("duplicate preset slot %d", p.Slot)
		}
		seen[p.Slot] = true

		p.Source = strings.ToUpper(strings.TrimSpace(p.Source))
		if p.Source == "" {
			if p.StreamURL != "" {
				p.Source = sourceLocalInternetRadio
			} else {
				p.Source = sourceTuneIn
			}
		}
		if p.Type == "" {
			p.Type = "stationurl"
		}
		if p.Name == "" {
			p.Name = fmt.Sprintf("Preset %d", p.Slot)
		}
		if p.Source == sourceTuneIn {
			p.Location = normalizeTuneInLocation(p.Location)
		}
		if p.Source == sourceLocalInternetRadio && p.Location == "" {
			if p.StreamURL == "" {
				return fmt.Errorf("preset %d: LOCAL_INTERNET_RADIO needs location or stream_url", p.Slot)
			}
		}
		if p.Location == "" {
			if p.Source == sourceLocalInternetRadio && p.StreamURL != "" {
				continue
			}
			return fmt.Errorf("preset %d: location is required", p.Slot)
		}
	}
	return nil
}

func normalizeTuneInLocation(location string) string {
	location = strings.TrimSpace(location)
	if location == "" {
		return location
	}
	if strings.HasPrefix(location, "/v1/playback/station/") {
		return location
	}
	if strings.HasPrefix(location, "s") || strings.HasPrefix(location, "p") {
		return "/v1/playback/station/" + location
	}
	return location
}

func (c *Config) listenAddr() string {
	return ":" + strconv.Itoa(c.Port)
}

func (c *Config) externalBaseURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(c.Port)
}

func encodeCustomStream(streamURL, imageURL, name string) string {
	payload := map[string]string{
		"streamUrl": streamURL,
		"imageUrl":  imageURL,
		"name":      name,
	}
	data, _ := json.Marshal(payload)
	return base64.URLEncoding.EncodeToString(data)
}

func decodeCustomStream(data string) (streamURL, imageURL, name string, err error) {
	raw, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		raw, err = base64.StdEncoding.DecodeString(data)
	}
	if err != nil {
		return "", "", "", err
	}

	var payload struct {
		StreamURL string `json:"streamUrl"`
		ImageURL  string `json:"imageUrl"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", "", "", err
	}
	if payload.StreamURL == "" {
		return "", "", "", errors.New("custom stream payload missing streamUrl")
	}
	return payload.StreamURL, payload.ImageURL, payload.Name, nil
}

func decodePathValue(value string) string {
	raw, err := base64.URLEncoding.DecodeString(value)
	if err != nil {
		raw, err = base64.StdEncoding.DecodeString(value)
	}
	if err == nil {
		return string(raw)
	}
	if decoded, unescapeErr := url.PathUnescape(value); unescapeErr == nil {
		return decoded
	}
	return value
}
