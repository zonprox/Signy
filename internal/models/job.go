package models

import "time"

// JobStatus represents the current state of a signing job.
type JobStatus string

const (
	JobStatusQueued     JobStatus = "QUEUED"
	JobStatusSigning    JobStatus = "SIGNING"
	JobStatusPublishing JobStatus = "PUBLISHING"
	JobStatusDone       JobStatus = "DONE"
	JobStatusFailed     JobStatus = "FAILED"
)

// SigningOptions configures optional zsign parameters.
type SigningOptions struct {
	BundleName    string   `json:"bundle_name,omitempty"`
	BundleID      string   `json:"bundle_id,omitempty"`
	BundleVersion string   `json:"bundle_version,omitempty"`
	DylibPaths    []string `json:"dylib_paths,omitempty"`
}

// Job represents a signing job.
type Job struct {
	JobID            string         `json:"job_id"`
	UserID           int64          `json:"user_id"`
	CertSetID        string         `json:"certset_id"`
	IPAPath          string         `json:"ipa_path"`
	ArtifactBasePath string         `json:"artifact_base_path"`
	Options          SigningOptions `json:"options"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	Status           JobStatus `json:"status"`
	ErrorCode        string    `json:"error_code,omitempty"`
	UserFriendlyError string  `json:"user_friendly_error,omitempty"`
	RetryCount       int       `json:"retry_count"`
}

// JobEvent represents an immutable event in a job's timeline.
type JobEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Status    JobStatus `json:"status"`
	Message   string    `json:"message"`
	Details   string    `json:"details,omitempty"`
}
