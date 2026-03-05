package worker

import (
	"log/slog"
	"os"
	"time"

	"github.com/zonprox/Signy/internal/metrics"
	"github.com/zonprox/Signy/internal/storage"
)

// Cleaner handles periodic cleanup of old artifacts and incoming files.
type Cleaner struct {
	store         *storage.Manager
	retentionDays int
	logger        *slog.Logger
}

// NewCleaner creates a new cleanup handler.
func NewCleaner(store *storage.Manager, retentionDays int, logger *slog.Logger) *Cleaner {
	return &Cleaner{
		store:         store,
		retentionDays: retentionDays,
		logger:        logger,
	}
}

// RunCleanupLoop runs the cleanup loop every interval.
func (c *Cleaner) RunCleanupLoop(stopCh <-chan struct{}, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	c.logger.Info("cleanup loop started", "interval", interval, "retention_days", c.retentionDays)

	// Run immediately on start
	c.cleanup()

	for {
		select {
		case <-stopCh:
			c.logger.Info("cleanup loop stopped")
			return
		case <-ticker.C:
			c.cleanup()
		}
	}
}

func (c *Cleaner) cleanup() {
	maxAge := time.Duration(c.retentionDays) * 24 * time.Hour
	c.logger.Info("starting cleanup cycle", "max_age", maxAge)

	// Clean artifacts
	artifactsDir := c.store.BasePath() + "/artifacts"
	removed, err := c.store.CleanOldFiles(artifactsDir, maxAge)
	if err != nil {
		c.logger.Warn("artifact cleanup error", "error", err)
	} else if removed > 0 {
		c.logger.Info("cleaned artifacts", "removed", removed)
		metrics.CleanupFilesRemoved.Add(float64(removed))
	}

	// Clean incoming files from all users
	usersDir := c.store.BasePath() + "/users"
	c.cleanUserIncoming(usersDir, maxAge)
}

func (c *Cleaner) cleanUserIncoming(usersDir string, maxAge time.Duration) {
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		incomingDir := usersDir + "/" + entry.Name() + "/incoming"
		removed, err := c.store.CleanOldFiles(incomingDir, maxAge)
		if err != nil {
			c.logger.Warn("incoming cleanup error", "user_dir", entry.Name(), "error", err)
			continue
		}
		if removed > 0 {
			c.logger.Info("cleaned incoming files", "user", entry.Name(), "removed", removed)
			metrics.CleanupFilesRemoved.Add(float64(removed))
		}
	}
}
