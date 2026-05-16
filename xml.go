package main

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`
	dateStr   = "2012-09-19T12:43:00.000+00:00"
)

type sourceDef struct {
	ID   int
	Name string
}

var tinyProviders = []sourceDef{
	{ID: 2, Name: sourceInternetRadio},
	{ID: 9, Name: sourceAux},
	{ID: 11, Name: sourceLocalInternetRadio},
	{ID: 25, Name: sourceTuneIn},
}

type xmlSourceProvider struct {
	ID        int    `xml:"id,attr"`
	CreatedOn string `xml:"createdOn"`
	Name      string `xml:"name"`
	UpdatedOn string `xml:"updatedOn"`
}

type xmlSourceProviders struct {
	XMLName   xml.Name            `xml:"sourceProviders"`
	Providers []xmlSourceProvider `xml:"sourceprovider"`
}

type xmlCredential struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type xmlSource struct {
	ID               string         `xml:"id,attr,omitempty"`
	Type             string         `xml:"type,attr,omitempty"`
	CreatedOn        string         `xml:"createdOn,omitempty"`
	Credential       *xmlCredential `xml:"credential,omitempty"`
	Name             string         `xml:"name"`
	SourceProviderID string         `xml:"sourceproviderid,omitempty"`
	SourceName       string         `xml:"sourcename"`
	SourceSettings   string         `xml:"sourceSettings"`
	UpdatedOn        string         `xml:"updatedOn,omitempty"`
	Username         string         `xml:"username"`
}

type xmlPreset struct {
	ButtonNumber    string     `xml:"buttonNumber,attr,omitempty"`
	ContainerArt    string     `xml:"containerArt"`
	ContentItemType string     `xml:"contentItemType"`
	CreatedOn       string     `xml:"createdOn"`
	Location        string     `xml:"location"`
	Name            string     `xml:"name"`
	Source          *xmlSource `xml:"source,omitempty"`
	UpdatedOn       string     `xml:"updatedOn"`
	Username        string     `xml:"username"`
}

type xmlPresets struct {
	XMLName xml.Name    `xml:"presets"`
	Presets []xmlPreset `xml:"preset"`
}

type xmlAccount struct {
	XMLName           xml.Name             `xml:"account"`
	ID                string               `xml:"id,attr"`
	AccountStatus     string               `xml:"accountStatus"`
	Devices           []xmlAccountDevice   `xml:"devices>device"`
	Mode              string               `xml:"mode"`
	PreferredLanguage string               `xml:"preferredLanguage"`
	ProviderSettings  []xmlProviderSetting `xml:"providerSettings>providerSetting"`
	Sources           []xmlSource          `xml:"sources>source"`
}

type xmlAccountDevice struct {
	DeviceID        string             `xml:"deviceid,attr"`
	AttachedProduct xmlAttachedProduct `xml:"attachedProduct"`
	CreatedOn       string             `xml:"createdOn"`
	FirmwareVersion string             `xml:"firmwareVersion"`
	IPAddress       string             `xml:"ipaddress"`
	Name            string             `xml:"name"`
	Presets         []xmlPreset        `xml:"presets>preset,omitempty"`
	Recents         []string           `xml:"recents>recent,omitempty"`
	SerialNumber    string             `xml:"serialNumber,omitempty"`
	UpdatedOn       string             `xml:"updatedOn"`
}

type xmlMargeDevice struct {
	DeviceID        string             `xml:"deviceid,attr"`
	AttachedProduct xmlAttachedProduct `xml:"attachedProduct"`
	CreatedOn       string             `xml:"createdOn"`
	IPAddress       string             `xml:"ipaddress"`
	Name            string             `xml:"name"`
	UpdatedOn       string             `xml:"updatedOn"`
}

type xmlAttachedProduct struct {
	ProductCode  string `xml:"product_code,attr"`
	ProductLabel string `xml:"productlabel"`
	SerialNumber string `xml:"serialnumber"`
	UpdatedOn    string `xml:"updatedOn"`
}

type xmlDevices struct {
	XMLName          xml.Name             `xml:"devices"`
	Devices          []xmlMargeDevice     `xml:"device"`
	ProviderSettings []xmlProviderSetting `xml:"providerSettings>providerSetting"`
}

type xmlSources struct {
	XMLName xml.Name    `xml:"sources"`
	Sources []xmlSource `xml:"source"`
}

type xmlProviderSetting struct {
	BoseID     string `xml:"boseId"`
	KeyName    string `xml:"keyName"`
	Value      string `xml:"value"`
	ProviderID string `xml:"providerId"`
}

type xmlToken struct {
	XMLName     xml.Name `xml:"token"`
	AccessToken string   `xml:"accessToken"`
	TokenType   string   `xml:"tokenType"`
	ExpiresIn   int      `xml:"expiresIn"`
}

func (s *Server) sourceProvidersXML() ([]byte, error) {
	resp := xmlSourceProviders{}
	for _, p := range tinyProviders {
		resp.Providers = append(resp.Providers, xmlSourceProvider{
			ID:        p.ID,
			CreatedOn: dateStr,
			Name:      p.Name,
			UpdatedOn: dateStr,
		})
	}
	return marshalXML(resp)
}

func (s *Server) accountFullXML(account, baseURL string) ([]byte, error) {
	resp := xmlAccount{
		ID:                account,
		AccountStatus:     "OK",
		Mode:              "NORMAL",
		PreferredLanguage: "en",
		ProviderSettings:  providerSettings(account),
		Sources:           s.sources(),
		Devices: []xmlAccountDevice{{
			DeviceID:        s.deviceID(),
			AttachedProduct: s.attachedProduct(),
			CreatedOn:       dateStr,
			FirmwareVersion: s.state.FirmwareVersion,
			IPAddress:       s.state.IPAddress,
			Name:            s.state.DeviceName,
			Presets:         s.presets(baseURL),
			Recents:         []string{},
			SerialNumber:    firstNonEmpty(s.state.DeviceSerialNumber, s.deviceID()),
			UpdatedOn:       dateStr,
		}},
	}
	return marshalXML(resp)
}

func (s *Server) accountSourcesXML() ([]byte, error) {
	return marshalXML(xmlSources{Sources: s.sources()})
}

func (s *Server) accountDevicesXML(account string) ([]byte, error) {
	resp := xmlDevices{
		ProviderSettings: providerSettings(account),
		Devices: []xmlMargeDevice{{
			DeviceID:        s.deviceID(),
			AttachedProduct: s.attachedProduct(),
			CreatedOn:       dateStr,
			IPAddress:       s.state.IPAddress,
			Name:            s.state.DeviceName,
			UpdatedOn:       dateStr,
		}},
	}
	return marshalXML(resp)
}

func (s *Server) presetsXML(baseURL string) ([]byte, error) {
	return marshalXML(xmlPresets{Presets: s.presets(baseURL)})
}

func streamingTokenXML() ([]byte, error) {
	return marshalXML(xmlToken{
		AccessToken: "tiny-" + strconv.FormatInt(time.Now().Unix(), 10),
		TokenType:   "Bearer",
		ExpiresIn:   3600,
	})
}

func apiVersionsXML() []byte {
	return []byte(xmlHeader + `
<versions>
  <version>221</version>
  <project>soundtouch-tiny</project>
  <api type="streaming" xml="application/vnd.bose.streaming-v1.0+xml" json="application/vnd.bose.streaming-v1.0+json"></api>
  <api type="support" xml="application/vnd.bose.support-v1.0+xml" json="application/vnd.bose.support-v1.0+json"></api>
</versions>`)
}

func softwareUpdateXML() []byte {
	return []byte(xmlHeader + `
<software_update>
  <softwareUpdateLocation></softwareUpdateLocation>
</software_update>`)
}

func (s *Server) sources() []xmlSource {
	sources := []xmlSource{
		sourceXML("10001", sourceAux, "9", "", ""),
		sourceXML("10002", sourceInternetRadio, "2", "", ""),
		sourceXML("10003", sourceLocalInternetRadio, "11", generateSerialSecret("local-internet-radio"), "token"),
		sourceXML("10004", sourceTuneIn, "25", generateSerialSecret("tunein"), "token"),
	}
	return sources
}

func (s *Server) presets(baseURL string) []xmlPreset {
	presets := make([]xmlPreset, 0, len(s.cfg.Presets))
	for _, p := range s.cfg.Presets {
		src := sourceForPreset(p)
		location := p.Location
		if p.Source == sourceLocalInternetRadio && location == "" && p.StreamURL != "" {
			location = strings.TrimRight(baseURL, "/") + orionPath + "/station?data=" + encodeCustomStream(p.StreamURL, p.Art, p.Name)
		}
		presets = append(presets, xmlPreset{
			ButtonNumber:    strconv.Itoa(p.Slot),
			ContainerArt:    p.Art,
			ContentItemType: p.Type,
			CreatedOn:       dateStr,
			Location:        location,
			Name:            p.Name,
			Source:          &src,
			UpdatedOn:       dateStr,
			Username:        firstNonEmpty(p.SourceAccount, p.Name),
		})
	}
	sort.Slice(presets, func(i, j int) bool {
		return presets[i].ButtonNumber < presets[j].ButtonNumber
	})
	return presets
}

func sourceForPreset(p PresetConfig) xmlSource {
	switch p.Source {
	case sourceLocalInternetRadio:
		return sourceXML(sourceLocalInternetRadio, sourceLocalInternetRadio, "11", generateSerialSecret("local-internet-radio"), "token")
	case sourceTuneIn:
		return sourceXML(sourceTuneIn, sourceTuneIn, "25", generateSerialSecret("tunein"), "token")
	case sourceAux:
		return sourceXML(sourceAux, sourceAux, "9", "", "")
	default:
		return sourceXML(p.Source, p.Source, providerIDForSource(p.Source), "", "")
	}
}

func sourceXML(id, sourceType, providerID, secret, secretType string) xmlSource {
	src := xmlSource{
		ID:               id,
		Type:             "Audio",
		CreatedOn:        dateStr,
		Name:             sourceType,
		SourceProviderID: providerID,
		SourceName:       sourceType,
		SourceSettings:   "",
		UpdatedOn:        dateStr,
		Username:         sourceType,
	}
	if sourceType == sourceTuneIn {
		src.Name = ""
		src.SourceName = ""
		src.Username = ""
	}
	if secret != "" || secretType != "" {
		src.Credential = &xmlCredential{Type: firstNonEmpty(secretType, "token"), Value: secret}
	}
	return src
}

func providerIDForSource(source string) string {
	for _, p := range tinyProviders {
		if p.Name == source {
			return strconv.Itoa(p.ID)
		}
	}
	return ""
}

func providerSettings(account string) []xmlProviderSetting {
	return []xmlProviderSetting{
		{BoseID: account, KeyName: "ELIGIBLE_FOR_TRIAL", Value: "false", ProviderID: "14"},
		{BoseID: account, KeyName: "STREAMING_QUALITY", Value: "2", ProviderID: "15"},
	}
}

func (s *Server) attachedProduct() xmlAttachedProduct {
	return xmlAttachedProduct{
		ProductCode:  s.state.ProductCode,
		ProductLabel: s.state.ProductCode,
		SerialNumber: firstNonEmpty(s.state.ProductSerialNumber, s.state.DeviceSerialNumber, s.deviceID()),
		UpdatedOn:    dateStr,
	}
}

func marshalXML(v interface{}) ([]byte, error) {
	data, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xmlHeader+"\n"), data...), nil
}

func generateSerialSecret(serial string) string {
	data := []byte(fmt.Sprintf(`{"serial":%q}`, serial))
	return base64.StdEncoding.EncodeToString(data)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
