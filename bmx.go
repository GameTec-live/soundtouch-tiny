package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	tuneInDescribe = "https://opml.radiotime.com/describe.ashx?id=%s"
	tuneInStream   = "http://opml.radiotime.com/Tune.ashx?id=%s&formats=mp3,aac,ogg,hls"
	tuneInSearch   = "https://api.radiotime.com/profiles?fulltextsearch=true&version=1.3&query=%s"
)

type bmxPlayback struct {
	Links      map[string]bmxLink `json:"_links,omitempty"`
	Audio      bmxAudio           `json:"audio"`
	ImageURL   string             `json:"imageUrl"`
	IsFavorite *bool              `json:"isFavorite,omitempty"`
	Name       string             `json:"name"`
	StreamType string             `json:"streamType"`
}

type bmxLink struct {
	Href              string `json:"href"`
	UseInternalClient string `json:"useInternalClient,omitempty"`
}

type bmxAudio struct {
	HasPlaylist bool        `json:"hasPlaylist"`
	IsRealtime  bool        `json:"isRealtime"`
	MaxTimeout  int         `json:"maxTimeout,omitempty"`
	StreamURL   string      `json:"streamUrl"`
	Streams     []bmxStream `json:"streams"`
}

type bmxStream struct {
	Links             map[string]bmxLink `json:"_links,omitempty"`
	BufferingTimeout  int                `json:"bufferingTimeout,omitempty"`
	ConnectingTimeout int                `json:"connectingTimeout,omitempty"`
	HasPlaylist       bool               `json:"hasPlaylist"`
	IsRealtime        bool               `json:"isRealtime"`
	StreamURL         string             `json:"streamUrl"`
}

func (s *Server) bmxRegistry(base string) map[string]interface{} {
	return map[string]interface{}{
		"_links": map[string]interface{}{
			"bmx_services_availability": map[string]string{"href": "../servicesAvailability"},
		},
		"askAgainAfter": 1230482,
		"bmx_services": []map[string]interface{}{
			{
				"_links": map[string]interface{}{
					"bmx_token": map[string]string{"href": "/v1/token"},
					"self":      map[string]string{"href": "/"},
				},
				"askAdapter": false,
				"assets": map[string]interface{}{
					"color":       "#000000",
					"description": "TuneIn radio playback for SoundTouch Tiny.",
					"icons":       map[string]string{},
					"name":        "TuneIn",
				},
				"authenticationModel": map[string]interface{}{
					"anonymousAccount": map[string]interface{}{"autoCreate": true, "enabled": true},
				},
				"baseUrl":     base + "/bmx/tunein",
				"id":          map[string]interface{}{"name": sourceTuneIn, "value": 25},
				"streamTypes": []string{"liveRadio"},
			},
			{
				"_links": map[string]interface{}{
					"bmx_token": map[string]string{"href": "/token"},
					"self":      map[string]string{"href": "/"},
				},
				"askAdapter": false,
				"assets": map[string]interface{}{
					"color":       "#000000",
					"description": "Custom internet radio streams.",
					"icons":       map[string]string{},
					"name":        "Custom Stations",
				},
				"authenticationModel": map[string]interface{}{
					"anonymousAccount": map[string]interface{}{"autoCreate": true, "enabled": true},
				},
				"baseUrl":     base + orionPath,
				"id":          map[string]interface{}{"name": sourceLocalInternetRadio, "value": 11},
				"streamTypes": []string{"liveRadio"},
			},
		},
	}
}

func (s *Server) tuneInPlayback(baseURL, stationID string) (*bmxPlayback, error) {
	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Get(fmt.Sprintf(tuneInDescribe, stationID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	describeBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var desc struct {
		Body struct {
			Outline struct {
				Station struct {
					Name string `xml:"name,attr"`
					Logo string `xml:"logo,attr"`
				} `xml:"station"`
			} `xml:"outline"`
		} `xml:"body"`
	}
	if err := xml.Unmarshal(describeBody, &desc); err != nil {
		return nil, err
	}

	streamResp, err := client.Get(fmt.Sprintf(tuneInStream, stationID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = streamResp.Body.Close() }()

	streamBody, err := io.ReadAll(streamResp.Body)
	if err != nil {
		return nil, err
	}

	streams := splitStreamList(string(streamBody))
	streams = preferTuneInStreamURLs(client, streams)
	if len(streams) == 0 {
		return nil, fmt.Errorf("no streams found for %s", stationID)
	}

	return s.playbackResponse(baseURL, desc.Body.Outline.Station.Name, desc.Body.Outline.Station.Logo, streams, stationID), nil
}

func (s *Server) playbackResponse(baseURL, name, image string, streams []string, guideID string) *bmxPlayback {
	reportQuery := url.Values{}
	reportQuery.Set("stream_id", "e3342")
	reportQuery.Set("guide_id", guideID)
	reportQuery.Set("listen_id", "3432432423")
	reportQuery.Set("stream_type", "liveRadio")
	report := "/v1/report?" + reportQuery.Encode()
	out := make([]bmxStream, 0, len(streams))
	for _, streamURL := range streams {
		proxied := s.proxyStreamURL(baseURL, streamURL)
		out = append(out, bmxStream{
			Links:             map[string]bmxLink{"bmx_reporting": {Href: report}},
			BufferingTimeout:  20,
			ConnectingTimeout: 10,
			HasPlaylist:       true,
			IsRealtime:        true,
			StreamURL:         proxied,
		})
	}
	fav := false
	return &bmxPlayback{
		Links: map[string]bmxLink{
			"bmx_reporting":  {Href: report},
			"bmx_favorite":   {Href: "/v1/favorite/" + guideID},
			"bmx_nowplaying": {Href: "/v1/now-playing/station/" + guideID, UseInternalClient: "ALWAYS"},
		},
		Audio: bmxAudio{
			HasPlaylist: true,
			IsRealtime:  true,
			MaxTimeout:  60,
			StreamURL:   out[0].StreamURL,
			Streams:     out,
		},
		ImageURL:   image,
		IsFavorite: &fav,
		Name:       name,
		StreamType: "liveRadio",
	}
}

func (s *Server) customPlayback(baseURL, streamURL, imageURL, name string) *bmxPlayback {
	if name == "" {
		name = "Custom Radio"
	}
	return s.playbackResponse(baseURL, name, imageURL, []string{streamURL}, "custom")
}

func splitStreamList(body string) []string {
	var streams []string
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		streams = append(streams, line)
	}
	return streams
}

func preferTuneInStreamURLs(client *http.Client, streamURLList []string) []string {
	seen := make(map[string]bool)
	preferred := make([]string, 0, len(streamURLList))
	fallback := make([]string, 0, len(streamURLList))

	for _, rawURL := range streamURLList {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			continue
		}

		if directURL, ok := tuneInDirectStreamVariant(client, rawURL); ok && !seen[directURL] {
			preferred = append(preferred, directURL)
			seen[directURL] = true
		}

		if !seen[rawURL] {
			fallback = append(fallback, rawURL)
			seen[rawURL] = true
		}
	}

	return append(preferred, fallback...)
}

func tuneInDirectStreamVariant(client *http.Client, rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}

	basePath := ""
	switch {
	case strings.HasSuffix(u.Path, "/hls"):
		basePath = strings.TrimSuffix(u.Path, "/hls")
	case strings.HasSuffix(u.Path, "/hls.m3u8"):
		basePath = strings.TrimSuffix(u.Path, "/hls.m3u8")
	default:
		return "", false
	}

	for _, suffix := range []string{"aac", "aacp", "stream", "mp3"} {
		candidate := *u
		candidate.Path = basePath + "/" + suffix
		if tuneInDirectAudioProbe(client, candidate.String()) {
			return candidate.String(), true
		}
	}

	return "", false
}

func tuneInDirectAudioProbe(client *http.Client, rawURL string) bool {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	return isDirectAudioContentType(resp.Header.Get("Content-Type"))
}

func isDirectAudioContentType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	return strings.HasPrefix(contentType, "audio/") || contentType == "application/octet-stream"
}

func (s *Server) proxyStreamURL(baseURL, rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return rawURL
	}
	if !s.cfg.ProxyStreams && !(s.cfg.ProxyHTTPSStreams && strings.EqualFold(parsed.Scheme, "https")) {
		return rawURL
	}
	return strings.TrimRight(baseURL, "/") + "/bmx/stream?url=" + url.QueryEscape(rawURL)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
