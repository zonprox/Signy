package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zonprox/Signy/internal/certset"
	"github.com/zonprox/Signy/internal/config"
	"github.com/zonprox/Signy/internal/crypto"
	"github.com/zonprox/Signy/internal/job"
	"github.com/zonprox/Signy/internal/metrics"
	"github.com/zonprox/Signy/internal/models"
	"github.com/zonprox/Signy/internal/queue"
	"github.com/zonprox/Signy/internal/storage"
)

// Worker is the signing worker service.
type Worker struct {
	cfg        *config.Config
	rdb        *redis.Client
	consumer   *queue.Consumer
	jobMgr     *job.Manager
	signer     *Signer
	cleaner    *Cleaner
	store      *storage.Manager
	logger     *slog.Logger
	processKey []byte
}

// New creates a new Worker instance.
func New(cfg *config.Config, logger *slog.Logger) (*Worker, error) {
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	rdb := redis.NewClient(opts)

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	hostname, _ := os.Hostname()
	consumerName := fmt.Sprintf("worker-%s-%d", hostname, os.Getpid())

	visTimeout := time.Duration(cfg.VisibilityTimeoutSeconds) * time.Second
	store := storage.NewManager(cfg.StoragePath, logger)
	certMgr := certset.NewManager(store, logger, cfg.MasterKey, cfg.MaxCertSetsPerUser)
	producer := queue.NewProducer(rdb, logger)
	consumer := queue.NewConsumer(rdb, logger, consumerName, visTimeout)
	jobMgr := job.NewManager(rdb, store, producer, logger)

	mockMode := os.Getenv("ZSIGN_MOCK") == "true"
	signer := NewSigner(cfg, store, certMgr, logger, mockMode)
	cleaner := NewCleaner(store, cfg.RetentionDaysDefault, logger)

	// Resolve process key for ephemeral password decryption.
	// Must match the key used by the Bot to encrypt session passwords.
	// When MASTER_KEY is set, both Bot and Worker derive the same key via HKDF.
	var processKey []byte
	if cfg.HasMasterKey() {
		processKey, err = crypto.DeriveSharedEphemeralKey(cfg.MasterKey)
		if err != nil {
			return nil, fmt.Errorf("derive shared ephemeral key: %w", err)
		}
	} else {
		// Without MASTER_KEY, use a random key. Note: this only works when
		// bot and worker run in the same OS process (single binary).
		processKey, err = crypto.GenerateRandomKey()
		if err != nil {
			return nil, fmt.Errorf("generate random key: %w", err)
		}
	}

	return &Worker{
		cfg:        cfg,
		rdb:        rdb,
		consumer:   consumer,
		jobMgr:     jobMgr,
		signer:     signer,
		cleaner:    cleaner,
		store:      store,
		logger:     logger,
		processKey: processKey,
	}, nil
}

// Run starts the worker and blocks until context is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	// Ensure consumer group exists
	if err := w.consumer.EnsureGroup(ctx); err != nil {
		return fmt.Errorf("ensure consumer group: %w", err)
	}

	// Start cleanup loop (every 6 hours)
	stopCleanup := make(chan struct{})
	go w.cleaner.RunCleanupLoop(stopCleanup, 6*time.Hour)

	// Start stale message recovery loop
	go w.recoverStaleLoop(ctx)

	// Start worker pool
	w.logger.Info("starting worker pool", "concurrency", w.cfg.WorkerConcurrency)
	sem := make(chan struct{}, w.cfg.WorkerConcurrency)
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("shutting down worker, waiting for in-flight jobs")
			wg.Wait()
			close(stopCleanup)
			_ = w.rdb.Close()
			w.logger.Info("worker shutdown complete")
			return nil
		default:
		}

		// Read new messages
		msgs, err := w.consumer.ReadNew(ctx, 1)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			w.logger.Error("read new messages", "error", err)
			time.Sleep(time.Second)
			continue
		}

		for _, msg := range msgs {
			sem <- struct{}{} // acquire semaphore
			wg.Add(1)
			go func(m queue.Message) {
				defer wg.Done()
				defer func() { <-sem }() // release semaphore
				w.processMessage(ctx, m)
			}(msg)
		}
	}
}

func (w *Worker) processMessage(ctx context.Context, msg queue.Message) {
	jobID, userIDStr, certSetID, ipaPath, err := queue.DecodeJobPayload(msg.Payload)
	if err != nil {
		w.logger.Error("decode job payload", "error", err, "msg_id", msg.ID)
		_ = w.consumer.Ack(ctx, msg.ID) // bad message, don't retry
		return
	}

	userID, _ := strconv.ParseInt(userIDStr, 10, 64)
	logger := w.logger.With("job_id", jobID, "user_id", userID, "msg_id", msg.ID)
	logger.Info("processing job")

	// Get job from Redis
	j, err := w.jobMgr.GetJob(ctx, jobID)
	if err != nil {
		logger.Error("get job", "error", err)
		_ = w.consumer.Ack(ctx, msg.ID)
		return
	}

	// Always ensure Dylibs are cleaned up from incoming directory whether signing succeeds or fails
	defer func() {
		for _, dylib := range j.Options.DylibPaths {
			_ = w.store.RemoveAll(dylib)
		}
	}()

	// Check if already completed (idempotency)
	if j.Status == models.JobStatusDone || j.Status == models.JobStatusFailed {
		logger.Info("job already terminal, skipping", "status", j.Status)
		_ = w.consumer.Ack(ctx, msg.ID)
		return
	}

	// Check retry count
	if j.RetryCount >= 3 {
		logger.Warn("max retries exceeded, failing job")
		_ = w.jobMgr.SetFailed(ctx, j, "MAX_RETRIES", "Job failed after maximum retries. Please try again.")
		_ = w.consumer.Ack(ctx, msg.ID)
		_ = queue.PublishJobEvent(ctx, w.rdb, jobID, "FAILED", "Maximum retries exceeded")
		metrics.JobsTotal.WithLabelValues("failed").Inc()
		return
	}

	// Try to acquire per-user lock
	lockTTL := time.Duration(w.cfg.JobTimeoutSigningSeconds+60) * time.Second
	acquired, _ := w.jobMgr.TryAcquireUserLock(ctx, userID, lockTTL)
	if !acquired {
		logger.Info("user lock not acquired, will retry later")
		// Don't ACK — message will become stale and be reclaimed
		return
	}
	defer func() { _ = w.jobMgr.ReleaseUserLock(ctx, userID) }()

	// Update status to SIGNING
	_ = w.jobMgr.UpdateStatus(ctx, j, models.JobStatusSigning, "Signing in progress")
	_ = queue.PublishJobEvent(ctx, w.rdb, jobID, "SIGNING", "Signing started")
	metrics.JobsInFlight.Inc()

	start := time.Now()

	// Resolve ephemeral password if needed
	var ephemeralPassword string
	if !w.cfg.HasMasterKey() {
		passKey := fmt.Sprintf("signy:job_pass_token:%s", jobID)
		encPass, err := w.rdb.Get(ctx, passKey).Result()
		if err != nil {
			logger.Error("get ephemeral password", "error", err)
			_ = w.jobMgr.SetFailed(ctx, j, "NO_PASSWORD", "P12 password not available. Please start a new job.")
			_ = w.consumer.Ack(ctx, msg.ID)
			_ = queue.PublishJobEvent(ctx, w.rdb, jobID, "FAILED", "Password not available")
			metrics.JobsInFlight.Dec()
			metrics.JobsTotal.WithLabelValues("failed").Inc()
			return
		}
		w.rdb.Del(ctx, passKey) // one-time use

		passBytes, err := crypto.DecryptEphemeral(w.processKey, encPass)
		if err != nil {
			// Try with a service-level shared key approach
			// In multi-instance deployments, this needs a shared key
			logger.Warn("decrypt ephemeral password failed, trying raw", "error", err)
			ephemeralPassword = encPass // fallback: treat as already plaintext token
		} else {
			ephemeralPassword = string(passBytes)
		}
	}

	// Execute signing
	_, err = w.signer.Sign(ctx, jobID, userID, certSetID, ipaPath, j.ArtifactBasePath, j.Options, ephemeralPassword)
	duration := time.Since(start)
	metrics.JobsInFlight.Dec()

	if err != nil {
		logger.Error("signing failed", "error", err, "duration", duration)
		_ = w.jobMgr.IncrementRetry(ctx, j)
		_ = w.jobMgr.SetFailed(ctx, j, "SIGN_ERROR", fmt.Sprintf("Signing failed: %s", truncateError(err.Error())))
		_ = w.consumer.Ack(ctx, msg.ID) // ACK to prevent infinite retries of same stream msg
		_ = queue.PublishJobEvent(ctx, w.rdb, jobID, "FAILED", "Signing failed")
		metrics.JobsTotal.WithLabelValues("failed").Inc()
		metrics.JobDurationSeconds.WithLabelValues("failed").Observe(duration.Seconds())
		return
	}

	// Update to PUBLISHING
	_ = w.jobMgr.UpdateStatus(ctx, j, models.JobStatusPublishing, "Publishing artifacts")
	_ = queue.PublishJobEvent(ctx, w.rdb, jobID, "PUBLISHING", "Generating install links")

	// Write job meta
	_ = w.jobMgr.WriteJobMeta(j)

	// Update to DONE
	_ = w.jobMgr.UpdateStatus(ctx, j, models.JobStatusDone, "Signing complete. Download page is ready.")
	_ = queue.PublishJobEvent(ctx, w.rdb, jobID, "DONE", "Signing complete! Download page is ready.")

	// ACK the message
	_ = w.consumer.Ack(ctx, msg.ID)

	metrics.JobsTotal.WithLabelValues("done").Inc()
	metrics.JobDurationSeconds.WithLabelValues("done").Observe(duration.Seconds())
	logger.Info("job completed successfully", "duration", duration)
}

func (w *Worker) recoverStaleLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			claimed, err := w.consumer.ClaimStale(ctx, 5)
			if err != nil {
				w.logger.Warn("claim stale error", "error", err)
				continue
			}
			if len(claimed) > 0 {
				w.logger.Info("claimed stale messages", "count", len(claimed))
				for _, msg := range claimed {
					go w.processMessage(ctx, msg)
				}
			}
		}
	}
}

func truncateError(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
