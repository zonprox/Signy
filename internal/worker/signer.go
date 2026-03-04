package worker

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/zonprox/Signy/internal/certset"
	"github.com/zonprox/Signy/internal/config"
	"github.com/zonprox/Signy/internal/crypto"
	"github.com/zonprox/Signy/internal/storage"
)

// Signer executes zsign to sign IPA files.
type Signer struct {
	cfg     *config.Config
	store   *storage.Manager
	certMgr *certset.Manager
	logger  *slog.Logger
	mock    bool
}

// NewSigner creates a new signer.
func NewSigner(cfg *config.Config, store *storage.Manager, certMgr *certset.Manager, logger *slog.Logger, mock bool) *Signer {
	return &Signer{
		cfg:     cfg,
		store:   store,
		certMgr: certMgr,
		logger:  logger,
		mock:    mock,
	}
}

// SignResult contains the result of a signing operation.
type SignResult struct {
	SignedIPAPath  string
	ManifestPath  string
	SignLogPath   string
	InstallURL    string
}

// Sign performs the IPA signing using zsign.
func (s *Signer) Sign(ctx context.Context, jobID string, userID int64, certSetID, ipaPath, artifactBase string, ephemeralPassword string) (*SignResult, error) {
	signedPath := filepath.Join(artifactBase, "signed.ipa")
	logPath := filepath.Join(artifactBase, "sign.log")

	// Determine P12 path and password
	p12Path, password, tempP12, err := s.resolveP12(userID, certSetID, ephemeralPassword)
	if err != nil {
		return nil, fmt.Errorf("resolve p12: %w", err)
	}
	if tempP12 != "" {
		defer func() {
			os.Remove(tempP12)
			s.logger.Info("cleaned up temp p12", "job_id", jobID)
		}()
	}

	provPath := s.certMgr.GetProvisionPath(userID, certSetID)

	if s.mock {
		return s.mockSign(ipaPath, signedPath, logPath, artifactBase, jobID)
	}

	// Build zsign command
	timeout := time.Duration(s.cfg.JobTimeoutSigningSeconds) * time.Second
	signCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"-k", p12Path,
		"-p", password,
		"-m", provPath,
		"-o", signedPath,
		ipaPath,
	}

	cmd := exec.CommandContext(signCtx, "zsign", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	s.logger.Info("starting zsign", "job_id", jobID, "ipa", ipaPath)

	err = cmd.Run()

	// Write sign log (cap at 2MB)
	logContent := fmt.Sprintf("=== STDOUT ===\n%s\n=== STDERR ===\n%s\n",
		truncate(stdout.String(), 1024*1024),
		truncate(stderr.String(), 1024*1024))
	_ = os.WriteFile(logPath, []byte(logContent), 0644) //nolint:errcheck // best-effort log write

	if err != nil {
		return nil, fmt.Errorf("zsign failed: %w (stderr: %s)", err, truncate(stderr.String(), 500))
	}

	// Verify signed IPA exists
	if !s.store.FileExists(signedPath) {
		return nil, fmt.Errorf("zsign completed but signed IPA not found at %s", signedPath)
	}

	// Generate manifest
	manifestPath := filepath.Join(artifactBase, "manifest.plist")
	manifest := GenerateManifest(s.cfg.BaseURL, jobID)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0644); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	installURL := fmt.Sprintf("itms-services://?action=download-manifest&url=%s/artifacts/%s/manifest.plist",
		s.cfg.BaseURL, jobID)

	// Update cert set last used
	s.certMgr.UpdateLastUsed(userID, certSetID)

	return &SignResult{
		SignedIPAPath: signedPath,
		ManifestPath:  manifestPath,
		SignLogPath:   logPath,
		InstallURL:    installURL,
	}, nil
}

// resolveP12 resolves the P12 file path and password.
// If MASTER_KEY is set, decrypts p12.enc to a temp file.
// Returns: (p12FilePath, password, tempFilePath, error)
func (s *Signer) resolveP12(userID int64, certSetID string, ephemeralPassword string) (string, string, string, error) {
	if s.cfg.HasMasterKey() {
		// Decrypt P12 to temp file
		encPath := s.certMgr.GetP12Path(userID, certSetID)
		encData, err := s.store.ReadFile(encPath)
		if err != nil {
			return "", "", "", fmt.Errorf("read encrypted p12: %w", err)
		}

		p12Data, err := crypto.DecryptFile(s.cfg.MasterKey, fmt.Sprintf("p12-%d-%s", userID, certSetID), encData)
		if err != nil {
			return "", "", "", fmt.Errorf("decrypt p12: %w", err)
		}

		// Write to temp file with 0600 permissions
		tmpFile, err := os.CreateTemp("", "signy-p12-*.p12")
		if err != nil {
			return "", "", "", fmt.Errorf("create temp p12: %w", err)
		}
		_ = tmpFile.Chmod(0600)
		_, _ = tmpFile.Write(p12Data)
		tmpFile.Close()

		// Get password
		password, err := s.certMgr.GetP12Password(userID, certSetID)
		if err != nil {
			os.Remove(tmpFile.Name())
			return "", "", "", fmt.Errorf("get p12 password: %w", err)
		}

		return tmpFile.Name(), password, tmpFile.Name(), nil
	}

	// No MASTER_KEY: use plain P12 file and ephemeral password
	p12Path := s.certMgr.GetP12Path(userID, certSetID)
	if ephemeralPassword == "" {
		return "", "", "", fmt.Errorf("no MASTER_KEY and no ephemeral password provided")
	}
	return p12Path, ephemeralPassword, "", nil
}

func (s *Signer) mockSign(ipaPath, signedPath, logPath, artifactBase, jobID string) (*SignResult, error) {
	s.logger.Info("MOCK: simulating signing", "job_id", jobID, "ipa", ipaPath)

	// Copy input IPA to signed path
	input, err := os.ReadFile(ipaPath)
	if err != nil {
		return nil, fmt.Errorf("mock read ipa: %w", err)
	}
	if err := os.WriteFile(signedPath, input, 0644); err != nil {
		return nil, fmt.Errorf("mock write signed ipa: %w", err)
	}

	// Write fake sign log
	logContent := fmt.Sprintf("=== MOCK SIGNING ===\nJob: %s\nInput: %s\nOutput: %s\nTimestamp: %s\nStatus: SUCCESS (mock)\n",
		jobID, ipaPath, signedPath, time.Now().UTC().Format(time.RFC3339))
	_ = os.WriteFile(logPath, []byte(logContent), 0644)

	// Generate manifest
	manifestPath := filepath.Join(artifactBase, "manifest.plist")
	manifest := GenerateManifest(s.cfg.BaseURL, jobID)
	_ = os.WriteFile(manifestPath, []byte(manifest), 0644)

	installURL := fmt.Sprintf("itms-services://?action=download-manifest&url=%s/artifacts/%s/manifest.plist",
		s.cfg.BaseURL, jobID)

	time.Sleep(2 * time.Second) // Simulate signing time

	return &SignResult{
		SignedIPAPath: signedPath,
		ManifestPath:  manifestPath,
		SignLogPath:   logPath,
		InstallURL:    installURL,
	}, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}
