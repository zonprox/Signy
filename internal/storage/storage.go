package storage

import (
	"fmt"
	"io"
	"log/slog"
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

// CopyLocalFile copies a local file.
func (m *Manager) CopyLocalFile(srcPath, destPath string, maxBytes int64) (int64, error) {
	dir := filepath.Dir(destPath)
	if err := m.EnsureDir(dir); err != nil {
		return 0, fmt.Errorf("ensure dir: %w", err)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return 0, fmt.Errorf("open src: %w", err)
	}
	defer func() { _ = src.Close() }()

	f, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("create dest: %w", err)
	}
	defer func() { _ = f.Close() }()

	reader := io.LimitReader(src, maxBytes+1)

	n, err := io.Copy(f, reader)
	if err != nil {
		_ = os.Remove(destPath)
		return 0, fmt.Errorf("copy: %w", err)
	}
	if n > maxBytes {
		_ = os.Remove(destPath)
		return 0, fmt.Errorf("file exceeds max size of %d bytes", maxBytes)
	}

	return n, nil
}

// IncomingIPAPath returns the path for an incoming IPA file.
func (m *Manager) IncomingIPAPath(userID int64, telegramFileID string) string {
	ts := time.Now().Unix()
	return filepath.Join(m.IncomingDir(userID), fmt.Sprintf("%d_%s.ipa", ts, telegramFileID))
}

// IncomingDylibPath returns the path for an incoming dylib file.
func (m *Manager) IncomingDylibPath(userID int64, telegramFileID string) string {
	ts := time.Now().Unix()
	return filepath.Join(m.IncomingDir(userID), fmt.Sprintf("%d_%s.dylib", ts, telegramFileID))
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
