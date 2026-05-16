package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	cfg          *Config
	state        runtimeState
	mu           sync.RWMutex
	httpClient   *http.Client
	streamClient *http.Client
	recent       []string
}

type runtimeState struct {
	DeviceID            string
	DeviceName          string
	IPAddress           string
	ProductCode         string
	DeviceSerialNumber  string
	ProductSerialNumber string
	FirmwareVersion     string
}

type powerOnRequest struct {
	XMLName xml.Name `xml:"device-data"`
	Device  struct {
		ID              string `xml:"id,attr"`
		SerialNumber    string `xml:"serialnumber"`
		FirmwareVersion string `xml:"firmware-version"`
		Product         struct {
			ProductCode  string `xml:"product_code,attr"`
			SerialNumber string `xml:"serialnumber"`
		} `xml:"product"`
	} `xml:"device"`
	DiagnosticData struct {
		DeviceLandscape struct {
			IPAddress    string   `xml:"ip-address"`
			MacAddresses []string `xml:"macaddresses>macaddress"`
		} `xml:"device-landscape"`
	} `xml:"diagnostic-data"`
}

func NewServer(cfg *Config) *Server {
	httpTransport := newTransport(cfg.InsecureHTTPS)
	streamTransport := newTransport(cfg.InsecureHTTPS)
	return &Server{
		cfg: cfg,
		state: runtimeState{
			DeviceID:            cfg.DeviceID,
			DeviceName:          cfg.DeviceName,
			IPAddress:           cfg.IPAddress,
			ProductCode:         cfg.ProductCode,
			DeviceSerialNumber:  cfg.DeviceSerialNumber,
			ProductSerialNumber: cfg.ProductSerialNumber,
			FirmwareVersion:     cfg.FirmwareVersion,
		},
		httpClient:   &http.Client{Timeout: 10 * time.Second, Transport: httpTransport},
		streamClient: &http.Client{Transport: streamTransport},
	}
}

func newTransport(insecureHTTPS bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecureHTTPS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return transport
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("%s %s host=%s from=%s", r.Method, r.URL.RequestURI(), r.Host, r.RemoteAddr)
	rec := &statusRecorder{ResponseWriter: w}
	defer func() {
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		s.recordAccess(r, status)
	}()
	w = rec

	path := strings.TrimRight(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}

	switch {
	case path == "/" || path == "/health":
		_, _ = w.Write([]byte("ok\n"))
	case path == "/debug/recent":
		s.handleRecent(w)
	case path == "/streaming/support/power_on" && (r.Method == http.MethodPost || r.Method == http.MethodGet):
		s.handlePowerOn(w, r)
	case path == "/streaming/support/customersupport" && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusOK)
	case path == "/streaming/stats/usage" || path == "/streaming/stats/error" ||
		strings.HasPrefix(path, "/v1/stapp/") || strings.HasPrefix(path, "/v1/scmudc/"):
		w.WriteHeader(http.StatusOK)
	case path == "/streaming/sourceproviders" && r.Method == http.MethodGet:
		s.writeXML(w, s.sourceProvidersXML)
	case path == "/streaming/resources/api_versions.xml" && r.Method == http.MethodGet:
		writeXMLBytes(w, apiVersionsXML())
	case strings.HasPrefix(path, "/streaming/software/update/account/") || path == "/updates/soundtouch":
		writeXMLBytes(w, softwareUpdateXML())
	case strings.HasPrefix(path, "/streaming/device/") && strings.HasSuffix(path, "/streaming_token"):
		s.writeXML(w, streamingTokenXML)
	case path == "/bmx/registry/v1/services" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.bmxRegistry(s.baseURL(r)))
	case path == "/bmx/registry/v1/servicesAvailability" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]interface{}{"services": []interface{}{}})
	case path == "/bmx/tunein/v1/token" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusOK, map[string]string{"access_token": "tiny-tunein", "refresh_token": "tiny-tunein"})
	case strings.HasPrefix(path, "/bmx/tunein/v1/playback/station/") && r.Method == http.MethodGet:
		s.handleTuneInPlayback(w, r, path)
	case path == "/bmx/tunein/v1/report" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusOK, map[string]interface{}{"nextReportIn": 1800})
	case path == orionPath+"/token" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusOK, map[string]interface{}{"access_token": generateSerialSecret("orion"), "refresh_token": generateSerialSecret("orion")})
	case path == orionPath+"/station" && r.Method == http.MethodGet:
		s.handleOrionStation(w, r)
	case strings.HasPrefix(path, "/custom/v1/playback/") && r.Method == http.MethodGet:
		s.handleCustomPlayback(w, r)
	case path == "/bmx/stream" && r.Method == http.MethodGet:
		s.handleStreamProxy(w, r)
	case s.handleAccountRoute(w, r, path):
		return
	default:
		http.NotFound(w, r)
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(data)
}

func (s *Server) recordAccess(r *http.Request, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	line := time.Now().Format(time.RFC3339) + " " + strconv.Itoa(status) + " " + r.Method + " " + r.URL.RequestURI() + " host=" + r.Host + " from=" + r.RemoteAddr
	s.recent = append(s.recent, line)
	if len(s.recent) > 64 {
		copy(s.recent, s.recent[len(s.recent)-64:])
		s.recent = s.recent[:64]
	}
}

func (s *Server) handleRecent(w http.ResponseWriter) {
	s.mu.RLock()
	lines := append([]string(nil), s.recent...)
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if len(lines) == 0 {
		_, _ = w.Write([]byte("no requests recorded\n"))
		return
	}
	_, _ = w.Write([]byte(strings.Join(lines, "\n") + "\n"))
}

func (s *Server) handlePowerOn(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err == nil {
		var req powerOnRequest
		if xml.Unmarshal(body, &req) == nil {
			s.learnFromPowerOn(req, r.RemoteAddr)
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) learnFromPowerOn(req powerOnRequest, remoteAddr string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Device.ID != "" {
		s.state.DeviceID = req.Device.ID
	}
	if req.DiagnosticData.DeviceLandscape.IPAddress != "" {
		s.state.IPAddress = req.DiagnosticData.DeviceLandscape.IPAddress
	} else if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		s.state.IPAddress = host
	}
	if req.Device.Product.ProductCode != "" {
		s.state.ProductCode = req.Device.Product.ProductCode
	}
	if req.Device.SerialNumber != "" {
		s.state.DeviceSerialNumber = req.Device.SerialNumber
	}
	if req.Device.Product.SerialNumber != "" {
		s.state.ProductSerialNumber = req.Device.Product.SerialNumber
	}
	if req.Device.FirmwareVersion != "" {
		s.state.FirmwareVersion = req.Device.FirmwareVersion
	}
}

func (s *Server) handleAccountRoute(w http.ResponseWriter, r *http.Request, path string) bool {
	prefix := "/streaming/account/"
	if strings.HasPrefix(path, "/accounts/") {
		prefix = "/accounts/"
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}

	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return false
	}
	account := parts[0]
	tail := strings.Join(parts[1:], "/")

	switch {
	case r.Method == http.MethodPost && isDeviceRegistrationTail(tail):
		s.handleAddAccountDevice(w, r, account)
	case tail == "provider_settings":
		s.writeXML(w, func() ([]byte, error) { return providerSettingsXML(account) })
	case r.Method == http.MethodGet && strings.HasPrefix(tail, "device/") && strings.HasSuffix(tail, "/group"):
		writeXMLBytes(w, groupXML())
	case (r.Method == http.MethodPost || r.Method == http.MethodPut) && strings.HasPrefix(tail, "device/") && strings.Contains(tail, "/preset/"):
		s.handleUpdateAccountPreset(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(tail, "device/") && (strings.HasSuffix(tail, "/recent") || strings.HasSuffix(tail, "/recents")):
		writeXMLBytesStatus(w, http.StatusCreated, recentXML())
	case r.Method == http.MethodGet && strings.HasPrefix(tail, "device/") && (strings.HasSuffix(tail, "/recent") || strings.HasSuffix(tail, "/recents")):
		writeXMLBytes(w, recentsXML())
	case tail == "full":
		s.writeXML(w, func() ([]byte, error) { return s.accountFullXML(account, s.baseURL(r)) })
	case tail == "sources":
		s.writeXML(w, s.accountSourcesXML)
	case tail == "devices":
		s.writeXML(w, func() ([]byte, error) { return s.accountDevicesXML(account) })
	case tail == "presets" || tail == "presets/all":
		s.writeXML(w, func() ([]byte, error) { return s.presetsXML(s.baseURL(r)) })
	case strings.HasPrefix(tail, "device/") && strings.HasSuffix(tail, "/presets"):
		s.writeXML(w, func() ([]byte, error) { return s.presetsXML(s.baseURL(r)) })
	case strings.HasPrefix(tail, "devices/") && strings.HasSuffix(tail, "/presets"):
		s.writeXML(w, func() ([]byte, error) { return s.presetsXML(s.baseURL(r)) })
	default:
		return false
	}
	return true
}

func isDeviceRegistrationTail(tail string) bool {
	if tail == "device" {
		return true
	}
	if !strings.HasPrefix(tail, "device/") {
		return false
	}
	return !strings.Contains(strings.TrimPrefix(tail, "device/"), "/")
}

func (s *Server) handleAddAccountDevice(w http.ResponseWriter, r *http.Request, account string) {
	var req struct {
		DeviceID   string `xml:"deviceid,attr"`
		Name       string `xml:"name"`
		MACAddress string `xml:"macaddress"`
	}
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := xml.Unmarshal(body, &req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
	}

	s.mu.Lock()
	if req.DeviceID != "" {
		s.state.DeviceID = req.DeviceID
	}
	if req.Name != "" {
		s.state.DeviceName = req.Name
	}
	if req.MACAddress != "" && s.state.DeviceID == "" {
		s.state.DeviceID = req.MACAddress
	}
	s.mu.Unlock()

	data, err := s.accountDeviceXML()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.bose.streaming-v1.2+xml")
	w.Header().Set("Location", "/streaming/account/"+account+"/device/"+s.deviceID())
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(data)
}

func (s *Server) handleUpdateAccountPreset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ButtonNumber    string `xml:"buttonNumber,attr"`
		SourceID        string `xml:"sourceid"`
		Name            string `xml:"name"`
		Username        string `xml:"username"`
		Location        string `xml:"location"`
		ContentItemType string `xml:"contentItemType"`
		ContainerArt    string `xml:"containerArt"`
	}
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := xml.Unmarshal(body, &req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
	}
	source := strings.ToUpper(strings.TrimSpace(req.SourceID))
	if source == "" {
		source = sourceTuneIn
	}
	preset := xmlPreset{
		ButtonNumber:    req.ButtonNumber,
		ContainerArt:    req.ContainerArt,
		ContentItemType: firstNonEmpty(req.ContentItemType, "stationurl"),
		CreatedOn:       dateStr,
		Location:        req.Location,
		Name:            req.Name,
		Source:          ptrSource(sourceForPreset(PresetConfig{Source: source})),
		UpdatedOn:       dateStr,
		Username:        req.Username,
	}
	if preset.Username == "" {
		preset.Username = preset.Name
	}
	data, err := marshalXML(preset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeXMLBytes(w, data)
}

func (s *Server) handleTuneInPlayback(w http.ResponseWriter, r *http.Request, path string) {
	stationID := strings.TrimPrefix(path, "/bmx/tunein/v1/playback/station/")
	resp, err := s.tuneInPlayback(s.baseURL(r), stationID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleOrionStation(w http.ResponseWriter, r *http.Request) {
	streamURL, imageURL, name, err := decodeCustomStream(r.URL.Query().Get("data"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, s.customPlayback(s.baseURL(r), streamURL, imageURL, name))
}

func (s *Server) handleCustomPlayback(w http.ResponseWriter, r *http.Request) {
	encoded := strings.TrimPrefix(strings.TrimRight(r.URL.Path, "/"), "/custom/v1/playback/")
	streamURL := decodePathValue(encoded)
	imageURL := r.URL.Query().Get("imageUrl")
	name := r.URL.Query().Get("name")
	writeJSON(w, http.StatusOK, s.customPlayback(s.baseURL(r), streamURL, imageURL, name))
}

func (s *Server) handleStreamProxy(w http.ResponseWriter, r *http.Request) {
	targetURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if targetURL == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
	if err != nil {
		http.Error(w, "invalid upstream URL", http.StatusBadRequest)
		return
	}
	if accept := r.Header.Get("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	}
	if ua := r.Header.Get("User-Agent"); ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	client := s.streamClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("stream proxy: %v", err)
	}
}

func (s *Server) deviceID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state.DeviceID != "" {
		return s.state.DeviceID
	}
	return "LOCALDEVICE"
}

func (s *Server) writeXML(w http.ResponseWriter, fn func() ([]byte, error)) {
	data, err := fn()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeXMLBytes(w, data)
}

func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.ServerURL != "" {
		return strings.TrimRight(s.cfg.ServerURL, "/")
	}
	host := r.Host
	if host == "" {
		host = "127.0.0.1:" + strconv.Itoa(s.cfg.Port)
	}
	return "http://" + host
}

func writeXMLBytes(w http.ResponseWriter, data []byte) {
	writeXMLBytesStatus(w, http.StatusOK, data)
}

func writeXMLBytesStatus(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/vnd.bose.streaming-v1.2+xml")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func encodeURLSafe(raw string) string {
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

func prettyJSON(v interface{}) string {
	data, _ := json.MarshalIndent(v, "", "  ")
	return string(data)
}
