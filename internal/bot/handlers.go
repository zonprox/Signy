package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/go-redis/v9"
	"github.com/zonprox/Signy/internal/bot/fsm"
	"github.com/zonprox/Signy/internal/certset"
	"github.com/zonprox/Signy/internal/config"
	"github.com/zonprox/Signy/internal/crypto"
	"github.com/zonprox/Signy/internal/job"
	"github.com/zonprox/Signy/internal/metrics"
	"github.com/zonprox/Signy/internal/models"
	"github.com/zonprox/Signy/internal/storage"
)

// Handlers processes Telegram updates.
type Handlers struct {
	api        *tgbotapi.BotAPI
	cfg        *config.Config
	store      *storage.Manager
	certMgr    *certset.Manager
	jobMgr     *job.Manager
	fsm        *fsm.Store
	debouncer  *callbackDebouncer
	limiter    *rateLimiter
	logger     *slog.Logger
	rdb        *redis.Client
	processKey []byte // ephemeral encryption key for no-MASTER_KEY mode
}

// NewHandlers creates bot handlers.
func NewHandlers(
	api *tgbotapi.BotAPI,
	cfg *config.Config,
	store *storage.Manager,
	certMgr *certset.Manager,
	jobMgr *job.Manager,
	fsmStore *fsm.Store,
	rdb *redis.Client,
	logger *slog.Logger,
	processKey []byte, // ephemeral encryption key; shared with Worker when MASTER_KEY is set
) *Handlers {
	return &Handlers{
		api:        api,
		cfg:        cfg,
		store:      store,
		certMgr:    certMgr,
		jobMgr:     jobMgr,
		fsm:        fsmStore,
		debouncer:  newCallbackDebouncer(2 * time.Second),
		limiter:    newRateLimiter(30, time.Minute),
		logger:     logger,
		rdb:        rdb,
		processKey: processKey,
	}
}

// HandleUpdate routes a Telegram update to the appropriate handler.
func (h *Handlers) HandleUpdate(ctx context.Context, update tgbotapi.Update) {
	logMiddleware(h.logger, update)

	if update.CallbackQuery != nil {
		h.handleCallback(ctx, update.CallbackQuery)
		return
	}

	if update.Message != nil {
		h.handleMessage(ctx, update.Message)
		return
	}
}

func (h *Handlers) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	userID := msg.From.ID

	if !h.limiter.Allow(userID) {
		h.sendText(msg.Chat.ID, "⏳ You're sending messages too quickly. Please wait a moment.")
		return
	}

	// Handle commands
	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			h.fsm.Clear(userID)
			h.sendMainMenu(msg.Chat.ID)
		case "sign":
			h.handleNewJob(ctx, msg.Chat.ID, userID)
		case "certs":
			h.sendCertMenu(msg.Chat.ID)
		case "jobs":
			h.handleMyJobs(ctx, msg.Chat.ID, userID)
		case "help":
			h.sendHelp(msg.Chat.ID)
		default:
			h.sendMainMenu(msg.Chat.ID)
		}
		return
	}

	// Handle FSM states
	state := h.fsm.GetState(userID)
	switch state {

	case models.StateCertCreateP12:
		h.handleCertP12Upload(ctx, msg)
	case models.StateCertCreatePassword:
		h.handleCertPassword(ctx, msg)
	case models.StateCertCreateProvision:
		h.handleCertProvisionUpload(ctx, msg)
	case models.StateJobUploadIPA:
		h.handleIPAUpload(ctx, msg)
	case models.StateJobPasswordPrompt:
		h.handleJobPassword(ctx, msg)
	case models.StateJobSetBundleName,
		models.StateJobSetBundleID,
		models.StateJobSetBundleVersion:
		h.handleJobTextOption(ctx, msg, state)
	case models.StateJobUploadDylib:
		h.handleJobDylibUpload(ctx, msg)
	default:
		h.sendMainMenu(msg.Chat.ID)
	}
}

func (h *Handlers) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	// Debounce
	dedupeKey := fmt.Sprintf("%d:%s", cb.From.ID, cb.Data)
	if !h.debouncer.ShouldProcess(dedupeKey) {
		h.ackCallback(cb.ID, "")
		return
	}

	h.ackCallback(cb.ID, "")

	data := cb.Data
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID

	switch {
	// Main menu
	case data == CBMainNewJob:
		h.handleNewJob(ctx, chatID, cb.From.ID)
	case data == CBMainCerts:
		// Send a new message instead of edit to trigger the new list rendering
		deleteMsg := tgbotapi.NewDeleteMessage(chatID, msgID)
		_, _ = h.api.Send(deleteMsg)
		h.sendCertMenu(chatID)
	case data == CBMainMyJobs:
		h.handleMyJobs(ctx, chatID, cb.From.ID)
	case data == CBMainSettings:
		h.editMenu(chatID, msgID, "⚙️ *Settings*\n\nNo configurable settings at this time.", BackToMainKeyboard())
	case data == CBMainHelp:
		h.editHelp(chatID, msgID)

	// Cert menu
	case data == CBCertAdd:
		h.startCertCreate(chatID, cb.From.ID)
	case data == CBCertStatus:
		h.handleCheckStatus(ctx, chatID, cb.From.ID)
	case data == CBCertDelete:
		h.handleDeleteMenu(ctx, chatID, cb.From.ID)
	case data == CBCertBack || data == CBBack:
		h.fsm.Clear(cb.From.ID)
		h.editMenu(chatID, msgID, "🏠 *Main Menu*\n\nWelcome to Signy IPA Signing Service! Choose an option:", MainMenuKeyboard())

	// Cert selection
	case strings.HasPrefix(data, CBCertSelectPfx):
		// This callback from set_default is now theoretically dead code, or refactored for another purpose
		// But keeping standard select to prevent panic. If we have selection elsewhere, it can be handled here.
		// For now it does nothing since we don't have a default selection menu.
	case strings.HasPrefix(data, CBCertDeletePfx):
		setID := strings.TrimPrefix(data, CBCertDeletePfx)
		h.handleDeleteConfirm(chatID, setID)
	case strings.HasPrefix(data, CBCertDelConfirm):
		setID := strings.TrimPrefix(data, CBCertDelConfirm)
		h.handleDeleteExecute(ctx, chatID, cb.From.ID, setID)
	case data == CBCertDelCancel:
		h.sendCertMenu(chatID)

	// Job flow
	case strings.HasPrefix(data, CBJobCertPfx):
		setID := strings.TrimPrefix(data, CBJobCertPfx)
		h.handleJobSelectCert(ctx, chatID, cb.From.ID, setID)

	// Job Options
	case data == CBJobOptName:
		h.promptJobOption(chatID, cb.From.ID, models.StateJobSetBundleName, "📝 *App Name*\n\nEnter the new App Name:")
	case data == CBJobOptBundle:
		h.promptJobOption(chatID, cb.From.ID, models.StateJobSetBundleID, "🆔 *Bundle ID*\n\nEnter the new Bundle ID (e.g., `com.example.app`):")
	case data == CBJobOptVersion:
		h.promptJobOption(chatID, cb.From.ID, models.StateJobSetBundleVersion, "🏷 *Version*\n\nEnter the new Version string (e.g., `1.0.0`):")
	case data == CBJobOptDylib:
		h.promptJobOption(chatID, cb.From.ID, models.StateJobUploadDylib, "💉 *Inject Dylib*\n\nPlease upload a `.dylib` file now:")

	case data == CBJobConfirm:
		h.handleJobConfirm(ctx, chatID, cb.From.ID, msgID)
	case data == CBJobCancel:
		h.fsm.Clear(cb.From.ID)
		h.editMenu(chatID, msgID, "❌ Job cancelled.", MainMenuKeyboard())

	// Job details
	case strings.HasPrefix(data, CBJobDetailPfx):
		jobID := strings.TrimPrefix(data, CBJobDetailPfx)
		h.handleJobDetail(ctx, chatID, jobID)
	}
}

// === MAIN MENU ===

func (h *Handlers) sendMainMenu(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "🏠 *Main Menu*\n\nWelcome to Signy IPA Signing Service! Choose an option:")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = MainMenuKeyboard()
	_, _ = h.api.Send(msg)
}

func (h *Handlers) sendCertMenu(chatID int64) {
	// For cert menu, list all cert sets user currently has
	sets, _ := h.certMgr.List(chatID) // chatID is also userID in private chats

	msgText := "🪪 *Certificate Management*\n\n"
	if len(sets) == 0 {
		msgText += "📭 You don't have any certificates yet.\n\n"
	} else {
		msgText += "📋 *Your Certificates:*\n"
		for i, s := range sets {
			msgText += fmt.Sprintf("%d. *%s* `%s`\n", i+1, s.Name, s.SetID)
		}
		msgText += "\n"
	}
	msgText += "Manage your signing certificates and provisioning profiles:"

	msg := tgbotapi.NewMessage(chatID, msgText)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = CertMenuKeyboard()
	_, _ = h.api.Send(msg)
}

func (h *Handlers) sendHelp(chatID int64) {
	text := `❓ *Help*

*How to sign an IPA:*
1️⃣ First, add a certificate set (P12 + provisioning profile)
2️⃣ Start a new signing job
3️⃣ Upload your IPA file
4️⃣ Confirm and wait for signing
5️⃣ Install via the OTA link

*Certificate Management:*
• *Add Cert Set* — Upload P12 + password + provision
• *Check Status* — Verify your cert set is valid
• *Delete* — Remove a cert set

*Limits:*
• Max IPA size: ` + fmt.Sprintf("%d MB", h.cfg.MaxIPAMB) + `
• Max cert sets: ` + fmt.Sprintf("%d", h.cfg.MaxCertSetsPerUser) + `

_Send /start anytime to return to the main menu._`

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = BackToMainKeyboard()
	_, _ = h.api.Send(msg)
}

// === CERT CREATE FLOW ===

func (h *Handlers) startCertCreate(chatID int64, userID int64) {
	h.fsm.Set(userID, &models.UserSession{
		UserID: userID,
		State:  models.StateCertCreateP12,
	})
	h.sendText(chatID, "➕ *Add Certificate Set*\n\n📤 Please upload your *.p12* certificate file.")
}

func (h *Handlers) handleCertP12Upload(ctx context.Context, msg *tgbotapi.Message) {
	if msg.Document == nil {
		h.sendText(msg.Chat.ID, "⚠️ Please send a *.p12* file as a document attachment.")
		return
	}

	if msg.Document.FileSize > int(h.cfg.MaxP12Bytes()) {
		h.sendText(msg.Chat.ID, fmt.Sprintf("⚠️ P12 file is too large. Maximum size is %d MB.", h.cfg.MaxP12MB))
		return
	}

	destPath := h.store.IncomingIPAPath(msg.From.ID, msg.Document.FileID) + ".p12"
	start := time.Now()

	msgDownloading := tgbotapi.NewMessage(msg.Chat.ID, "⏳ Downloading P12... Please wait.")
	msgDownloading.ParseMode = "Markdown"
	_, _ = h.api.Send(msgDownloading)

	_, err := h.downloadTelegramFile(msg.Document.FileID, destPath, h.cfg.MaxP12Bytes())

	metrics.FileDownloadDuration.Observe(time.Since(start).Seconds())
	if err != nil {
		h.logger.Error("failed to download p12", "error", err)
		h.sendText(msg.Chat.ID, "❌ Failed to download P12 file. Please try again.")
		return
	}

	sess := h.fsm.Get(msg.From.ID)
	sess.P12Path = destPath
	sess.State = models.StateCertCreatePassword
	h.fsm.Set(msg.From.ID, sess)

	h.sendText(msg.Chat.ID, "🔐 Please send the *P12 password*.\n\n⚠️ _Your message will not be echoed back. The password will be securely stored._")
}

func (h *Handlers) handleCertPassword(ctx context.Context, msg *tgbotapi.Message) {
	password := msg.Text

	// Try to delete the password message for security
	deleteMsg := tgbotapi.NewDeleteMessage(msg.Chat.ID, msg.MessageID)
	_, _ = h.api.Send(deleteMsg)

	if password == "" {
		h.sendText(msg.Chat.ID, "⚠️ Password cannot be empty. Please send the P12 password:")
		return
	}

	sess := h.fsm.Get(msg.From.ID)

	// Read P12 data
	p12Data, err := h.store.ReadFile(sess.P12Path)
	if err != nil {
		h.logger.Error("failed to read p12", "error", err)
		h.sendText(msg.Chat.ID, "❌ Internal error. Please start over.")
		h.fsm.Clear(msg.From.ID)
		return
	}

	// Store password temporarily in session (will create cert set after provision upload)
	// We encrypt the password in Redis for the session
	encPass, err := crypto.EncryptEphemeral(h.processKey, []byte(password))
	if err != nil {
		h.logger.Error("encrypt session password", "error", err)
		h.sendText(msg.Chat.ID, "❌ Internal error. Please start over.")
		h.fsm.Clear(msg.From.ID)
		return
	}

	// Store encrypted password token in Redis with short TTL
	tokenKey := fmt.Sprintf("signy:session_pass:%d", msg.From.ID)
	h.rdb.Set(ctx, tokenKey, encPass, 10*time.Minute)

	_ = p12Data // will be read again during cert creation

	sess.State = models.StateCertCreateProvision
	h.fsm.Set(msg.From.ID, sess)

	h.sendText(msg.Chat.ID, "🔑 Password received securely.\n\n📤 Now please upload your *.mobileprovision* file.")
}

func (h *Handlers) handleCertProvisionUpload(ctx context.Context, msg *tgbotapi.Message) {
	if msg.Document == nil {
		h.sendText(msg.Chat.ID, "⚠️ Please send a *.mobileprovision* file as a document attachment.")
		return
	}

	if msg.Document.FileSize > int(h.cfg.MaxProvBytes()) {
		h.sendText(msg.Chat.ID, fmt.Sprintf("⚠️ Provision file is too large. Maximum size is %d KB.", h.cfg.MaxProvKB))
		return
	}

	destPath := h.store.IncomingIPAPath(msg.From.ID, msg.Document.FileID) + ".mobileprovision"

	msgDownloading := tgbotapi.NewMessage(msg.Chat.ID, "⏳ Downloading Provision... Please wait.")
	msgDownloading.ParseMode = "Markdown"
	_, _ = h.api.Send(msgDownloading)

	_, err := h.downloadTelegramFile(msg.Document.FileID, destPath, h.cfg.MaxProvBytes())
	if err != nil {
		h.logger.Error("failed to download provision", "error", err)
		h.sendText(msg.Chat.ID, "❌ Failed to download provision file. Please try again.")
		return
	}

	sess := h.fsm.Get(msg.From.ID)

	// Read files
	p12Data, err := h.store.ReadFile(sess.P12Path)
	if err != nil {
		h.logger.Error("read p12 for cert creation", "error", err)
		h.sendText(msg.Chat.ID, "❌ Internal error reading P12. Please start over.")
		h.fsm.Clear(msg.From.ID)
		return
	}

	provData, err := h.store.ReadFile(destPath)
	if err != nil {
		h.logger.Error("read provision for cert creation", "error", err)
		h.sendText(msg.Chat.ID, "❌ Internal error reading provision. Please start over.")
		h.fsm.Clear(msg.From.ID)
		return
	}

	// Retrieve password from Redis
	tokenKey := fmt.Sprintf("signy:session_pass:%d", msg.From.ID)
	encPass, err := h.rdb.Get(ctx, tokenKey).Result()
	if err != nil {
		h.logger.Error("retrieve session password", "error", err)
		h.sendText(msg.Chat.ID, "❌ Session expired. Please start over from the certificate menu.")
		h.fsm.Clear(msg.From.ID)
		return
	}
	h.rdb.Del(ctx, tokenKey) // delete after use

	passBytes, err := crypto.DecryptEphemeral(h.processKey, encPass)
	if err != nil {
		h.logger.Error("decrypt session password", "error", err)
		h.sendText(msg.Chat.ID, "❌ Internal error. Please start over.")
		h.fsm.Clear(msg.From.ID)
		return
	}

	// Create cert set
	cs, err := h.certMgr.Create(ctx, msg.From.ID, p12Data, string(passBytes), provData)
	if err != nil {
		h.logger.Error("create cert set", "error", err)
		h.sendText(msg.Chat.ID, fmt.Sprintf("❌ Failed to create certificate set: %s", err.Error()))
		h.fsm.Clear(msg.From.ID)
		return
	}

	metrics.CertSetsTotal.WithLabelValues("create").Inc()

	// Cleanup temp files
	_ = h.store.RemoveAll(sess.P12Path)
	_ = h.store.RemoveAll(destPath)

	h.fsm.Clear(msg.From.ID)

	text := fmt.Sprintf("✅ *Certificate Set Created!*\n\n"+
		"📛 Name: *%s*\n"+
		"🆔 ID: `%s`\n"+
		"🔑 Fingerprint: `%s`\n"+
		"📋 %s\n",
		cs.Name, cs.SetID, cs.P12FingerprintShort, cs.ProvisionSummary)

	m := tgbotapi.NewMessage(msg.Chat.ID, text)
	m.ParseMode = "Markdown"
	m.ReplyMarkup = CertMenuKeyboard()
	_, _ = h.api.Send(m)
}

func (h *Handlers) handleCheckStatus(ctx context.Context, chatID int64, userID int64) {
	sets, _ := h.certMgr.List(userID)
	if len(sets) == 0 {
		h.sendText(chatID, "📭 You have no certificate sets.\n\nPlease add a cert set.")
		return
	}

	// Just check the most recently added for simplicity or loop. Let's just do the first one in the list.
	cs := sets[0]

	status, detail, _ := h.certMgr.CheckStatus(userID, cs.SetID)
	metrics.CertSetsTotal.WithLabelValues("check_status").Inc()

	var statusEmoji string
	switch status {
	case models.CertSetStatusValid:
		statusEmoji = "✅"
	case models.CertSetStatusMissingFiles:
		statusEmoji = "⚠️"
	default:
		statusEmoji = "❌"
	}

	lastChecked := "Never"
	if cs.LastCheckedAt != nil {
		lastChecked = cs.LastCheckedAt.Format("2006-01-02 15:04 UTC")
	}

	text := fmt.Sprintf("✅ *Certificate Status Check*\n\n"+
		"📛 Name: *%s*\n"+
		"🆔 ID: `%s`\n"+
		"%s Status: *%s*\n"+
		"📝 Detail: %s\n"+
		"🔑 Fingerprint: `%s`\n"+
		"📋 %s\n"+
		"🕐 Last Checked: %s",
		cs.Name, cs.SetID, statusEmoji, status, detail,
		cs.P12FingerprintShort, cs.ProvisionSummary, lastChecked)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = CertMenuKeyboard()
	_, _ = h.api.Send(msg)
}

// === CERT DELETE ===

func (h *Handlers) handleDeleteMenu(ctx context.Context, chatID int64, userID int64) {
	sets, _ := h.certMgr.List(userID)
	if len(sets) == 0 {
		h.sendText(chatID, "📭 No certificate sets to delete.")
		return
	}

	var infos []CertSetInfo
	for _, s := range sets {
		infos = append(infos, CertSetInfo{
			SetID: s.SetID,
			Name:  s.Name,
		})
	}

	msg := tgbotapi.NewMessage(chatID, "🗑 *Delete Certificate Set*\n\nSelect a certificate set to delete:")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = CertListKeyboard(infos, CBCertDeletePfx)
	_, _ = h.api.Send(msg)
}

func (h *Handlers) handleDeleteConfirm(chatID int64, setID string) {
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("⚠️ *Are you sure?*\n\nThis will permanently delete cert set `%s` and all associated files.", setID))
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = ConfirmDeleteKeyboard(setID)
	_, _ = h.api.Send(msg)
}

func (h *Handlers) handleDeleteExecute(ctx context.Context, chatID int64, userID int64, setID string) {
	if err := h.certMgr.Delete(userID, setID); err != nil {
		h.sendText(chatID, fmt.Sprintf("❌ Failed to delete: %s", err.Error()))
		return
	}

	metrics.CertSetsTotal.WithLabelValues("delete").Inc()
	h.sendText(chatID, fmt.Sprintf("🗑 Certificate set `%s` deleted.", setID))
	h.sendCertMenu(chatID)
}

// === NEW JOB FLOW ===

func (h *Handlers) handleNewJob(ctx context.Context, chatID int64, userID int64) {
	sets, _ := h.certMgr.List(userID)
	if len(sets) == 0 {
		h.sendText(chatID, "📭 You need to add a certificate set before creating a signing job.\n\nGo to *🪪 Certificates* → *➕ Add Cert Set*.")
		return
	}

	h.fsm.Set(userID, &models.UserSession{
		UserID: userID,
		State:  models.StateJobSelectCert,
	})

	var infos []CertSetInfo
	for _, s := range sets {
		infos = append(infos, CertSetInfo{
			SetID: s.SetID,
			Name:  s.Name,
		})
	}

	msg := tgbotapi.NewMessage(chatID, "➕ *New Signing Job*\n\n📋 *Choose Certificate*\n\nSelect a cert set for this job:")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = CertListKeyboard(infos, CBJobCertPfx)
	_, _ = h.api.Send(msg)
}

func (h *Handlers) handleJobSelectCert(ctx context.Context, chatID int64, userID int64, setID string) {
	sess := h.fsm.Get(userID)
	sess.SelectedCertSetID = setID
	sess.State = models.StateJobUploadIPA
	h.fsm.Set(userID, sess)

	h.sendText(chatID, "📤 Please send or forward your *.ipa* file now.")
}

func (h *Handlers) handleIPAUpload(ctx context.Context, msg *tgbotapi.Message) {
	if msg.Document == nil {
		h.sendText(msg.Chat.ID, "⚠️ Please send an *.ipa* file as a document attachment.")
		return
	}

	if int64(msg.Document.FileSize) > h.cfg.MaxIPABytes() {
		h.sendText(msg.Chat.ID, fmt.Sprintf("⚠️ IPA file is too large. Maximum size is %d MB.", h.cfg.MaxIPAMB))
		return
	}

	// We rely entirely on MaxIPAMB internally because we use the local API unconditionally.

	msgDownloading := tgbotapi.NewMessage(msg.Chat.ID, "⏳ Downloading IPA... Please wait.")
	msgDownloading.ParseMode = "Markdown"
	_, _ = h.api.Send(msgDownloading)

	destPath := h.store.IncomingIPAPath(msg.From.ID, msg.Document.FileID)
	start := time.Now()

	size, err := h.downloadTelegramFile(msg.Document.FileID, destPath, h.cfg.MaxIPABytes())
	metrics.FileDownloadDuration.Observe(time.Since(start).Seconds())
	if err != nil {
		h.logger.Error("download ipa", "error", err)
		h.sendText(msg.Chat.ID, "❌ Failed to download IPA. Please try again.")
		return
	}

	sess := h.fsm.Get(msg.From.ID)
	sess.IPAPath = destPath
	sess.IPASize = size

	// If no MASTER_KEY, need password each time
	if !h.cfg.HasMasterKey() {
		sess.State = models.StateJobPasswordPrompt
		h.fsm.Set(msg.From.ID, sess)
		h.sendText(msg.Chat.ID, "🔐 MASTER\\_KEY is not configured. Please send your P12 password for this signing job.\n\n_The password will be used only for this job and not stored._")
		return
	}

	h.fsm.Set(msg.From.ID, sess)
	h.sendJobSummary(msg.Chat.ID, msg.From.ID)
}

func (h *Handlers) handleJobPassword(ctx context.Context, msg *tgbotapi.Message) {
	password := msg.Text

	// Delete password message
	deleteMsg := tgbotapi.NewDeleteMessage(msg.Chat.ID, msg.MessageID)
	_, _ = h.api.Send(deleteMsg)

	if password == "" {
		h.sendText(msg.Chat.ID, "⚠️ Password cannot be empty. Please send the P12 password:")
		return
	}

	// Store ephemeral password in Redis
	encPass, err := crypto.EncryptEphemeral(h.processKey, []byte(password))
	if err != nil {
		h.logger.Error("encrypt job password", "error", err)
		h.sendText(msg.Chat.ID, "❌ Internal error. Please try again.")
		return
	}

	tokenKey := fmt.Sprintf("signy:job_pass:%d", msg.From.ID)
	h.rdb.Set(ctx, tokenKey, encPass, 10*time.Minute)

	sess := h.fsm.Get(msg.From.ID)
	sess.State = models.StateJobConfirm
	h.fsm.Set(msg.From.ID, sess)

	h.sendJobSummary(msg.Chat.ID, msg.From.ID)
}

func (h *Handlers) sendJobSummary(chatID int64, userID int64) {
	sess := h.fsm.Get(userID)
	cs, _ := h.certMgr.Get(userID, sess.SelectedCertSetID)

	certName := sess.SelectedCertSetID
	if cs != nil {
		certName = cs.Name
	}

	sizeMB := float64(sess.IPASize) / (1024 * 1024)

	appName := sess.Options.BundleName
	if appName == "" {
		appName = "Keep Original"
	}
	appBundle := sess.Options.BundleID
	if appBundle == "" {
		appBundle = "Keep Original"
	}
	appVersion := sess.Options.BundleVersion
	if appVersion == "" {
		appVersion = "Keep Original"
	}

	text := fmt.Sprintf("📋 *Job Configuration*\n\n"+
		"🪪 Certificate: *%s*\n"+
		"📦 IPA Size: *%.1f MB*\n\n"+
		"📝 App Name: `%s`\n"+
		"🆔 Bundle ID: `%s`\n"+
		"🏷 Version: `%s`\n"+
		"💉 Dylibs: *%d injected*\n\n"+
		"_Select options below to modify them before signing._",
		certName, sizeMB, appName, appBundle, appVersion, len(sess.Options.DylibPaths))

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = JobOptionsKeyboard()
	_, _ = h.api.Send(msg)
}

func (h *Handlers) handleJobConfirm(ctx context.Context, chatID int64, userID int64, msgID int) {
	sess := h.fsm.Get(userID)

	// Dedupe check
	dedupeKey := fmt.Sprintf("%d:%s:%s", userID, sess.SelectedCertSetID, sess.IPAPath)
	isDupe, _ := h.jobMgr.CheckDedupe(ctx, dedupeKey, 5*time.Minute)
	if isDupe {
		h.sendText(chatID, "⚠️ This job appears to be a duplicate. Please wait for the existing job to complete.")
		h.fsm.Clear(userID)
		return
	}

	// If no MASTER_KEY, verify ephemeral password token exists before creating job
	if !h.cfg.HasMasterKey() {
		tokenKey := fmt.Sprintf("signy:job_pass:%d", userID)
		exists, _ := h.rdb.Exists(ctx, tokenKey).Result()
		if exists == 0 {
			h.sendText(chatID, "❌ Password session expired. Please start the job again.")
			h.fsm.Clear(userID)
			return
		}
	}

	j, err := h.jobMgr.CreateAndEnqueue(ctx, userID, sess.SelectedCertSetID, sess.IPAPath, sess.Options)
	if err != nil {
		h.logger.Error("create job", "error", err)
		h.sendText(chatID, fmt.Sprintf("❌ Failed to create job: %s", err.Error()))
		h.fsm.Clear(userID)
		return
	}

	// If no MASTER_KEY, associate the password token with the job
	if !h.cfg.HasMasterKey() {
		tokenKey := fmt.Sprintf("signy:job_pass:%d", userID)
		encPass, _ := h.rdb.Get(ctx, tokenKey).Result()
		jobPassKey := fmt.Sprintf("signy:job_pass_token:%s", j.JobID)
		h.rdb.Set(ctx, jobPassKey, encPass, 10*time.Minute)
		h.rdb.Del(ctx, tokenKey)
	}

	metrics.JobsTotal.WithLabelValues("queued").Inc()
	h.fsm.Clear(userID)

	text := fmt.Sprintf("✅ *Job Created!*\n\n"+
		"🆔 Job ID: `%s`\n"+
		"📊 Status: *QUEUED*\n\n"+
		"⏳ You will receive updates as the job progresses.",
		j.JobID)

	editMsg := tgbotapi.NewEditMessageText(chatID, msgID, text)
	editMsg.ParseMode = "Markdown"
	keyboard := BackToMainKeyboard()
	editMsg.ReplyMarkup = &keyboard
	_, err = h.api.Send(editMsg)
	if err == nil {
		msgKey := fmt.Sprintf("signy:job_msg:%s", j.JobID)
		h.rdb.Set(ctx, msgKey, msgID, 24*time.Hour)
	}
}

// === JOB OPTIONS ===

func (h *Handlers) promptJobOption(chatID int64, userID int64, nextState models.UserState, promptText string) {
	sess := h.fsm.Get(userID)
	if sess.State == models.StateIdle {
		return
	}
	sess.State = nextState
	h.fsm.Set(userID, sess)

	msg := tgbotapi.NewMessage(chatID, promptText)
	msg.ParseMode = "Markdown"
	_, _ = h.api.Send(msg)
}

func (h *Handlers) handleJobTextOption(ctx context.Context, msg *tgbotapi.Message, state models.UserState) {
	userID := msg.From.ID
	text := strings.TrimSpace(msg.Text)

	if text == "" {
		h.sendText(msg.Chat.ID, "⚠️ Value cannot be empty. Try again:")
		return
	}

	sess := h.fsm.Get(userID)
	switch state {
	case models.StateJobSetBundleName:
		sess.Options.BundleName = text
	case models.StateJobSetBundleID:
		sess.Options.BundleID = text
	case models.StateJobSetBundleVersion:
		sess.Options.BundleVersion = text
	}

	// Revert to job summary state
	sess.State = models.StateJobUploadIPA // using this state arbitrarily to hold before Confirm
	h.fsm.Set(userID, sess)

	// Refresh the menu
	h.sendJobSummary(msg.Chat.ID, userID)
}

func (h *Handlers) handleJobDylibUpload(ctx context.Context, msg *tgbotapi.Message) {
	userID := msg.From.ID

	if msg.Document == nil || !strings.HasSuffix(strings.ToLower(msg.Document.FileName), ".dylib") {
		h.sendText(msg.Chat.ID, "⚠️ Please upload a valid `.dylib` file:")
		return
	}

	if msg.Document.FileSize > 50*1024*1024 { // 50MB limit for dylibs
		h.sendText(msg.Chat.ID, "⚠️ Dylib is too large (max 50MB).")
		return
	}

	destPath := h.store.IncomingDylibPath(userID, msg.Document.FileID)

	msgDownloading := tgbotapi.NewMessage(msg.Chat.ID, "⏳ Downloading Dylib... Please wait.")
	msgDownloading.ParseMode = "Markdown"
	pMsg, _ := h.api.Send(msgDownloading)

	_, err := h.downloadTelegramFile(msg.Document.FileID, destPath, 50*1024*1024)

	deleteMsg := tgbotapi.NewDeleteMessage(msg.Chat.ID, pMsg.MessageID)
	_, _ = h.api.Send(deleteMsg)

	if err != nil {
		h.logger.Error("download dylib", "error", err)
		h.sendText(msg.Chat.ID, "❌ Failed to download Dylib. Please try again.")
		return
	}

	sess := h.fsm.Get(userID)
	sess.Options.DylibPaths = append(sess.Options.DylibPaths, destPath)
	sess.State = models.StateJobUploadIPA // revert back to holding state
	h.fsm.Set(userID, sess)

	h.sendText(msg.Chat.ID, "💉 Dylib added successfully!")
	h.sendJobSummary(msg.Chat.ID, userID)
}

// === MY JOBS ===

func (h *Handlers) handleMyJobs(ctx context.Context, chatID int64, userID int64) {
	jobs, err := h.jobMgr.ListUserJobs(ctx, userID, 10)
	if err != nil {
		h.sendText(chatID, "❌ Failed to load jobs.")
		return
	}

	if len(jobs) == 0 {
		msg := tgbotapi.NewMessage(chatID, "📭 No jobs found. Start a new signing job!")
		msg.ReplyMarkup = BackToMainKeyboard()
		_, _ = h.api.Send(msg)
		return
	}

	text := "🧾 *My Jobs*\n\n"
	var buttons [][]tgbotapi.InlineKeyboardButton

	for _, j := range jobs {
		emoji := statusEmoji(j.Status)
		text += fmt.Sprintf("%s `%s` — *%s* (%s)\n", emoji, j.JobID[:8], j.Status, j.CreatedAt.Format("Jan 2 15:04"))
		buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("%s %s", emoji, j.JobID[:8]),
				CBJobDetailPfx+j.JobID,
			),
		))
	}

	buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅ Main Menu", CBBack),
	))

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	_, _ = h.api.Send(msg)
}

func (h *Handlers) handleJobDetail(ctx context.Context, chatID int64, jobID string) {
	j, err := h.jobMgr.GetJob(ctx, jobID)
	if err != nil {
		h.sendText(chatID, "❌ Job not found.")
		return
	}

	text := fmt.Sprintf("%s *Job Details*\n\n"+
		"🆔 ID: `%s`\n"+
		"📊 Status: *%s*\n"+
		"📅 Created: %s\n"+
		"🔄 Retries: %d\n",
		statusEmoji(j.Status), j.JobID, j.Status,
		j.CreatedAt.Format("2006-01-02 15:04:05 UTC"),
		j.RetryCount)

	if j.Status == models.JobStatusFailed {
		text += fmt.Sprintf("\n❌ Error: %s", j.UserFriendlyError)
	}

	if j.Status == models.JobStatusDone {
		text += fmt.Sprintf("\n\n✅ Signed IPA ready.\n🆔 Job: `%s`\n📥 Check the download page on your server.", j.JobID)
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true
	msg.ReplyMarkup = BackToMainKeyboard()
	_, _ = h.api.Send(msg)
}

// === HELPERS ===

func (h *Handlers) sendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	_, _ = h.api.Send(msg)
}

// editMenu edits an existing message with new text and keyboard (in-place menu transition).
func (h *Handlers) editMenu(chatID int64, msgID int, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, text, keyboard)
	edit.ParseMode = "Markdown"
	_, _ = h.api.Send(edit)
}

// editHelp edits a message to show help text.
func (h *Handlers) editHelp(chatID int64, msgID int) {
	text := `❓ *Help*

*How to sign an IPA:*
1️⃣ First, add a certificate set (P12 + provisioning profile)
2️⃣ Start a new signing job
3️⃣ Upload your IPA file
4️⃣ Confirm and wait for signing
5️⃣ Install via the OTA link

*Certificate Management:*
• *Add Cert Set* — Upload P12 + password + provision
• *Check Status* — Verify your cert set is valid
• *Delete* — Remove a cert set

*Commands:*
/start — Main menu
/sign — New signing job
/certs — Certificate management
/jobs — My jobs
/help — This help

*Limits:*
• Max IPA size: ` + fmt.Sprintf("%d MB", h.cfg.MaxIPAMB) + `
• Max cert sets: ` + fmt.Sprintf("%d", h.cfg.MaxCertSetsPerUser) + ``

	h.editMenu(chatID, msgID, text, BackToMainKeyboard())
}

func (h *Handlers) ackCallback(callbackID string, text string) {
	callback := tgbotapi.NewCallback(callbackID, text)
	_, _ = h.api.Request(callback)
}

func statusEmoji(status models.JobStatus) string {
	switch status {
	case models.JobStatusQueued:
		return "🕐"
	case models.JobStatusSigning:
		return "✍️"
	case models.JobStatusPublishing:
		return "📦"
	case models.JobStatusDone:
		return "✅"
	case models.JobStatusFailed:
		return "❌"
	default:
		return "❓"
	}
}

// SubscribeJobEvents listens for job status events and sends Telegram notifications.
func (h *Handlers) SubscribeJobEvents(ctx context.Context) {
	sub := h.rdb.Subscribe(ctx, "signy:job:events")
	defer func() { _ = sub.Close() }()

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			var event map[string]string
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				h.logger.Warn("invalid job event", "error", err)
				continue
			}

			jobID := event["job_id"]
			status := event["status"]
			message := event["message"]

			j, err := h.jobMgr.GetJob(ctx, jobID)
			if err != nil {
				continue
			}

			emoji := statusEmoji(models.JobStatus(status))
			text := fmt.Sprintf("%s *Job Update*\n\n🆔 `%s`\n📊 Status: *%s*\n📝 %s", emoji, jobID[:8], status, message)

			if status == "DONE" {
				if h.cfg.BaseURL != "" {
					text += fmt.Sprintf("\n\n✅ Ready — [Install / Download](%s/jobs/%s)", h.cfg.BaseURL, jobID)
				} else {
					text += "\n\n✅ Ready — open the download page on your server."
				}
			}

			msgKey := fmt.Sprintf("signy:job_msg:%s", jobID)
			msgID, err := h.rdb.Get(ctx, msgKey).Int()

			var sentMsg tgbotapi.Message
			var sendErr error

			if err == nil && msgID > 0 {
				editMsg := tgbotapi.NewEditMessageText(j.UserID, msgID, text)
				editMsg.ParseMode = "Markdown"
				if status != "DONE" {
					keyboard := BackToMainKeyboard()
					editMsg.ReplyMarkup = &keyboard
				} else {
					// Inline keyboard can be adjusted or removed for DONE state. Let's keep a back button.
					keyboard := BackToMainKeyboard()
					editMsg.ReplyMarkup = &keyboard
				}
				sentMsg, sendErr = h.api.Send(editMsg)
			}

			// If editing failed or there's no stored message ID, send a new message
			if err != nil || sendErr != nil || msgID == 0 {
				m := tgbotapi.NewMessage(j.UserID, text)
				m.ParseMode = "Markdown"
				m.ReplyMarkup = BackToMainKeyboard()
				sentMsg, _ = h.api.Send(m)

				if sentMsg.MessageID > 0 {
					h.rdb.Set(ctx, msgKey, sentMsg.MessageID, 24*time.Hour)
				}
			}
		}
	}
}

// downloadTelegramFile copies a file mapped from the telegram-bot-api local volume.
func (h *Handlers) downloadTelegramFile(fileID, destPath string, maxBytes int64) (int64, error) {
	file, err := h.api.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return 0, err
	}

	// The Telegram Bot API server returns the absolute file path
	// where it saved the file (e.g. /var/lib/telegram-bot-api/...).
	// Since we mount this volume into the signy container, we copy it directly.
	if !strings.HasPrefix(file.FilePath, "/") {
		return 0, fmt.Errorf("local API is required but got non-local path: %s", file.FilePath)
	}

	return h.store.CopyLocalFile(file.FilePath, destPath, maxBytes)
}
