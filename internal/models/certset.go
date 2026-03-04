package models

import "time"

// CertSetStatus represents the validation state of a certificate set.
type CertSetStatus string

const (
	CertSetStatusValid          CertSetStatus = "VALID"
	CertSetStatusMissingFiles   CertSetStatus = "MISSING_FILES"
	CertSetStatusInvalidP12     CertSetStatus = "INVALID_P12"
	CertSetStatusInvalidProv    CertSetStatus = "INVALID_PROVISION"
	CertSetStatusDecryptFail    CertSetStatus = "DECRYPT_FAIL"
	CertSetStatusUnknown        CertSetStatus = "UNKNOWN"
)

// CertSet represents a user's certificate set (P12 + provision profile).
type CertSet struct {
	SetID              string        `json:"set_id"`
	UserID             int64         `json:"user_id"`
	Name               string        `json:"name"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
	StatusValid        CertSetStatus `json:"status_valid"`
	LastCheckedAt      *time.Time    `json:"last_checked_at,omitempty"`
	LastUsedAt         *time.Time    `json:"last_used_at,omitempty"`
	P12FingerprintShort string       `json:"p12_fingerprint_short"`
	ProvisionSummary   string        `json:"provision_summary"`
}
