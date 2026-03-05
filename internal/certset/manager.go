package certset

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

// Create creates a new certificate set.
func (m *Manager) Create(ctx context.Context, userID int64, p12Data []byte, p12Password string, provData []byte) (*models.CertSet, error) {
	existing, err := m.List(userID)
	if err != nil {
		return nil, fmt.Errorf("list certsets: %w", err)
	}
	if len(existing) >= m.maxSets {
		return nil, fmt.Errorf("maximum number of certificate sets (%d) reached", m.maxSets)
	}

	// Validate files directly BEFORE writing anything to disk
	p12Info, err := ValidateP12(p12Data, p12Password)
	if err != nil {
		return nil, fmt.Errorf("invalid P12 certificate: %w", err)
	}

	provInfo, err := ValidateProvision(provData)
	if err != nil {
		return nil, fmt.Errorf("invalid provisioning profile: %w", err)
	}

	name := p12Info.SubjectCN
	if name == "" {
		name = provInfo.TeamName
	}
	if name == "" {
		name = "Unnamed Certificate"
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
	}

	// Store provision profile
	if err := m.store.WriteFile(filepath.Join(dir, "provision.mobileprovision"), provData, 0644); err != nil {
		m.cleanupOnError(dir)
		return nil, fmt.Errorf("write provision: %w", err)
	}

	now := time.Now().UTC()
	
	certType := "Ad-Hoc (1 Device)"

	cs := &models.CertSet{
		SetID:               setID,
		UserID:              userID,
		Name:                name,
		CreatedAt:           now,
		UpdatedAt:           now,
		StatusValid:         models.CertSetStatusValid,
		LastCheckedAt:       &now,
		P12FingerprintShort: fingerprint,
		ProvisionSummary:    fmt.Sprintf("Team: %s | AppID: %s", provInfo.TeamName, provInfo.AppID),
		CertSubjectCN:       p12Info.SubjectCN,
		CertExpiration:      p12Info.ExpirationDate,
		CertCountry:         p12Info.Country,
		CertType:            certType,
		ProvDevicesCount:    len(provInfo.ProvisionedDevices),
		HasPassword:         p12Password != "",
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

// ListAll returns all cert sets across all users (for Admin Dashboard).
func (m *Manager) ListAll() ([]*models.CertSet, error) {
	usersDir := filepath.Join(m.store.BasePath(), "users")
	userEntries, err := os.ReadDir(usersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read users dir: %w", err)
	}

	var allCerts []*models.CertSet
	for _, ue := range userEntries {
		if !ue.IsDir() {
			continue
		}
		// Try to parse the directory name as user ID
		var userID int64
		if _, err := fmt.Sscanf(ue.Name(), "%d", &userID); err != nil {
			continue
		}

		userCerts, err := m.List(userID)
		if err == nil {
			allCerts = append(allCerts, userCerts...)
		}
	}
	return allCerts, nil
}

// Delete removes a cert set and all its files.
func (m *Manager) Delete(userID int64, setID string) error {
	dir := m.store.CertSetDir(userID, setID)
	if err := m.store.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove certset dir: %w", err)
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
