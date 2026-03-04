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
) *Handlers {
	processKey, _ := crypto.GenerateRandomKey()
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

	// Handle /start command
	if msg.IsCommand() && msg.Command() == "start" {
		h.fsm.Clear(userID)
		h.sendMainMenu(msg.Chat.ID)
		return
	}

	// Handle FSM states
	state := h.fsm.GetState(userID)
	switch state {
	case models.StateCertCreateName:
		h.handleCertName(ctx, msg)
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

	switch {
	// Main menu
	case data == CBMainNewJob:
		h.handleNewJob(ctx, chatID, cb.From.ID)
	case data == CBMainCerts:
		h.sendCertMenu(chatID)
	case data == CBMainMyJobs:
		h.handleMyJobs(ctx, chatID, cb.From.ID)
	case data == CBMainSettings:
		h.sendText(chatID, "⚙️ *Settings*\n\nNo configurable settings at this time.")
	case data == CBMainHelp:
		h.sendHelp(chatID)

	// Cert menu
	case data == CBCertAdd:
		h.startCertCreate(chatID, cb.From.ID)
	case data == CBCertDefault:
		h.handleSetDefault(ctx, chatID, cb.From.ID)
	case data == CBCertStatus:
		h.handleCheckStatus(ctx, chatID, cb.From.ID)
	case data == CBCertDelete:
		h.handleDeleteMenu(ctx, chatID, cb.From.ID)
	case data == CBCertBack || data == CBBack:
		h.fsm.Clear(cb.From.ID)
		h.sendMainMenu(chatID)

	// Cert selection
	case strings.HasPrefix(data, CBCertSelectPfx):
		setID := strings.TrimPrefix(data, CBCertSelectPfx)
		h.handleSelectDefault(ctx, chatID, cb.From.ID, setID)
	case strings.HasPrefix(data, CBCertDeletePfx):
		setID := strings.TrimPrefix(data, CBCertDeletePfx)
		h.handleDeleteConfirm(chatID, setID)
	case strings.HasPrefix(data, CBCertDelConfirm):
		setID := strings.TrimPrefix(data, CBCertDelConfirm)
		h.handleDeleteExecute(ctx, chatID, cb.From.ID, setID)
	case data == CBCertDelCancel:
		h.sendCertMenu(chatID)

	// Job flow
	case data == CBJobUseDefault:
		h.handleJobUseDefault(ctx, chatID, cb.From.ID)
	case data == CBJobChooseCert:
		h.handleJobChooseCert(ctx, chatID, cb.From.ID)
	case strings.HasPrefix(data, CBJobCertPfx):
		setID := strings.TrimPrefix(data, CBJobCertPfx)
		h.handleJobSelectCert(ctx, chatID, cb.From.ID, setID)
	case data == CBJobConfirm:
		h.handleJobConfirm(ctx, chatID, cb.From.ID)
	case data == CBJobCancel:
		h.fsm.Clear(cb.From.ID)
		h.sendText(chatID, "❌ Job cancelled.")
		h.sendMainMenu(chatID)

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
	msg := tgbotapi.NewMessage(chatID, "🪪 *Certificate Management*\n\nManage your signing certificates and provisioning profiles:")
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
• *Set Default* — Choose which cert to use by default
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
		State:  models.StateCertCreateName,
	})
	h.sendText(chatID, "➕ *Add Certificate Set*\n\nPlease send a short name for this cert set (2-32 characters):\n\n_Example: My Dev Cert_")
}

func (h *Handlers) handleCertName(ctx context.Context, msg *tgbotapi.Message) {
	name := strings.TrimSpace(msg.Text)
	if len(name) < 2 || len(name) > 32 {
		h.sendText(msg.Chat.ID, "⚠️ Name must be between 2 and 32 characters. Try again:")
		return
	}

	sess := h.fsm.Get(msg.From.ID)
	sess.CertSetName = name
	sess.State = models.StateCertCreateP12
	h.fsm.Set(msg.From.ID, sess)

	h.sendText(msg.Chat.ID, fmt.Sprintf("✅ Name: *%s*\n\n📤 Now please upload your *.p12* certificate file.", name))
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

	// Download p12 file
	fileURL, err := h.api.GetFileDirectURL(msg.Document.FileID)
	if err != nil {
		h.logger.Error("failed to get p12 file URL", "error", err)
		h.sendText(msg.Chat.ID, "❌ Failed to download P12 file. Please try again.")
		return
	}

	destPath := h.store.IncomingIPAPath(msg.From.ID, msg.Document.FileID) + ".p12"
	start := time.Now()
	_, err = h.store.StreamDownload(fileURL, destPath, h.cfg.MaxP12Bytes())
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

	// Download provision
	fileURL, err := h.api.GetFileDirectURL(msg.Document.FileID)
	if err != nil {
		h.logger.Error("failed to get provision URL", "error", err)
		h.sendText(msg.Chat.ID, "❌ Failed to download provision file. Please try again.")
		return
	}

	destPath := h.store.IncomingIPAPath(msg.From.ID, msg.Document.FileID) + ".mobileprovision"
	_, err = h.store.StreamDownload(fileURL, destPath, h.cfg.MaxProvBytes())
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
	cs, err := h.certMgr.Create(ctx, msg.From.ID, sess.CertSetName, p12Data, string(passBytes), provData)
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

	defaultID, _ := h.certMgr.GetDefaultID(msg.From.ID)
	if defaultID == cs.SetID {
		text += "\n⭐ _Set as default automatically._"
	}

	m := tgbotapi.NewMessage(msg.Chat.ID, text)
	m.ParseMode = "Markdown"
	m.ReplyMarkup = CertMenuKeyboard()
	_, _ = h.api.Send(m)
}

// === CERT DEFAULT ===

func (h *Handlers) handleSetDefault(ctx context.Context, chatID int64, userID int64) {
	sets, _ := h.certMgr.List(userID)
	if len(sets) == 0 {
		h.sendText(chatID, "📭 You have no certificate sets. Add one first!")
		return
	}

	defaultID, _ := h.certMgr.GetDefaultID(userID)
	var infos []CertSetInfo
	for _, s := range sets {
		infos = append(infos, CertSetInfo{
			SetID:     s.SetID,
			Name:      s.Name,
			IsDefault: s.SetID == defaultID,
		})
	}

	msg := tgbotapi.NewMessage(chatID, "⭐ *Set Default Certificate*\n\nSelect a certificate set to use as default:")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = CertListKeyboard(infos, CBCertSelectPfx)
	_, _ = h.api.Send(msg)
}

func (h *Handlers) handleSelectDefault(ctx context.Context, chatID int64, userID int64, setID string) {
	if err := h.certMgr.SetDefault(userID, setID); err != nil {
		h.sendText(chatID, fmt.Sprintf("❌ Failed to set default: %s", err.Error()))
		return
	}

	cs, _ := h.certMgr.Get(userID, setID)
	name := setID
	if cs != nil {
		name = cs.Name
	}

	metrics.CertSetsTotal.WithLabelValues("set_default").Inc()
	h.sendText(chatID, fmt.Sprintf("⭐ Default certificate set changed to *%s*.", name))
	h.sendCertMenu(chatID)
}

// === CERT STATUS ===

func (h *Handlers) handleCheckStatus(ctx context.Context, chatID int64, userID int64) {
	defaultID, _ := h.certMgr.GetDefaultID(userID)
	if defaultID == "" {
		h.sendText(chatID, "📭 No default certificate set configured.\n\nPlease add a cert set and set it as default.")
		return
	}

	cs, err := h.certMgr.Get(userID, defaultID)
	if err != nil {
		h.sendText(chatID, "❌ Could not read default cert set. It may have been deleted.")
		return
	}

	status, detail, _ := h.certMgr.CheckStatus(userID, defaultID)
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

	defaultID, _ := h.certMgr.GetDefaultID(userID)
	var infos []CertSetInfo
	for _, s := range sets {
		infos = append(infos, CertSetInfo{
			SetID:     s.SetID,
			Name:      s.Name,
			IsDefault: s.SetID == defaultID,
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
	defaultID, _ := h.certMgr.GetDefaultID(userID)
	hasDefault := defaultID != ""

	if !hasDefault {
		sets, _ := h.certMgr.List(userID)
		if len(sets) == 0 {
			h.sendText(chatID, "📭 You need to add a certificate set before creating a signing job.\n\nGo to *🪪 Certificates* → *➕ Add Cert Set*.")
			return
		}
	}

	h.fsm.Set(userID, &models.UserSession{
		UserID: userID,
		State:  models.StateJobSelectCert,
	})

	var text string
	if hasDefault {
		cs, _ := h.certMgr.GetDefault(userID)
		if cs != nil {
			text = fmt.Sprintf("➕ *New Signing Job*\n\n📋 Default cert: *%s* (`%s`)\n\nChoose cert to use:", cs.Name, cs.SetID)
		} else {
			text = "➕ *New Signing Job*\n\nChoose cert to use:"
		}
	} else {
		text = "➕ *New Signing Job*\n\nNo default cert set. Please choose one:"
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = JobCertChoiceKeyboard(hasDefault)
	_, _ = h.api.Send(msg)
}

func (h *Handlers) handleJobUseDefault(ctx context.Context, chatID int64, userID int64) {
	defaultID, _ := h.certMgr.GetDefaultID(userID)
	if defaultID == "" {
		h.sendText(chatID, "❌ No default cert set. Please choose one.")
		return
	}

	sess := h.fsm.Get(userID)
	sess.SelectedCertSetID = defaultID
	sess.State = models.StateJobUploadIPA

	// Check if password needed (no MASTER_KEY mode)
	if !h.cfg.HasMasterKey() {
		sess.State = models.StateJobUploadIPA
	}

	h.fsm.Set(userID, sess)
	h.sendText(chatID, "📤 Please send or forward your *.ipa* file now.")
}

func (h *Handlers) handleJobChooseCert(ctx context.Context, chatID int64, userID int64) {
	sets, _ := h.certMgr.List(userID)
	if len(sets) == 0 {
		h.sendText(chatID, "📭 No cert sets available.")
		return
	}

	defaultID, _ := h.certMgr.GetDefaultID(userID)
	var infos []CertSetInfo
	for _, s := range sets {
		infos = append(infos, CertSetInfo{
			SetID:     s.SetID,
			Name:      s.Name,
			IsDefault: s.SetID == defaultID,
		})
	}

	msg := tgbotapi.NewMessage(chatID, "📋 *Choose Certificate*\n\nSelect a cert set for this job:")
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

	h.sendText(msg.Chat.ID, "⏳ Downloading IPA... Please wait.")

	fileURL, err := h.api.GetFileDirectURL(msg.Document.FileID)
	if err != nil {
		h.logger.Error("get ipa file URL", "error", err)
		h.sendText(msg.Chat.ID, "❌ Failed to get IPA download URL. Please try again.")
		return
	}

	destPath := h.store.IncomingIPAPath(msg.From.ID, msg.Document.FileID)
	start := time.Now()
	size, err := h.store.StreamDownload(fileURL, destPath, h.cfg.MaxIPABytes())
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

	sess.State = models.StateJobConfirm
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

	text := fmt.Sprintf("📋 *Job Summary*\n\n"+
		"🪪 Certificate: *%s*\n"+
		"📦 IPA Size: *%.1f MB*\n"+
		"\n_Ready to start signing?_",
		certName, sizeMB)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = JobConfirmKeyboard()
	_, _ = h.api.Send(msg)
}

func (h *Handlers) handleJobConfirm(ctx context.Context, chatID int64, userID int64) {
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

	j, err := h.jobMgr.CreateAndEnqueue(ctx, userID, sess.SelectedCertSetID, sess.IPAPath)
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

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = BackToMainKeyboard()
	_, _ = h.api.Send(msg)
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
		baseURL := h.cfg.BaseURL
		text += fmt.Sprintf("\n\n📥 *Download Links:*\n"+
			"• [Signed IPA](%s/artifacts/%s/signed.ipa)\n"+
			"• [Manifest](%s/artifacts/%s/manifest.plist)\n"+
			"• [Sign Log](%s/artifacts/%s/sign.log)\n\n"+
			"📲 *OTA Install:*\n`itms-services://?action=download-manifest&url=%s/artifacts/%s/manifest.plist`",
			baseURL, j.JobID, baseURL, j.JobID, baseURL, j.JobID, baseURL, j.JobID)
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
	defer sub.Close()

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
				baseURL := h.cfg.BaseURL
				text += fmt.Sprintf("\n\n📲 *Install:*\n`itms-services://?action=download-manifest&url=%s/artifacts/%s/manifest.plist`",
					baseURL, jobID)
			}

			m := tgbotapi.NewMessage(j.UserID, text)
			m.ParseMode = "Markdown"
			m.ReplyMarkup = BackToMainKeyboard()
			_, _ = h.api.Send(m)
		}
	}
}
