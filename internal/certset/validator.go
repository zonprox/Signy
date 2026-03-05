package certset

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/pkcs12"
	"howett.net/plist"
)

// CertificateInfo contains extracted parsed data from a P12 cert.
type CertificateInfo struct {
	SubjectCN      string
	ExpirationDate time.Time
	Country        string
	Fingerprint    string
}

// ProvisionInfo contains extracted parsed data from a mobileprovision profile.
type ProvisionInfo struct {
	AppID              string
	TeamName           string
	TeamID             string
	ExpirationDate     time.Time
	ProvisionedDevices []string
	IsAdHoc            bool
}

// ValidateP12 parses the provided P12 bytes and password, returning details if valid.
func ValidateP12(p12Data []byte, password string) (*CertificateInfo, error) {
	// Decode P12 safely
	privateKey, cert, err := pkcs12.Decode(p12Data, password)
	if err != nil {
		return nil, fmt.Errorf("failed to decode P12 (invalid password or corrupt file): %w", err)
	}

	if privateKey == nil || cert == nil {
		return nil, fmt.Errorf("P12 file does not contain both private key and certificate")
	}

	// Verify the cert is currently physically active
	now := time.Now()
	if now.Before(cert.NotBefore) {
		return nil, fmt.Errorf("certificate is not yet valid (starts: %s)", cert.NotBefore)
	}
	if now.After(cert.NotAfter) {
		return nil, fmt.Errorf("certificate has expired (expired: %s)", cert.NotAfter)
	}

	country := "Unknown"
	if len(cert.Subject.Country) > 0 {
		country = cert.Subject.Country[0]
	}

	// Use existing utility or create simple sha1 if missing. The manager handles short fingerprinting.
	// But let's just surface basic metadata.
	return &CertificateInfo{
		SubjectCN:      cert.Subject.CommonName,
		ExpirationDate: cert.NotAfter,
		Country:        country,
	}, nil
}

// ValidateProvision extracts the XML plist from the PKCS7 CMS structure
// and parses the entitlements and devices.
func ValidateProvision(provData []byte) (*ProvisionInfo, error) {
	// A standard .mobileprovision is a signed DER PKCS7 message wrapping an XML plist.
	// Easiest, most robust extraction without heavy ASN1 parsing:
	// Find <?xml and </plist>
	startTag := []byte("<?xml")
	endTag := []byte("</plist>")

	startIdx := bytes.Index(provData, startTag)
	if startIdx == -1 {
		return nil, fmt.Errorf("failed to find start of XML plist in provisioning profile")
	}

	endIdx := bytes.Index(provData[startIdx:], endTag)
	if endIdx == -1 {
		return nil, fmt.Errorf("failed to find end of XML plist in provisioning profile")
	}

	// Extract the pure XML String
	plistData := provData[startIdx : startIdx+endIdx+len(endTag)]

	// Define our extraction skeleton
	var profile struct {
		AppIDName            string    `plist:"AppIDName"`
		TeamName             string    `plist:"TeamName"`
		TeamIdentifier       []string  `plist:"TeamIdentifier"`
		ExpirationDate       time.Time `plist:"ExpirationDate"`
		Entitlements         struct {
			ApplicationIdentifier string `plist:"application-identifier"`
			GetTaskAllow          bool   `plist:"get-task-allow"`
		} `plist:"Entitlements"`
		ProvisionedDevices []string          `plist:"ProvisionedDevices"`
		ProvisionsAllDevices bool            `plist:"ProvisionsAllDevices"`
	}

	decoder := plist.NewDecoder(bytes.NewReader(plistData))
	if err := decoder.Decode(&profile); err != nil {
		return nil, fmt.Errorf("failed to parse provisioning XML: %w", err)
	}

	now := time.Now()
	if now.After(profile.ExpirationDate) {
		return nil, fmt.Errorf("provisioning profile has expired (expired: %s)", profile.ExpirationDate)
	}

	teamID := ""
	if len(profile.TeamIdentifier) > 0 {
		teamID = profile.TeamIdentifier[0]
	}

	// Calculate Ad-Hoc / Enterprise status
	// If it has ProvisionedDevices -> Development / AdHoc
	// If ProvisionsAllDevices == true -> Enterprise (In-House)
	// If neither -> App Store (cannot be used for arbitrary sideloading usually)
	isAdHoc := len(profile.ProvisionedDevices) > 0 && !profile.ProvisionsAllDevices

	// STRICT 1-DEVICE CHECK FOR AD-HOC (As requested by User)
	if isAdHoc && len(profile.ProvisionedDevices) != 1 {
		return nil, fmt.Errorf("Ad-Hoc certificate rejected: exactly 1 device UDID is required, but found %d", len(profile.ProvisionedDevices))
	}
	
	appID := profile.Entitlements.ApplicationIdentifier
	if idx := strings.Index(appID, "."); idx != -1 {
		appID = appID[idx+1:] // strip TeamID prefix from app-id
	}

	return &ProvisionInfo{
		AppID:              appID,
		TeamName:           profile.TeamName,
		TeamID:             teamID,
		ExpirationDate:     profile.ExpirationDate,
		ProvisionedDevices: profile.ProvisionedDevices,
		IsAdHoc:            isAdHoc,
	}, nil
}
