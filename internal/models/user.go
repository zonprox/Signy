package models

// UserState represents the FSM state for a user's current interaction flow.
type UserState string

const (
	StateIdle                UserState = ""
	StateCertCreateName      UserState = "cert_create_name"
	StateCertCreateP12       UserState = "cert_create_p12"
	StateCertCreatePassword  UserState = "cert_create_password"
	StateCertCreateProvision UserState = "cert_create_provision"
	StateJobSelectCert       UserState = "job_select_cert"
	StateJobUploadIPA        UserState = "job_upload_ipa"
	StateJobConfirm          UserState = "job_confirm"
	StateJobPasswordPrompt   UserState = "job_password_prompt"
)

// UserSession holds ephemeral FSM data during a user's interaction flow.
type UserSession struct {
	State       UserState `json:"state"`
	UserID      int64     `json:"user_id"`

	// CertSet creation flow
	CertSetName string `json:"certset_name,omitempty"`
	CertSetID   string `json:"certset_id,omitempty"`
	P12Path     string `json:"p12_path,omitempty"`

	// Job creation flow
	SelectedCertSetID string `json:"selected_certset_id,omitempty"`
	IPAPath           string `json:"ipa_path,omitempty"`
	IPASize           int64  `json:"ipa_size,omitempty"`
	BundleID          string `json:"bundle_id,omitempty"`
	PendingJobID      string `json:"pending_job_id,omitempty"`
}
