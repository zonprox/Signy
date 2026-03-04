package storage

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Manager handles file storage operations.
type Manager struct {
	basePath string
	logger   *slog.Logger
}

// NewManager creates a storage manager.
func NewManager(basePath string, logger *slog.Logger) *Manager {
	return &Manager{basePath: basePath, logger: logger}
}

// BasePath returns the base storage path.
func (m *Manager) BasePath() string {
	return m.basePath
}

// UserDir returns the storage directory for a user.
func (m *Manager) UserDir(userID int64) string {
	return filepath.Join(m.basePath, "users", fmt.Sprintf("%d", userID))
}

// CertSetDir returns the directory for a cert set.
func (m *Manager) CertSetDir(userID int64, setID string) string {
	return filepath.Join(m.UserDir(userID), "certsets", setID)
}

// IncomingDir returns the directory for incoming files.
func (m *Manager) IncomingDir(userID int64) string {
	return filepath.Join(m.UserDir(userID), "incoming")
}

// ArtifactDir returns the directory for job artifacts.
func (m *Manager) ArtifactDir(jobID string) string {
	return filepath.Join(m.basePath, "artifacts", jobID)
}

// EnsureDir creates a directory and all parents with 0755 permissions.
func (m *Manager) EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

// WriteFile writes data to a file with the given permissions.
func (m *Manager) WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := m.EnsureDir(dir); err != nil {
		return fmt.Errorf("ensure dir: %w", err)
	}
	return os.WriteFile(path, data, perm)
}

// StreamDownload downloads a URL to a file path using streaming (no full RAM load).
func (m *Manager) StreamDownload(url, destPath string, maxBytes int64) (int64, error) {
	dir := filepath.Dir(destPath)
	if err := m.EnsureDir(dir); err != nil {
		return 0, fmt.Errorf("ensure dir: %w", err)
	}

	resp, err := http.Get(url)
	if err != nil {
		return 0, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	reader := io.LimitReader(resp.Body, maxBytes+1) // +1 to detect overflow
	n, err := io.Copy(f, reader)
	if err != nil {
		os.Remove(destPath)
		return 0, fmt.Errorf("write: %w", err)
	}
	if n > maxBytes {
		os.Remove(destPath)
		return 0, fmt.Errorf("file exceeds max size of %d bytes", maxBytes)
	}

	return n, nil
}

// IncomingIPAPath returns the path for an incoming IPA file.
func (m *Manager) IncomingIPAPath(userID int64, telegramFileID string) string {
	ts := time.Now().Unix()
	return filepath.Join(m.IncomingDir(userID), fmt.Sprintf("%d_%s.ipa", ts, telegramFileID))
}

// ReadDefaultSetID reads the default cert set ID for a user.
func (m *Manager) ReadDefaultSetID(userID int64) (string, error) {
	path := filepath.Join(m.UserDir(userID), "default_set_id.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// WriteDefaultSetID writes the default cert set ID for a user.
func (m *Manager) WriteDefaultSetID(userID int64, setID string) error {
	dir := m.UserDir(userID)
	if err := m.EnsureDir(dir); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "default_set_id.txt"), []byte(setID), 0644)
}

// RemoveDefaultSetID removes the default cert set for a user.
func (m *Manager) RemoveDefaultSetID(userID int64) error {
	path := filepath.Join(m.UserDir(userID), "default_set_id.txt")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// RemoveAll removes a path and all children.
func (m *Manager) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

// FileExists checks if a file exists.
func (m *Manager) FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ReadFile reads a file.
func (m *Manager) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// CleanOldFiles removes files older than maxAge from a directory.
func (m *Manager) CleanOldFiles(dir string, maxAge time.Duration) (int, error) {
	removed := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			fullPath := filepath.Join(dir, entry.Name())
			if err := os.RemoveAll(fullPath); err != nil {
				m.logger.Warn("failed to remove old file", "path", fullPath, "error", err)
				continue
			}
			removed++
		}
	}
	return removed, nil
}
