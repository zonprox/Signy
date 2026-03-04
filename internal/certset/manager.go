package certset

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zonprox/Signy/internal/crypto"
	"github.com/zonprox/Signy/internal/models"
	"github.com/zonprox/Signy/internal/storage"
)

// Manager handles certificate set CRUD operations.
type Manager struct {
	store     *storage.Manager
	logger    *slog.Logger
	masterKey string
	maxSets   int
}

// NewManager creates a cert set manager.
func NewManager(store *storage.Manager, logger *slog.Logger, masterKey string, maxSets int) *Manager {
	return &Manager{
		store:     store,
		logger:    logger,
		masterKey: masterKey,
		maxSets:   maxSets,
	}
}

// HasMasterKey returns true if encryption is enabled.
func (m *Manager) HasMasterKey() bool {
	return m.masterKey != ""
}

// Create creates a new certificate set.
func (m *Manager) Create(ctx context.Context, userID int64, name string, p12Data []byte, p12Password string, provData []byte) (*models.CertSet, error) {
	existing, err := m.List(userID)
	if err != nil {
		return nil, fmt.Errorf("list certsets: %w", err)
	}
	if len(existing) >= m.maxSets {
		return nil, fmt.Errorf("maximum number of certificate sets (%d) reached", m.maxSets)
	}

	for _, cs := range existing {
		if strings.EqualFold(cs.Name, name) {
			return nil, fmt.Errorf("certificate set name %q already exists", name)
		}
	}

	setID := uuid.New().String()[:8]
	dir := m.store.CertSetDir(userID, setID)
	if err := m.store.EnsureDir(dir); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}

	// Compute fingerprint
	fingerprint := crypto.FingerprintShort(p12Data)

	// Store P12
	if m.masterKey != "" {
		encrypted, err := crypto.EncryptFile(m.masterKey, fmt.Sprintf("p12-%s-%s", fmt.Sprintf("%d", userID), setID), p12Data)
		if err != nil {
			m.cleanupOnError(dir)
			return nil, fmt.Errorf("encrypt p12: %w", err)
		}
		if err := m.store.WriteFile(filepath.Join(dir, "p12.enc"), encrypted, 0600); err != nil {
			m.cleanupOnError(dir)
			return nil, fmt.Errorf("write p12.enc: %w", err)
		}

		// Store password encrypted
		encPass, err := crypto.EncryptFile(m.masterKey, fmt.Sprintf("pass-%s-%s", fmt.Sprintf("%d", userID), setID), []byte(p12Password))
		if err != nil {
			m.cleanupOnError(dir)
			return nil, fmt.Errorf("encrypt password: %w", err)
		}
		if err := m.store.WriteFile(filepath.Join(dir, "p12pass.enc"), encPass, 0600); err != nil {
			m.cleanupOnError(dir)
			return nil, fmt.Errorf("write p12pass.enc: %w", err)
		}
	} else {
		if err := m.store.WriteFile(filepath.Join(dir, "p12.p12"), p12Data, 0600); err != nil {
			m.cleanupOnError(dir)
			return nil, fmt.Errorf("write p12: %w", err)
		}
		// Without MASTER_KEY, password is NOT stored
	}

	// Store provision profile
	if err := m.store.WriteFile(filepath.Join(dir, "provision.mobileprovision"), provData, 0644); err != nil {
		m.cleanupOnError(dir)
		return nil, fmt.Errorf("write provision: %w", err)
	}

	// Build provision summary (best-effort parsing)
	provSummary := parseProvisionSummary(provData)

	now := time.Now().UTC()
	cs := &models.CertSet{
		SetID:               setID,
		UserID:              userID,
		Name:                name,
		CreatedAt:           now,
		UpdatedAt:           now,
		StatusValid:         models.CertSetStatusValid,
		LastCheckedAt:       &now,
		P12FingerprintShort: fingerprint,
		ProvisionSummary:    provSummary,
	}

	// Write meta
	metaData, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		m.cleanupOnError(dir)
		return nil, fmt.Errorf("marshal meta: %w", err)
	}
	if err := m.store.WriteFile(filepath.Join(dir, "meta.json"), metaData, 0644); err != nil {
		m.cleanupOnError(dir)
		return nil, fmt.Errorf("write meta: %w", err)
	}

	// Auto-set as default if user has none
	defaultID, _ := m.store.ReadDefaultSetID(userID)
	if defaultID == "" {
		_ = m.store.WriteDefaultSetID(userID, setID) //nolint:errcheck // best-effort auto-default
	}

	return cs, nil
}

// Get returns a cert set by ID.
func (m *Manager) Get(userID int64, setID string) (*models.CertSet, error) {
	dir := m.store.CertSetDir(userID, setID)
	metaPath := filepath.Join(dir, "meta.json")
	data, err := m.store.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("read meta: %w", err)
	}
	var cs models.CertSet
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil, fmt.Errorf("unmarshal meta: %w", err)
	}
	return &cs, nil
}

// List returns all cert sets for a user.
func (m *Manager) List(userID int64) ([]*models.CertSet, error) {
	certsDir := filepath.Join(m.store.UserDir(userID), "certsets")
	entries, err := os.ReadDir(certsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read certsets dir: %w", err)
	}

	var result []*models.CertSet
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cs, err := m.Get(userID, entry.Name())
		if err != nil {
			m.logger.Warn("failed to read cert set", "set_id", entry.Name(), "error", err)
			continue
		}
		result = append(result, cs)
	}
	return result, nil
}

// GetDefault returns the default cert set for a user.
func (m *Manager) GetDefault(userID int64) (*models.CertSet, error) {
	defaultID, err := m.store.ReadDefaultSetID(userID)
	if err != nil {
		return nil, fmt.Errorf("read default: %w", err)
	}
	if defaultID == "" {
		return nil, nil
	}
	return m.Get(userID, defaultID)
}

// GetDefaultID returns the default cert set ID for a user.
func (m *Manager) GetDefaultID(userID int64) (string, error) {
	return m.store.ReadDefaultSetID(userID)
}

// SetDefault sets the default cert set for a user.
func (m *Manager) SetDefault(userID int64, setID string) error {
	// Verify set exists
	if _, err := m.Get(userID, setID); err != nil {
		return fmt.Errorf("cert set not found: %w", err)
	}
	return m.store.WriteDefaultSetID(userID, setID)
}

// Delete removes a cert set and all its files.
func (m *Manager) Delete(userID int64, setID string) error {
	dir := m.store.CertSetDir(userID, setID)
	if err := m.store.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove certset dir: %w", err)
	}

	// If this was the default, remove the default pointer
	defaultID, _ := m.store.ReadDefaultSetID(userID)
	if defaultID == setID {
		_ = m.store.RemoveDefaultSetID(userID) //nolint:errcheck // best-effort cleanup
	}

	return nil
}

// CheckStatus validates a cert set and returns the status.
func (m *Manager) CheckStatus(userID int64, setID string) (models.CertSetStatus, string, error) {
	dir := m.store.CertSetDir(userID, setID)

	// Check meta exists
	if !m.store.FileExists(filepath.Join(dir, "meta.json")) {
		return models.CertSetStatusMissingFiles, "meta.json not found", nil
	}

	// Check P12 exists
	p12Exists := m.store.FileExists(filepath.Join(dir, "p12.enc")) || m.store.FileExists(filepath.Join(dir, "p12.p12"))
	if !p12Exists {
		return models.CertSetStatusMissingFiles, "P12 file not found", nil
	}

	// Check provision exists
	if !m.store.FileExists(filepath.Join(dir, "provision.mobileprovision")) {
		return models.CertSetStatusMissingFiles, "Provisioning profile not found", nil
	}

	// If encrypted, try to decrypt p12 password
	if m.masterKey != "" {
		passPath := filepath.Join(dir, "p12pass.enc")
		if m.store.FileExists(passPath) {
			passData, err := m.store.ReadFile(passPath)
			if err != nil {
				return models.CertSetStatusDecryptFail, "Could not read encrypted password", nil
			}
			_, err = crypto.DecryptFile(m.masterKey, fmt.Sprintf("pass-%d-%s", userID, setID), passData)
			if err != nil {
				return models.CertSetStatusDecryptFail, "Could not decrypt P12 password (MASTER_KEY may have changed)", nil
			}
		}
	}

	// Update last_checked_at in meta
	cs, err := m.Get(userID, setID)
	if err != nil {
		return models.CertSetStatusUnknown, "Could not read cert set metadata", nil
	}
	now := time.Now().UTC()
	cs.LastCheckedAt = &now
	cs.StatusValid = models.CertSetStatusValid
	cs.UpdatedAt = now

	metaData, _ := json.MarshalIndent(cs, "", "  ")
	_ = m.store.WriteFile(filepath.Join(dir, "meta.json"), metaData, 0644) //nolint:errcheck // best-effort meta update

	return models.CertSetStatusValid, "All files present and accessible", nil
}

// GetP12Path returns the path to the P12 file (encrypted or plain).
func (m *Manager) GetP12Path(userID int64, setID string) string {
	dir := m.store.CertSetDir(userID, setID)
	if m.masterKey != "" {
		return filepath.Join(dir, "p12.enc")
	}
	return filepath.Join(dir, "p12.p12")
}

// GetProvisionPath returns the path to the provisioning profile.
func (m *Manager) GetProvisionPath(userID int64, setID string) string {
	return filepath.Join(m.store.CertSetDir(userID, setID), "provision.mobileprovision")
}

// GetP12Password retrieves the P12 password for a cert set.
// Returns ("", nil) if MASTER_KEY is not set (password not stored).
func (m *Manager) GetP12Password(userID int64, setID string) (string, error) {
	if m.masterKey == "" {
		return "", nil
	}
	dir := m.store.CertSetDir(userID, setID)
	passData, err := m.store.ReadFile(filepath.Join(dir, "p12pass.enc"))
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	plain, err := crypto.DecryptFile(m.masterKey, fmt.Sprintf("pass-%d-%s", userID, setID), passData)
	if err != nil {
		return "", fmt.Errorf("decrypt password: %w", err)
	}
	return string(plain), nil
}

// UpdateLastUsed updates the last_used_at timestamp.
func (m *Manager) UpdateLastUsed(userID int64, setID string) {
	cs, err := m.Get(userID, setID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	cs.LastUsedAt = &now
	cs.UpdatedAt = now
	metaData, _ := json.MarshalIndent(cs, "", "  ")
	dir := m.store.CertSetDir(userID, setID)
	_ = m.store.WriteFile(filepath.Join(dir, "meta.json"), metaData, 0644) //nolint:errcheck // best-effort meta update
}

func (m *Manager) cleanupOnError(dir string) {
	if err := os.RemoveAll(dir); err != nil {
		m.logger.Warn("cleanup on error failed", "dir", dir, "error", err)
	}
}

// parseProvisionSummary attempts best-effort parsing of mobileprovision XML.
func parseProvisionSummary(data []byte) string {
	content := string(data)

	// Best-effort: look for TeamIdentifier and application-identifier
	teamID := extractPlistValue(content, "TeamIdentifier")
	appID := extractPlistValue(content, "application-identifier")
	teamName := extractPlistValue(content, "TeamName")

	parts := []string{}
	if teamName != "" {
		parts = append(parts, "Team: "+teamName)
	}
	if teamID != "" {
		parts = append(parts, "TeamID: "+teamID)
	}
	if appID != "" {
		parts = append(parts, "AppID: "+appID)
	}

	if len(parts) == 0 {
		return "Unknown profile"
	}
	return strings.Join(parts, " | ")
}

// extractPlistValue does best-effort extraction of a value after a key in plist XML.
func extractPlistValue(content, key string) string {
	keyTag := "<key>" + key + "</key>"
	idx := strings.Index(content, keyTag)
	if idx < 0 {
		return ""
	}
	rest := content[idx+len(keyTag):]

	// Look for <string>...</string> or <array><string>...</string>
	for _, prefix := range []string{"<string>", "\n\t\t<string>", "\n\t<string>", "\n<string>"} {
		trimmed := strings.TrimSpace(rest)
		if strings.HasPrefix(trimmed, "<string>") {
			start := strings.Index(trimmed, "<string>") + len("<string>")
			end := strings.Index(trimmed[start:], "</string>")
			if end > 0 {
				return trimmed[start : start+end]
			}
		}
		if strings.HasPrefix(trimmed, "<array>") {
			// Get first string in array
			arrayContent := trimmed[len("<array>"):]
			start := strings.Index(arrayContent, "<string>")
			if start >= 0 {
				start += len("<string>")
				end := strings.Index(arrayContent[start:], "</string>")
				if end > 0 {
					return arrayContent[start : start+end]
				}
			}
		}
		_ = prefix
	}
	return ""
}
