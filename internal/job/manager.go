package job

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/zonprox/Signy/internal/models"
	"github.com/zonprox/Signy/internal/queue"
	"github.com/zonprox/Signy/internal/storage"
)

const (
	jobKeyPrefix     = "signy:job:"
	userJobsPrefix   = "signy:user_jobs:"
	userLockPrefix   = "signy:user_lock:"
	dedupeKeyPrefix  = "signy:dedupe:"
)

// Manager handles job lifecycle and state management.
type Manager struct {
	rdb      *redis.Client
	store    *storage.Manager
	producer *queue.Producer
	logger   *slog.Logger
}

// NewManager creates a job manager.
func NewManager(rdb *redis.Client, store *storage.Manager, producer *queue.Producer, logger *slog.Logger) *Manager {
	return &Manager{
		rdb:      rdb,
		store:    store,
		producer: producer,
		logger:   logger,
	}
}

// CreateAndEnqueue creates a new job, stores it, and enqueues it.
func (m *Manager) CreateAndEnqueue(ctx context.Context, userID int64, certSetID, ipaPath string, options models.SigningOptions) (*models.Job, error) {
	jobID := uuid.New().String()
	artifactBase := m.store.ArtifactDir(jobID)

	if err := m.store.EnsureDir(artifactBase); err != nil {
		return nil, fmt.Errorf("create artifact dir: %w", err)
	}

	now := time.Now().UTC()
	job := &models.Job{
		JobID:            jobID,
		UserID:           userID,
		CertSetID:        certSetID,
		IPAPath:          ipaPath,
		ArtifactBasePath: artifactBase,
		Options:          options,
		CreatedAt:        now,
		UpdatedAt:        now,
		Status:           models.JobStatusQueued,
		RetryCount:       0,
	}

	// Save to Redis hash
	if err := m.saveJob(ctx, job); err != nil {
		return nil, fmt.Errorf("save job: %w", err)
	}

	// Add to user's job list
	m.rdb.LPush(ctx, userJobsPrefix+fmt.Sprintf("%d", userID), jobID)
	m.rdb.LTrim(ctx, userJobsPrefix+fmt.Sprintf("%d", userID), 0, 49) // keep last 50

	// Write initial event
	m.AppendEvent(job.ArtifactBasePath, models.JobEvent{
		Timestamp: now,
		Status:    models.JobStatusQueued,
		Message:   "Job created and queued for signing",
	})

	// Enqueue to stream (we just need to pass the JobID, the worker can fetch the full job from Redis)
	payload := queue.EncodeJobPayload(jobID, userID, certSetID, ipaPath)
	if _, err := m.producer.Enqueue(ctx, payload); err != nil {
		return nil, fmt.Errorf("enqueue: %w", err)
	}

	return job, nil
}

// GetJob retrieves a job from Redis.
func (m *Manager) GetJob(ctx context.Context, jobID string) (*models.Job, error) {
	key := jobKeyPrefix + jobID
	data, err := m.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("hgetall: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("job %s not found", jobID)
	}
	return jobFromMap(data)
}

// UpdateStatus updates the job status in Redis and appends an event.
func (m *Manager) UpdateStatus(ctx context.Context, job *models.Job, status models.JobStatus, message string) error {
	job.Status = status
	job.UpdatedAt = time.Now().UTC()
	if err := m.saveJob(ctx, job); err != nil {
		return err
	}

	m.AppendEvent(job.ArtifactBasePath, models.JobEvent{
		Timestamp: job.UpdatedAt,
		Status:    status,
		Message:   message,
	})

	return nil
}

// SetFailed marks a job as failed.
func (m *Manager) SetFailed(ctx context.Context, job *models.Job, errorCode, userError string) error {
	job.Status = models.JobStatusFailed
	job.ErrorCode = errorCode
	job.UserFriendlyError = userError
	job.UpdatedAt = time.Now().UTC()
	if err := m.saveJob(ctx, job); err != nil {
		return err
	}

	m.AppendEvent(job.ArtifactBasePath, models.JobEvent{
		Timestamp: job.UpdatedAt,
		Status:    models.JobStatusFailed,
		Message:   userError,
		Details:   errorCode,
	})

	return nil
}

// IncrementRetry increments the retry count.
func (m *Manager) IncrementRetry(ctx context.Context, job *models.Job) error {
	job.RetryCount++
	job.UpdatedAt = time.Now().UTC()
	return m.saveJob(ctx, job)
}

// ListUserJobs returns the last N jobs for a user.
func (m *Manager) ListUserJobs(ctx context.Context, userID int64, limit int64) ([]*models.Job, error) {
	key := userJobsPrefix + fmt.Sprintf("%d", userID)
	ids, err := m.rdb.LRange(ctx, key, 0, limit-1).Result()
	if err != nil {
		return nil, fmt.Errorf("lrange: %w", err)
	}

	var jobs []*models.Job
	for _, id := range ids {
		job, err := m.GetJob(ctx, id)
		if err != nil {
			m.logger.Warn("failed to get job", "job_id", id, "error", err)
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// TryAcquireUserLock attempts to acquire a per-user concurrency lock.
func (m *Manager) TryAcquireUserLock(ctx context.Context, userID int64, ttl time.Duration) (bool, error) {
	key := userLockPrefix + fmt.Sprintf("%d", userID)
	err := m.rdb.SetArgs(ctx, key, "1", redis.SetArgs{TTL: ttl, Mode: "NX"}).Err()
	if err == redis.Nil {
		return false, nil // key already exists, lock not acquired
	}
	return err == nil, err
}

// ReleaseUserLock releases the per-user concurrency lock.
func (m *Manager) ReleaseUserLock(ctx context.Context, userID int64) error {
	key := userLockPrefix + fmt.Sprintf("%d", userID)
	return m.rdb.Del(ctx, key).Err()
}

// CheckDedupe checks if a job with the same dedupe key already exists.
// Returns true if the job should be skipped (duplicate).
func (m *Manager) CheckDedupe(ctx context.Context, dedupeKey string, ttl time.Duration) (bool, error) {
	key := dedupeKeyPrefix + dedupeKey
	err := m.rdb.SetArgs(ctx, key, "1", redis.SetArgs{TTL: ttl, Mode: "NX"}).Err()
	if err == redis.Nil {
		return true, nil // key already existed → duplicate
	}
	if err != nil {
		return false, err
	}
	return false, nil // key was newly set → not a duplicate
}

// AppendEvent appends an event to the events.jsonl file.
func (m *Manager) AppendEvent(artifactBase string, event models.JobEvent) {
	eventsPath := filepath.Join(artifactBase, "events.jsonl")
	data, err := json.Marshal(event)
	if err != nil {
		m.logger.Warn("failed to marshal event", "error", err)
		return
	}

	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		m.logger.Warn("failed to open events file", "path", eventsPath, "error", err)
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(data)
	_, _ = f.Write([]byte("\n"))
}

// WriteJobMeta writes job metadata to the artifact directory.
func (m *Manager) WriteJobMeta(job *models.Job) error {
	metaPath := filepath.Join(job.ArtifactBasePath, "meta.json")
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	return m.store.WriteFile(metaPath, data, 0644)
}

func (m *Manager) saveJob(ctx context.Context, job *models.Job) error {
	optionsJSON, _ := json.Marshal(job.Options)

	key := jobKeyPrefix + job.JobID
	fields := map[string]interface{}{
		"job_id":             job.JobID,
		"user_id":            fmt.Sprintf("%d", job.UserID),
		"certset_id":         job.CertSetID,
		"ipa_path":           job.IPAPath,
		"artifact_base_path": job.ArtifactBasePath,
		"options":            string(optionsJSON),
		"created_at":         job.CreatedAt.Format(time.RFC3339),
		"updated_at":         job.UpdatedAt.Format(time.RFC3339),
		"status":             string(job.Status),
		"error_code":         job.ErrorCode,
		"user_friendly_error": job.UserFriendlyError,
		"retry_count":        fmt.Sprintf("%d", job.RetryCount),
	}
	if err := m.rdb.HSet(ctx, key, fields).Err(); err != nil {
		return fmt.Errorf("hset: %w", err)
	}
	// Set TTL for job data (30 days)
	m.rdb.Expire(ctx, key, 30*24*time.Hour)
	return nil
}

// DeleteJob removes a job from Redis and its artifacts from disk.
func (m *Manager) DeleteJob(ctx context.Context, jobID string) error {
	job, err := m.GetJob(ctx, jobID)
	if err != nil {
		return err
	}

	// Remove from redis
	key := jobKeyPrefix + jobID
	m.rdb.Del(ctx, key)

	msgKey := "signy:job_msg:" + jobID
	m.rdb.Del(ctx, msgKey)

	// Remove from user list
	userKey := userJobsPrefix + fmt.Sprintf("%d", job.UserID)
	m.rdb.LRem(ctx, userKey, 0, jobID)

	// Remove artifacts from disk
	if job.ArtifactBasePath != "" {
		m.logger.Info("deleting job artifacts", "job_id", jobID, "path", job.ArtifactBasePath)
		_ = os.RemoveAll(job.ArtifactBasePath)
	}

	return nil
}

func jobFromMap(data map[string]string) (*models.Job, error) {
	userID, _ := strconv.ParseInt(data["user_id"], 10, 64)
	retryCount, _ := strconv.Atoi(data["retry_count"])
	createdAt, _ := time.Parse(time.RFC3339, data["created_at"])
	updatedAt, _ := time.Parse(time.RFC3339, data["updated_at"])

	var options models.SigningOptions
	if optStr := data["options"]; optStr != "" {
		_ = json.Unmarshal([]byte(optStr), &options)
	}

	return &models.Job{
		JobID:            data["job_id"],
		UserID:           userID,
		CertSetID:        data["certset_id"],
		IPAPath:          data["ipa_path"],
		ArtifactBasePath: data["artifact_base_path"],
		Options:          options,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
		Status:           models.JobStatus(data["status"]),
		ErrorCode:        data["error_code"],
		UserFriendlyError: data["user_friendly_error"],
		RetryCount:       retryCount,
	}, nil
}
