package main

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultDeviceAPI = "http://127.0.0.1:8090"

type configureOptions struct {
	DeviceURL string
	In        io.Reader
	Out       io.Writer
	Client    *http.Client
}

type devicePresets struct {
	XMLName xml.Name       `xml:"presets"`
	Presets []devicePreset `xml:"preset"`
}

type devicePreset struct {
	ID          int                `xml:"id,attr"`
	ContentItem *deviceContentItem `xml:"ContentItem"`
}

type deviceContentItem struct {
	Source        string `xml:"source,attr"`
	Type          string `xml:"type,attr"`
	Location      string `xml:"location,attr"`
	SourceAccount string `xml:"sourceAccount,attr"`
	IsPresetable  bool   `xml:"isPresetable,attr"`
	ItemName      string `xml:"itemName,omitempty"`
	ContainerArt  string `xml:"containerArt,omitempty"`
}

type tuneInStation struct {
	Name     string
	Subtitle string
	GuideID  string
	ImageURL string
}

func configure(args []string) error {
	fs := flag.NewFlagSet("configure", flag.ExitOnError)
	deviceURL := fs.String("device", defaultDeviceAPI, "local SoundTouch device API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runConfigure(configureOptions{
		DeviceURL: *deviceURL,
		In:        os.Stdin,
		Out:       os.Stdout,
		Client:    configureHTTPClient(),
	})
}

func configureHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return &http.Client{Timeout: 20 * time.Second, Transport: transport}
}

func runConfigure(opts configureOptions) error {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Client == nil {
		opts.Client = configureHTTPClient()
	}
	opts.DeviceURL = strings.TrimRight(firstNonEmpty(opts.DeviceURL, defaultDeviceAPI), "/")

	reader := bufio.NewReader(opts.In)
	for {
		presets, err := fetchDevicePresets(opts.Client, opts.DeviceURL)
		if err != nil {
			return err
		}
		printDevicePresets(opts.Out, presets)

		slot, ok, err := askPresetSlot(reader, opts.Out)
		if err != nil || !ok {
			return err
		}

		query, ok, err := askLine(reader, opts.Out, "Search station name (empty cancels): ")
		if err != nil || !ok {
			return err
		}
		stations, err := searchTuneInStations(opts.Client, query)
		if err != nil {
			return err
		}
		if len(stations) == 0 {
			fmt.Fprintln(opts.Out, "No stations found.")
			continue
		}

		selected, ok, err := askStationChoice(reader, opts.Out, stations)
		if err != nil || !ok {
			return err
		}
		if err := storeTuneInPreset(opts.Client, opts.DeviceURL, slot, selected); err != nil {
			return err
		}
		fmt.Fprintf(opts.Out, "Preset %d set to %s.\n\n", slot, selected.Name)
	}
}

func fetchDevicePresets(client *http.Client, deviceURL string) (*devicePresets, error) {
	resp, err := client.Get(deviceURL + "/presets")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GET /presets returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var presets devicePresets
	if err := xml.NewDecoder(resp.Body).Decode(&presets); err != nil {
		return nil, err
	}
	return &presets, nil
}

func printDevicePresets(out io.Writer, presets *devicePresets) {
	bySlot := map[int]devicePreset{}
	if presets != nil {
		for _, p := range presets.Presets {
			bySlot[p.ID] = p
		}
	}
	fmt.Fprintln(out, "Current presets:")
	for slot := 1; slot <= 6; slot++ {
		p, ok := bySlot[slot]
		if !ok || p.ContentItem == nil || p.ContentItem.ItemName == "" {
			fmt.Fprintf(out, "  %d. <empty>\n", slot)
			continue
		}
		fmt.Fprintf(out, "  %d. %s (%s)\n", slot, p.ContentItem.ItemName, p.ContentItem.Source)
		if p.ContentItem.Location != "" {
			fmt.Fprintf(out, "     %s\n", p.ContentItem.Location)
		}
	}
	fmt.Fprintln(out)
}

func askPresetSlot(reader *bufio.Reader, out io.Writer) (slot int, ok bool, err error) {
	for {
		answer, ok, err := askLine(reader, out, "Preset to change 1-6 (empty exits): ")
		if err != nil || !ok {
			return 0, ok, err
		}
		slot, err := strconv.Atoi(answer)
		if err == nil && slot >= 1 && slot <= 6 {
			return slot, true, nil
		}
		fmt.Fprintln(out, "Enter a number from 1 to 6, or empty to exit.")
	}
}

func askStationChoice(reader *bufio.Reader, out io.Writer, stations []tuneInStation) (tuneInStation, bool, error) {
	limit := len(stations)
	if limit > 10 {
		limit = 10
	}
	fmt.Fprintln(out, "Search results:")
	for i := 0; i < limit; i++ {
		s := stations[i]
		if s.Subtitle == "" {
			fmt.Fprintf(out, "  %d. %s [%s]\n", i+1, s.Name, s.GuideID)
		} else {
			fmt.Fprintf(out, "  %d. %s - %s [%s]\n", i+1, s.Name, s.Subtitle, s.GuideID)
		}
	}
	for {
		answer, ok, err := askLine(reader, out, "Select station (empty cancels): ")
		if err != nil || !ok {
			return tuneInStation{}, ok, err
		}
		choice, err := strconv.Atoi(answer)
		if err == nil && choice >= 1 && choice <= limit {
			return stations[choice-1], true, nil
		}
		fmt.Fprintf(out, "Enter a number from 1 to %d, or empty to cancel.\n", limit)
	}
}

func askLine(reader *bufio.Reader, out io.Writer, prompt string) (string, bool, error) {
	fmt.Fprint(out, prompt)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", false, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false, nil
	}
	return line, true, nil
}

func searchTuneInStations(client *http.Client, query string) ([]tuneInStation, error) {
	searchURL := fmt.Sprintf(tuneInSearch, url.QueryEscape(query))
	resp, err := client.Get(searchURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("TuneIn search returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Items []struct {
			Type          string `json:"Type"`
			ContainerType string `json:"ContainerType"`
			Children      []struct {
				Type     string `json:"Type"`
				GuideID  string `json:"GuideId"`
				Image    string `json:"Image"`
				Title    string `json:"Title"`
				Subtitle string `json:"Subtitle"`
			} `json:"Children"`
		} `json:"Items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	var stations []tuneInStation
	seen := map[string]bool{}
	for _, item := range payload.Items {
		if item.Type != "Container" || item.ContainerType == "NotPlayableStations" {
			continue
		}
		for _, child := range item.Children {
			if child.Type != "Station" || child.GuideID == "" || child.Title == "" || seen[child.GuideID] {
				continue
			}
			seen[child.GuideID] = true
			stations = append(stations, tuneInStation{
				Name:     child.Title,
				Subtitle: child.Subtitle,
				GuideID:  child.GuideID,
				ImageURL: child.Image,
			})
		}
	}
	return stations, nil
}

func storeTuneInPreset(client *http.Client, deviceURL string, slot int, station tuneInStation) error {
	body, err := tuneInPresetXML(slot, station)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(deviceURL, "/")+"/storePreset", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/xml")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("POST /storePreset returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func tuneInPresetXML(slot int, station tuneInStation) (string, error) {
	now := time.Now().Unix()
	preset := devicePreset{
		ID: slot,
		ContentItem: &deviceContentItem{
			Source:        sourceTuneIn,
			Type:          "stationurl",
			Location:      "/v1/playback/station/" + station.GuideID,
			SourceAccount: "",
			IsPresetable:  true,
			ItemName:      station.Name,
			ContainerArt:  station.ImageURL,
		},
	}
	type presetXML struct {
		XMLName     xml.Name           `xml:"preset"`
		ID          int                `xml:"id,attr"`
		CreatedOn   int64              `xml:"createdOn,attr"`
		UpdatedOn   int64              `xml:"updatedOn,attr"`
		ContentItem *deviceContentItem `xml:"ContentItem"`
	}
	data, err := xml.MarshalIndent(presetXML{
		ID:          preset.ID,
		CreatedOn:   now,
		UpdatedOn:   now,
		ContentItem: preset.ContentItem,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return xml.Header + string(data), nil
}
