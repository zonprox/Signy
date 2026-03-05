package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zonprox/Signy/internal/certset"
	"github.com/zonprox/Signy/internal/config"
	"github.com/zonprox/Signy/internal/job"
	"github.com/zonprox/Signy/internal/models"
)

//go:embed templates/*.html static/*
var webFS embed.FS

// Server provides the web UI and API endpoints.
type Server struct {
	cfg     *config.Config
	jobMgr  *job.Manager
	certMgr *certset.Manager
	rdb     *redis.Client
	logger  *slog.Logger
	mux     *http.ServeMux
	tmpl    *template.Template
}

// NewServer creates a new web server.
func NewServer(cfg *config.Config, jobMgr *job.Manager, certMgr *certset.Manager, rdb *redis.Client, logger *slog.Logger) (*Server, error) {
	funcMap := template.FuncMap{
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.Format("2006-01-02 15:04 UTC")
		},
		"statusClass": func(s models.JobStatus) string {
			switch s {
			case models.JobStatusDone:
				return "status-done"
			case models.JobStatusFailed:
				return "status-failed"
			case models.JobStatusSigning, models.JobStatusPublishing:
				return "status-signing"
			default:
				return "status-queued"
			}
		},
		"statusEmoji": func(s models.JobStatus) string {
			switch s {
			case models.JobStatusDone:
				return "✅"
			case models.JobStatusFailed:
				return "❌"
			case models.JobStatusSigning:
				return "⚙️"
			case models.JobStatusPublishing:
				return "📦"
			default:
				return "⏳"
			}
		},
		"add": func(a, b int) int { return a + b },
		"slice": func(s string, start, end int) string {
			r := []rune(s)
			if end > len(r) {
				end = len(r)
			}
			return string(r[start:end])
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(webFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	s := &Server{
		cfg:     cfg,
		jobMgr:  jobMgr,
		certMgr: certMgr,
		rdb:     rdb,
		logger:  logger,
		mux:     http.NewServeMux(),
		tmpl:    tmpl,
	}

	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /readyz", s.handleReady)
	s.mux.HandleFunc("GET /jobs/{id}", s.handleJobPage)
	s.mux.HandleFunc("GET /artifacts/{id}/manifest.plist", s.handleManifest)
	s.mux.HandleFunc("GET /api/jobs/{id}/status", s.handleJobStatus)

	// Auth & Admin endpoints
	s.mux.HandleFunc("GET /login", s.handleLoginPage)
	s.mux.HandleFunc("POST /login", s.handleLoginSubmit)
	s.mux.HandleFunc("POST /logout", s.handleLogout)

	s.mux.HandleFunc("GET /api/jobs", s.handleJobList)
	s.mux.HandleFunc("DELETE /api/jobs/{id}", s.handleJobDelete)
	s.mux.HandleFunc("GET /api/jobs/{id}/logs", s.handleJobLogsSSE)
	s.mux.HandleFunc("GET /api/events", s.handleEventsSSE)
	s.mux.HandleFunc("GET /api/certs", s.handleCertList)
	s.mux.HandleFunc("GET /api/certs/{userID}/{setID}/password", s.handleCertPassword)
	s.mux.HandleFunc("DELETE /api/certs/{userID}/{setID}", s.handleCertDelete)
	s.mux.HandleFunc("POST /api/certs/{userID}/{setID}/check", s.handleCertCheck)
	s.mux.HandleFunc("POST /api/certs/upload", s.handleCertUpload)
	s.mux.HandleFunc("POST /api/jobs/create", s.handleJobCreate)
	s.mux.HandleFunc("GET /dashboard", s.handleDashboard)
	s.mux.HandleFunc("GET /", s.handleHome)

	// Serve static files
	s.mux.Handle("GET /static/", http.FileServer(http.FS(webFS)))

	return s, nil
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

// --- Health ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.rdb.Ping(ctx).Err(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "not_ready"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

// baseURL derives the public base URL from the incoming request.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

type jobPageData struct {
	Job         *models.Job
	IpaURL      string
	ManifestURL string
	IsDone      bool
	IsFailed    bool
	IsPending   bool
}

func (s *Server) handleJobPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, err := s.jobMgr.GetJob(r.Context(), id)
	if err != nil {
		http.Error(w, "404 — Job not found", http.StatusNotFound)
		return
	}
	base := baseURL(r)
	data := jobPageData{
		Job:         j,
		IpaURL:      base + "/artifacts/" + id + "/signed.ipa",
		ManifestURL: base + "/artifacts/" + id + "/manifest.plist",
		IsDone:      j.Status == models.JobStatusDone,
		IsFailed:    j.Status == models.JobStatusFailed,
		IsPending:   j.Status != models.JobStatusDone && j.Status != models.JobStatusFailed,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "download.html", data); err != nil {
		s.logger.Error("download template error", "error", err)
	}
}

// handleManifest generates the OTA manifest.plist dynamically using the request host.
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	base := baseURL(r)
	ipaURL := base + "/artifacts/" + id + "/signed.ipa"
	iconURL := base + "/artifacts/" + id + "/icon.png"
	manifest := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>items</key><array><dict>
<key>assets</key><array>
<dict><key>kind</key><string>software-package</string><key>url</key><string>%s</string></dict>
<dict><key>kind</key><string>display-image</string><key>needs-shine</key><false/><key>url</key><string>%s</string></dict>
</array>
<key>metadata</key><dict>
<key>bundle-identifier</key><string>com.signy.signed</string>
<key>bundle-version</key><string>1.0</string>
<key>kind</key><string>software</string>
<key>title</key><string>Signed IPA (%s)</string>
</dict>
</dict></array></dict></plist>`, ipaURL, iconURL, id)
	w.Header().Set("Content-Type", "text/xml")
	w.Write([]byte(manifest)) //nolint:errcheck
}

// --- API ---

func (s *Server) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, err := s.jobMgr.GetJob(r.Context(), id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    j.Status,
		"updatedAt": j.UpdatedAt,
	})
}

func (s *Server) handleJobList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	list := s.fetchAllJobs(r.Context())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *Server) handleJobDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if err := s.jobMgr.DeleteJob(r.Context(), id); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Publish delete event to redis stream so clients can react
	s.rdb.Publish(r.Context(), "signy:events:global", fmt.Sprintf(`{"type":"deleted","job_id":"%s"}`, id))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func (s *Server) handleJobLogsSSE(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	j, err := s.jobMgr.GetJob(r.Context(), id)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	logPath := filepath.Join(j.ArtifactBasePath, "sign.log")

	// Create a context that cancels when the client disconnects or after a timeout
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	var file *os.File
	var lastPos int64 = 0

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Ensure we wait for file to be created before tailing
	for i := 0; i < 20; i++ {
		file, err = os.Open(logPath)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if file == nil {
		_, _ = fmt.Fprintf(w, "data: no log file available yet\n\n")
		flusher.Flush()
		return
	}
	defer func() { _ = file.Close() }()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := file.Stat()
			if err != nil {
				continue
			}

			if info.Size() > lastPos {
				buffer := make([]byte, info.Size()-lastPos)
				_, err := file.ReadAt(buffer, lastPos)
				if err == nil {
					// SSE lines must not contain raw newlines without `data:` prefix
					lines := bytes.Split(buffer, []byte("\n"))
					for _, line := range lines {
						if len(line) > 0 {
							// Check if we need to escape json
							js, _ := json.Marshal(string(line))
							_, _ = fmt.Fprintf(w, "data: %s\n\n", js)
						}
					}
					flusher.Flush()
					lastPos = info.Size()
				}
			}

			// If job is done, stop tailing after reading final contents
			j, _ := s.jobMgr.GetJob(r.Context(), id)
			if j != nil && (j.Status == models.JobStatusDone || j.Status == models.JobStatusFailed) {
				// Re-read once just in case we missed a tiny tail
				info, _ := file.Stat()
				if info.Size() > lastPos {
					buffer := make([]byte, info.Size()-lastPos)
					_, _ = file.ReadAt(buffer, lastPos)
					lines := bytes.Split(buffer, []byte("\n"))
					for _, line := range lines {
						if len(line) > 0 {
							js, _ := json.Marshal(string(line))
							_, _ = fmt.Fprintf(w, "data: %s\n\n", js)
						}
					}
					flusher.Flush()
				}
				_, _ = fmt.Fprintf(w, "event: eof\ndata: null\n\n")
				flusher.Flush()
				return
			}
		}
	}
}

func (s *Server) handleEventsSSE(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	pubsub := s.rdb.Subscribe(ctx, "signy:events:global")
	defer func() { _ = pubsub.Close() }()
	ch := pubsub.Channel()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			_, _ = fmt.Fprintf(w, "data: %s\n\n", msg.Payload)
			flusher.Flush()
		}
	}
}

// --- Admin Dashboard & Auth ---

func (s *Server) setAuthCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     "signy_session",
		Value:    value,
		HttpOnly: true,
		Secure:   false, // Change to true if strictly HTTPS
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		MaxAge:   maxAge,
	})
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.cfg.AdminPassword == "" {
		return true
	}

	cookie, err := r.Cookie("signy_session")
	if err != nil || cookie.Value != s.cfg.AdminPassword {
		http.Redirect(w, r, "/login", http.StatusFound)
		return false
	}
	return true
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "home.html", nil); err != nil {
		s.logger.Error("home template error", "error", err)
	}
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	// If already authenticated or no password required, go to dashboard
	if s.cfg.AdminPassword == "" {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}
	cookie, err := r.Cookie("signy_session")
	if err == nil && cookie.Value == s.cfg.AdminPassword {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "login.html", nil); err != nil {
		s.logger.Error("login template error", "error", err)
	}
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdminPassword == "" {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	password := r.FormValue("password")
	if password == s.cfg.AdminPassword {
		// Valid logic
		s.setAuthCookie(w, password, 86400*30) // 30 days
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	// Invalid logic, show login with error
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "login.html", map[string]string{"Error": "Invalid password"}); err != nil {
		s.logger.Error("login template error", "error", err)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.setAuthCookie(w, "", -1)
	http.Redirect(w, r, "/", http.StatusFound)
}

type dashboardData struct {
	Jobs  []*models.Job
	Certs []*models.CertSet
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	jobs := s.fetchAllJobs(r.Context())

	certs, err := s.certMgr.ListAll()
	if err != nil {
		s.logger.Error("failed to list certs", "error", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "dashboard.html", dashboardData{Jobs: jobs, Certs: certs}); err != nil {
		s.logger.Error("dashboard template error", "error", err)
	}
}

func (s *Server) fetchAllJobs(ctx context.Context) []*models.Job {
	keys, err := s.rdb.Keys(ctx, "signy:job:*").Result()
	if err != nil {
		return nil
	}
	var jobs []*models.Job
	for _, key := range keys {
		data, err := s.rdb.HGetAll(ctx, key).Result()
		if err != nil || len(data) == 0 {
			continue
		}
		j := parseJobFromMap(data)
		if j != nil {
			jobs = append(jobs, j)
		}
	}
	// Sort by created_at desc
	for i := 0; i < len(jobs); i++ {
		for k := i + 1; k < len(jobs); k++ {
			if jobs[k].CreatedAt.After(jobs[i].CreatedAt) {
				jobs[i], jobs[k] = jobs[k], jobs[i]
			}
		}
	}
	if len(jobs) > 100 {
		jobs = jobs[:100]
	}
	return jobs
}

func (s *Server) handleCertPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	userIDStr := r.PathValue("userID")
	setID := r.PathValue("setID")

	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}

	pass, err := s.certMgr.GetP12Password(userID, setID)
	if err != nil {
		http.Error(w, "password unavailable or failed to decrypt", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"password": pass}) //nolint:errcheck
}

func (s *Server) handleCertCheck(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	userIDStr := r.PathValue("userID")
	setID := r.PathValue("setID")

	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}

	status, detail, checkErr := s.certMgr.CheckStatus(userID, setID)

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"status": status,
		"detail": detail,
	}
	if checkErr != nil {
		resp["error"] = checkErr.Error()
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleCertDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	userIDStr := r.PathValue("userID")
	setID := r.PathValue("setID")

	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}

	if err := s.certMgr.Delete(userID, setID); err != nil {
		http.Error(w, "failed to delete", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// parseJobFromMap converts a Redis HGetAll map to a Job struct.
func parseJobFromMap(m map[string]string) *models.Job {
	j := &models.Job{
		JobID:             m["job_id"],
		CertSetID:         m["certset_id"],
		IPAPath:           m["ipa_path"],
		ArtifactBasePath:  m["artifact_base_path"],
		Status:            models.JobStatus(m["status"]),
		ErrorCode:         m["error_code"],
		UserFriendlyError: m["user_friendly_error"],
	}
	if uid, err := strconv.ParseInt(m["user_id"], 10, 64); err == nil {
		j.UserID = uid
	}
	if rc, err := strconv.Atoi(m["retry_count"]); err == nil {
		j.RetryCount = rc
	}
	if ts := m["created_at"]; ts != "" {
		_ = j.CreatedAt.UnmarshalText([]byte(ts))
	}
	if ts := m["updated_at"]; ts != "" {
		_ = j.UpdatedAt.UnmarshalText([]byte(ts))
	}
	return j
}

// --- Cert List (JSON) ---

func (s *Server) handleCertList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	certs, err := s.certMgr.ListAll()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(certs)
}

// --- Cert Upload (web form) ---

func (s *Server) handleCertUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	// 32 MB memory limit for multipart parse
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	password := r.FormValue("p12_password")

	// Read P12 file
	p12File, _, err := r.FormFile("p12_file")
	if err != nil {
		http.Error(w, "p12_file required", http.StatusBadRequest)
		return
	}
	defer p12File.Close()
	p12Data, err := io.ReadAll(p12File)
	if err != nil {
		http.Error(w, "failed to read p12", http.StatusInternalServerError)
		return
	}

	// Read provision file
	provFile, _, err := r.FormFile("provision_file")
	if err != nil {
		http.Error(w, "provision_file required", http.StatusBadRequest)
		return
	}
	defer provFile.Close()
	provData, err := io.ReadAll(provFile)
	if err != nil {
		http.Error(w, "failed to read provision", http.StatusInternalServerError)
		return
	}

	// userID from form (admin can upload on behalf of any Telegram user)
	var userID int64 = 0
	if uid := r.FormValue("user_id"); uid != "" {
		userID, _ = strconv.ParseInt(uid, 10, 64)
	}
	if userID == 0 {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}

	cs, err := s.certMgr.Create(r.Context(), userID, p12Data, password, provData)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cs)
}

// --- Job Create (web form) ---

func (s *Server) handleJobCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	maxIPA := s.cfg.MaxIPABytes()
	if err := r.ParseMultipartForm(maxIPA + 64<<20); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	certSetID := r.FormValue("certset_id")
	if certSetID == "" {
		http.Error(w, "certset_id required", http.StatusBadRequest)
		return
	}
	var userID int64
	if uid := r.FormValue("user_id"); uid != "" {
		userID, _ = strconv.ParseInt(uid, 10, 64)
	}
	if userID == 0 {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}

	// Read IPA
	ipaFile, ipaHeader, err := r.FormFile("ipa_file")
	if err != nil {
		http.Error(w, "ipa_file required", http.StatusBadRequest)
		return
	}
	defer ipaFile.Close()

	if ipaHeader.Size > maxIPA {
		http.Error(w, fmt.Sprintf("ipa too large (max %d MB)", s.cfg.MaxIPAMB), http.StatusRequestEntityTooLarge)
		return
	}

	// Save IPA to incoming path
	incomingDir := filepath.Join(s.cfg.StoragePath, "users", strconv.FormatInt(userID, 10), "incoming")
	_ = os.MkdirAll(incomingDir, 0755)
	ipaPath := filepath.Join(incomingDir, "web_"+strconv.FormatInt(time.Now().UnixNano(), 36)+".ipa")
	ipaOut, err := os.Create(ipaPath)
	if err != nil {
		http.Error(w, "failed to save ipa", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(ipaOut, ipaFile); err != nil {
		_ = ipaOut.Close()
		http.Error(w, "failed to write ipa", http.StatusInternalServerError)
		return
	}
	_ = ipaOut.Close()

	// Read dylib files (optional)
	opts := models.SigningOptions{
		BundleName:    r.FormValue("bundle_name"),
		BundleID:      r.FormValue("bundle_id"),
		BundleVersion: r.FormValue("bundle_version"),
	}

	dylibFiles := r.MultipartForm.File["dylib_files"]
	for _, fh := range dylibFiles {
		if fh.Size > 50<<20 {
			continue // skip oversized dylibs
		}
		df, err := fh.Open()
		if err != nil {
			continue
		}
		dylibPath := filepath.Join(incomingDir, "dylib_"+strconv.FormatInt(time.Now().UnixNano(), 36)+"_"+fh.Filename)
		dOut, err := os.Create(dylibPath)
		if err != nil {
			_ = df.Close()
			continue
		}
		_, _ = io.Copy(dOut, df)
		_ = dOut.Close()
		_ = df.Close()
		opts.DylibPaths = append(opts.DylibPaths, dylibPath)
	}

	job, err := s.jobMgr.CreateAndEnqueue(r.Context(), userID, certSetID, ipaPath, opts)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"job_id": job.JobID, "status": string(job.Status)})
}
