package main

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultWiFiSecurity = "wpa_or_wpa2"

type wifiOptions struct {
	Device      string
	SSID        string
	Password    string
	Security    string
	ScanTimeout int
}

type wirelessSurveyResponse struct {
	XMLName xml.Name             `xml:"PerformWirelessSiteSurveyResponse"`
	Error   string               `xml:"error,attr"`
	Items   []wirelessSurveyItem `xml:"items>item"`
}

type wirelessSurveyItem struct {
	SSID           string   `xml:"ssid,attr"`
	SignalStrength int      `xml:"signalStrength,attr"`
	Secure         bool     `xml:"secure,attr"`
	SecurityTypes  []string `xml:"securityTypes>type"`
}

type activeWirelessProfileResponse struct {
	SSID string `xml:"ssid"`
}

var wifiHTTPClient = &http.Client{Timeout: 20 * time.Second}

func wifi(args []string) error {
	fs := flag.NewFlagSet("wifi", flag.ExitOnError)
	device := fs.String("device", defaultDeviceAPI, "speaker API base URL")
	ssid := fs.String("ssid", "", "SSID to connect to; skips interactive scan selection when set")
	password := fs.String("password", "", "WiFi password; omit for open networks or to be prompted interactively")
	security := fs.String("security", "", "security type; defaults to visible network type or wpa_or_wpa2 when password is set")
	scanTimeout := fs.Int("scan-timeout", 5, "site survey timeout in seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runWiFi(wifiOptions{
		Device:      *device,
		SSID:        *ssid,
		Password:    *password,
		Security:    *security,
		ScanTimeout: *scanTimeout,
	}, os.Stdin, os.Stdout)
}

func runWiFi(opts wifiOptions, in io.Reader, out io.Writer) error {
	opts.Device = strings.TrimRight(opts.Device, "/")
	if opts.Device == "" {
		opts.Device = defaultDeviceAPI
	}
	if opts.ScanTimeout <= 0 {
		opts.ScanTimeout = 5
	}

	reader := bufio.NewReader(in)
	selected := wirelessSurveyItem{SSID: opts.SSID}
	if opts.SSID == "" {
		current, _ := currentWirelessProfile(opts.Device)
		if current != "" {
			fmt.Fprintf(out, "Current WiFi: %s\n\n", current)
		}
		networks, err := performWirelessSiteSurvey(opts.Device, opts.ScanTimeout)
		if err != nil {
			fmt.Fprintf(out, "Scan failed: %v\n", err)
		}
		selected, err = chooseWiFiNetwork(networks, reader, out)
		if err != nil {
			return err
		}
	}

	opts.SSID = strings.TrimSpace(selected.SSID)
	if opts.SSID == "" {
		return fmt.Errorf("SSID is required")
	}

	if opts.Security == "" {
		opts.Security = selected.preferredSecurity()
		if opts.Security == "" {
			if opts.Password == "" && !selected.Secure {
				opts.Security = "open"
			} else {
				opts.Security = defaultWiFiSecurity
			}
		}
	}
	if opts.Password == "" && opts.Security != "open" {
		fmt.Fprintf(out, "Password for %q: ", opts.SSID)
		pass, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		opts.Password = strings.TrimRight(pass, "\r\n")
	}

	fmt.Fprintf(out, "Adding WiFi profile for %q...\n", opts.SSID)
	if err := addWirelessProfile(opts.Device, opts.SSID, opts.Password, opts.Security); err != nil {
		return err
	}
	fmt.Fprintln(out, "WiFi profile accepted. The speaker may disconnect while it joins the network.")
	return nil
}

func (i wirelessSurveyItem) preferredSecurity() string {
	if len(i.SecurityTypes) > 0 {
		return strings.TrimSpace(i.SecurityTypes[0])
	}
	if i.Secure {
		return defaultWiFiSecurity
	}
	return "open"
}

func chooseWiFiNetwork(networks []wirelessSurveyItem, reader *bufio.Reader, out io.Writer) (wirelessSurveyItem, error) {
	networks = dedupeWiFiNetworks(networks)
	if len(networks) == 0 {
		fmt.Fprintln(out, "No visible networks found.")
		fmt.Fprint(out, "SSID to add: ")
		ssid, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return wirelessSurveyItem{}, err
		}
		return wirelessSurveyItem{SSID: strings.TrimSpace(ssid), Secure: true, SecurityTypes: []string{defaultWiFiSecurity}}, nil
	}

	fmt.Fprintln(out, "Visible WiFi networks:")
	for i, network := range networks {
		security := network.preferredSecurity()
		if security == "" {
			security = "unknown"
		}
		fmt.Fprintf(out, "  %d. %s (%d dBm, %s)\n", i+1, network.SSID, network.SignalStrength, security)
	}
	fmt.Fprintln(out, "  0. Enter another SSID")
	fmt.Fprint(out, "Select network: ")

	answer, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return wirelessSurveyItem{}, err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return wirelessSurveyItem{}, fmt.Errorf("cancelled")
	}
	if idx, err := strconv.Atoi(answer); err == nil {
		if idx == 0 {
			fmt.Fprint(out, "SSID to add: ")
			ssid, err := reader.ReadString('\n')
			if err != nil && err != io.EOF {
				return wirelessSurveyItem{}, err
			}
			return wirelessSurveyItem{SSID: strings.TrimSpace(ssid), Secure: true, SecurityTypes: []string{defaultWiFiSecurity}}, nil
		}
		if idx < 1 || idx > len(networks) {
			return wirelessSurveyItem{}, fmt.Errorf("selection %d is out of range", idx)
		}
		return networks[idx-1], nil
	}
	return wirelessSurveyItem{SSID: answer, Secure: true, SecurityTypes: []string{defaultWiFiSecurity}}, nil
}

func dedupeWiFiNetworks(networks []wirelessSurveyItem) []wirelessSurveyItem {
	bySSID := map[string]wirelessSurveyItem{}
	for _, network := range networks {
		if strings.TrimSpace(network.SSID) == "" {
			continue
		}
		current, ok := bySSID[network.SSID]
		if !ok || network.SignalStrength > current.SignalStrength {
			bySSID[network.SSID] = network
		}
	}
	out := make([]wirelessSurveyItem, 0, len(bySSID))
	for _, network := range bySSID {
		out = append(out, network)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SignalStrength == out[j].SignalStrength {
			return out[i].SSID < out[j].SSID
		}
		return out[i].SignalStrength > out[j].SignalStrength
	})
	return out
}

func currentWirelessProfile(device string) (string, error) {
	resp, err := wifiHTTPClient.Get(device + "/getActiveWirelessProfile")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GET /getActiveWirelessProfile returned %s", resp.Status)
	}
	var profile activeWirelessProfileResponse
	if err := xml.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return "", err
	}
	return profile.SSID, nil
}

func performWirelessSiteSurvey(device string, timeoutSeconds int) ([]wirelessSurveyItem, error) {
	body := fmt.Sprintf(`<PerformWirelessSiteSurvey timeout="%d"/>`, timeoutSeconds)
	resp, err := wifiHTTPClient.Post(device+"/performWirelessSiteSurvey", "text/xml", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("POST /performWirelessSiteSurvey returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	var survey wirelessSurveyResponse
	if err := xml.NewDecoder(resp.Body).Decode(&survey); err != nil {
		return nil, err
	}
	if survey.Error != "" && survey.Error != "none" {
		return nil, fmt.Errorf("site survey error: %s", survey.Error)
	}
	return survey.Items, nil
}

func addWirelessProfile(device, ssid, password, security string) error {
	body := fmt.Sprintf(
		`<AddWirelessProfile><profile ssid="%s" password="%s" securityType="%s" /></AddWirelessProfile>`,
		xmlAttributeEscape(ssid), xmlAttributeEscape(password), xmlAttributeEscape(security),
	)
	resp, err := wifiHTTPClient.Post(device+"/addWirelessProfile", "text/xml", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST /addWirelessProfile returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func xmlAttributeEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return strings.NewReplacer(`"`, "&quot;", `'`, "&#39;").Replace(buf.String())
}
