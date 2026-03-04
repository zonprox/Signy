package bot

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Callback data constants for inline keyboard buttons.
const (
	CBMainNewJob    = "main:new_job"
	CBMainCerts     = "main:certs"
	CBMainMyJobs    = "main:my_jobs"
	CBMainSettings  = "main:settings"
	CBMainHelp      = "main:help"

	CBCertAdd       = "cert:add"
	CBCertDefault   = "cert:default"
	CBCertStatus    = "cert:status"
	CBCertDelete    = "cert:delete"
	CBCertBack      = "cert:back"

	CBCertSelectPfx = "cert:select:"
	CBCertDeletePfx = "cert:del:"
	CBCertDelConfirm = "cert:del_confirm:"
	CBCertDelCancel  = "cert:del_cancel"

	CBJobUseDefault = "job:use_default"
	CBJobChooseCert = "job:choose_cert"
	CBJobCertPfx    = "job:cert:"
	CBJobConfirm    = "job:confirm"
	CBJobCancel     = "job:cancel"

	CBJobDetailPfx  = "job:detail:"

	CBBack          = "back:main"
)

// MainMenuKeyboard returns the main menu inline keyboard.
func MainMenuKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ New Signing Job", CBMainNewJob),
			tgbotapi.NewInlineKeyboardButtonData("🪪 Certificates", CBMainCerts),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🧾 My Jobs", CBMainMyJobs),
			tgbotapi.NewInlineKeyboardButtonData("⚙ Settings", CBMainSettings),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❓ Help", CBMainHelp),
		),
	)
}

// CertMenuKeyboard returns the certificate management menu.
func CertMenuKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Add Cert Set", CBCertAdd),
			tgbotapi.NewInlineKeyboardButtonData("⭐ Set Default", CBCertDefault),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Check Status", CBCertStatus),
			tgbotapi.NewInlineKeyboardButtonData("🗑 Delete", CBCertDelete),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅ Back", CBCertBack),
		),
	)
}

// JobConfirmKeyboard returns the signing job confirmation keyboard.
func JobConfirmKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Start Signing", CBJobConfirm),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", CBJobCancel),
		),
	)
}

// JobCertChoiceKeyboard returns the cert choice keyboard for job creation.
func JobCertChoiceKeyboard(hasDefault bool) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	if hasDefault {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Use Default", CBJobUseDefault),
			tgbotapi.NewInlineKeyboardButtonData("📋 Choose Another", CBJobChooseCert),
		))
	} else {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Choose Cert Set", CBJobChooseCert),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", CBJobCancel),
	))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// CertListKeyboard creates a keyboard with cert set buttons.
func CertListKeyboard(sets []CertSetInfo, callbackPrefix string) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, s := range sets {
		label := s.Name
		if s.IsDefault {
			label += " ⭐"
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, callbackPrefix+s.SetID),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅ Back", CBCertBack),
	))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// ConfirmDeleteKeyboard creates a confirmation keyboard for deletion.
func ConfirmDeleteKeyboard(setID string) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚠️ Yes, Delete", CBCertDelConfirm+setID),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", CBCertDelCancel),
		),
	)
}

// BackToMainKeyboard returns a simple back-to-main button.
func BackToMainKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅ Main Menu", CBBack),
		),
	)
}

// CertSetInfo holds minimal info for keyboard display.
type CertSetInfo struct {
	SetID     string
	Name      string
	IsDefault bool
}
