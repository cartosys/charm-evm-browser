package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"charm-wallet-tui/config"
	"charm-wallet-tui/helpers"
	"charm-wallet-tui/styles"
	"charm-wallet-tui/views/dapps"
	"charm-wallet-tui/views/details"
	logview "charm-wallet-tui/views/log"
	"charm-wallet-tui/views/oscillator"
	"charm-wallet-tui/views/scrollbar"
	"charm-wallet-tui/views/settings"
	"charm-wallet-tui/views/terra"
	"charm-wallet-tui/views/uniswap"
	"charm-wallet-tui/views/wallets"
	"charm-wallet-tui/views/watchedtokens"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// RPCFormPopupWidth is the outer dialog box width for the RPC add/edit popup.
// RPCFormPopupInnerWidth is the huh.Form width (dialog minus border+padding).
const (
	RPCFormPopupWidth      = 64
	RPCFormPopupInnerWidth = RPCFormPopupWidth - 6
)

// SendFormPopupWidth is the outer dialog box width for the Send Transaction popup.
// SendFormPopupInnerWidth is the huh.Form width (dialog minus border+padding).
const (
	SendFormPopupWidth      = 64
	SendFormPopupInnerWidth = SendFormPopupWidth - 6
)

// -------------------- VIEW --------------------

func (m *model) renderConfirmDialog(id, prompt string, yesSelected bool, onYes, onNo func(*model) (tea.Model, tea.Cmd)) string {
	dialogBoxStyle := styles.DialogBox.Padding(1, 0)
	buttonStyle := styles.ButtonNormal.MarginTop(1)
	activeButtonStyle := styles.ButtonActive.MarginRight(2).MarginTop(1)

	question := lipgloss.NewStyle().Width(50).Align(lipgloss.Center).Render(prompt)

	var okButton, cancelButton string
	if yesSelected {
		okButton = activeButtonStyle.Render("Yes")
		cancelButton = buttonStyle.Render("No")
	} else {
		okButton = buttonStyle.MarginRight(2).Render("Yes")
		cancelButton = activeButtonStyle.MarginRight(0).Render("No")
	}

	buttons := lipgloss.JoinHorizontal(lipgloss.Top, okButton, cancelButton)
	ui := lipgloss.JoinVertical(lipgloss.Center, question, buttons)

	dialog := dialogBoxStyle.Render(ui)

	// dialogBoxStyle uses Padding(1, 0): border(1) + padding-top(1) = +2 rows,
	// border(1) + padding-left(0) = +1 col (mirrors the +2/+3 convention used
	// elsewhere for DialogBox's default Padding(1, 2), adjusted for this
	// dialog's narrower horizontal padding).
	dialogW := lipgloss.Width(dialog)
	dialogH := lipgloss.Height(dialog)
	startX := (m.w - dialogW) / 2
	startY := (m.h - dialogH) / 2
	contentLeft := startX + 1
	buttonsRowY := startY + 2 + lipgloss.Height(question)

	uiWidth := lipgloss.Width(question)
	buttonsWidth := lipgloss.Width(buttons)
	if buttonsWidth > uiWidth {
		uiWidth = buttonsWidth
	}
	leftPad := (uiWidth - buttonsWidth) / 2

	yesX1 := contentLeft + leftPad
	yesX2 := yesX1 + lipgloss.Width(okButton)
	noX1 := yesX2
	noX2 := noX1 + lipgloss.Width(cancelButton)

	m.registerRegion(id+".yes", uiRegionButton, yesX1, buttonsRowY, yesX2, buttonsRowY+1, func(m *model) (tea.Model, tea.Cmd) { return onYes(m) })
	m.registerRegion(id+".no", uiRegionButton, noX1, buttonsRowY, noX2, buttonsRowY+1, func(m *model) (tea.Model, tea.Cmd) { return onNo(m) })

	return lipgloss.Place(
		m.w, m.h,
		lipgloss.Center, lipgloss.Center,
		dialog,
	)
}

func (m *model) renderDeleteDialog() string {
	msg := helpers.FadeString("Are you sure you want to delete the account "+helpers.ShortenAddr(m.deleteDialogAddr)+"?", "#F25D94", "#EDFF82")
	return m.renderConfirmDialog("confirmDeleteWallet", msg, m.deleteDialogYesSelected,
		(*model).confirmDeleteWalletYes, (*model).confirmDeleteWalletNo)
}

func (m *model) renderRPCDeleteDialog() string {
	msg := helpers.FadeString("Are you sure you want to delete the RPC endpoint "+m.deleteRPCDialogName+"?", "#F25D94", "#EDFF82")
	return m.renderConfirmDialog("confirmDeleteRPC", msg, m.deleteRPCDialogYesSelected,
		(*model).confirmDeleteRPCYes, (*model).confirmDeleteRPCNo)
}

func (m *model) renderTokenDeleteDialog() string {
	msg := helpers.FadeString("Are you sure you want to remove the watched token "+m.deleteTokenDialogName+"?", "#F25D94", "#EDFF82")
	return m.renderConfirmDialog("confirmDeleteToken", msg, m.deleteTokenDialogYesSelected,
		(*model).confirmDeleteTokenYes, (*model).confirmDeleteTokenNo)
}

func (m *model) renderAccountListPopup() string {
	dialogBoxStyle := styles.DialogBox.Background(styles.CPanel)

	title := lipgloss.NewStyle().
		Foreground(styles.CAccent2).
		Bold(true).
		Align(lipgloss.Center).
		Width(70).
		Render("Select Account")

	accountList, popupClickableAreas, _ := wallets.RenderList(m.accounts, m.accountListSelectedIdx)

	help := lipgloss.NewStyle().
		Foreground(styles.CMuted).
		Align(lipgloss.Center).
		Width(70).
		MarginTop(1).
		Render("↑/↓: Navigate • Enter: Select • Esc: Cancel")

	ui := lipgloss.JoinVertical(lipgloss.Left, title, "", accountList, help)
	dialog := dialogBoxStyle.Render(ui)

	// Register clickable areas with popup-relative coordinates.
	const (
		dialogWidth    = 74
		renderListBaseX = 4
		renderListBaseY = 9
	)
	dialogHeight := len(m.accounts)*3 + 8
	popupStartX := (m.w - dialogWidth) / 2
	popupStartY := (m.h - dialogHeight) / 2
	contentStartX := popupStartX + 3 // border(1) + padding(2)
	contentStartY := popupStartY + 4 // border(1) + padding(1) + title(1) + blank(1)

	m.clickableAreas = nil
	for _, area := range popupClickableAreas {
		m.clickableAreas = append(m.clickableAreas, config.ClickableArea{
			X:       contentStartX + (area.X - renderListBaseX),
			Y:       contentStartY + (area.Y - renderListBaseY),
			Width:   area.Width,
			Height:  area.Height,
			Address: area.Address,
		})
	}

	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, dialog)
}

func (m *model) renderOndoPickerPopup() string {
	dialogBoxStyle := styles.DialogBox.Background(styles.CPanel).Width(70)
	content := watchedtokens.RenderOndoPicker(m.filteredOndoTokens(), m.ondoPickerFilter, m.ondoPickerIdx)
	dialog := dialogBoxStyle.Render(content)
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, dialog)
}

func (m *model) renderTxResultContent() string {
	titleStr := "Transaction Ready To Sign (EIP-4527)"
	if m.txApproveQRFrames != nil {
		if !m.txSwapStep {
			titleStr = "Step 1 of 2: Approve — Tab to switch steps"
		} else {
			titleStr = "Step 2 of 2: Swap — Tab to switch steps"
		}
	}
	title := styles.TitleStyle.Render(titleStr)

	if m.txResultPackaging {
		return title + "\n\n" + m.spin.View() + " Packaging transaction..."
	}
	if m.txResultError != "" {
		errorStyle := lipgloss.NewStyle().Foreground(styles.CFail).Bold(true)
		muteStyle := lipgloss.NewStyle().Foreground(styles.CSubtle)
		return title + "\n\n" +
			errorStyle.Render("Error: "+m.txResultError) +
			"\n\n" + muteStyle.Render("Press ESC or Enter to close")
	}

	// Animated QR section: show current frame, centred, with frame counter.
	var qrSection string
	if len(m.txQRFrames) > 0 {
		qr := m.txQRFrames[m.txQRFrameIdx]
		if len(m.txQRFrames) > 1 {
			counter := lipgloss.NewStyle().
				Foreground(styles.CMuted).
				Render(fmt.Sprintf("Frame %d / %d", m.txQRFrameIdx+1, len(m.txQRFrames)))
			qrSection = qr + counter
		} else {
			qrSection = qr
		}
	}
	qrH := lipgloss.Height(qrSection)

	// Remaining vertical space goes to the scrollable text viewport.
	// Overhead: title(1) + blank(1) + qr + blank(1) + panel borders+padding(4) + slack(1) = qrH+8
	vpH := helpers.Max(3, m.h-qrH-8)
	vp := m.txQRViewport
	vp.Height = vpH
	track := scrollbar.Track(vpH, vp.TotalLineCount(), vp.YOffset)
	vpContent := scrollbar.Decorate(vp.View(), track)

	// Viewport scroll tracking: panel is vertically centred; viewport starts
	// after title(1) + blank(1) + qrSection + blank(1) + panel top border+padding(2) = qrH+5
	totalPanelH := 1 + 1 + qrH + 1 + vpH + 4 // title+blank+qr+blank+vp+borders/padding
	panelTop := (m.h - totalPanelH) / 2
	m.txQRScroll.PanelTop = panelTop + 2 + 1 + qrH + 1 // panel top + border+padding + qr + blank
	m.txQRScroll.TrackCol = m.txQRViewport.Width + 3

	result := title + "\n\n" + qrSection + "\n" + vpContent
	if m.txCopiedMsg != "" {
		result += "\n" + lipgloss.NewStyle().Foreground(styles.CSuccess).Bold(true).Render(m.txCopiedMsg)
	}
	return result
}

func (m *model) renderTxResultPanel() string {
	contentWidth := helpers.Max(0, m.w-8)
	centeredContent := lipgloss.NewStyle().Width(contentWidth).Align(lipgloss.Center).Render(m.renderTxResultContent())
	content := styles.PanelStyle.Width(helpers.Max(0, m.w-4)).Render(centeredContent)
	return styles.AppStyle.Render(lipgloss.Place(
		m.w, m.h,
		lipgloss.Center, lipgloss.Center,
		content,
	))
}

func (m *model) globalHeader() string {
	availableWidth := helpers.Max(0, m.w-8) // Account for panel padding

	// Active Address (the one marked with ★)
	var addrDisplay string
	if m.activeAddress != "" {
		addrDisplay = lipgloss.NewStyle().
			Foreground(styles.CAccent2).
			Bold(true).
			Render("Active Address: " + helpers.FadeString(helpers.ShortenAddr(m.activeAddress), "#F25D94", "#EDFF82"))

		// Track header address position for double-click detection
		// X position: 3 (panel left border + padding) + length of "Active Address: "
		// Y position: 2 (accounting for panel top border + padding = row 1, but 0-indexed)
		m.headerAddrX = 3 + len("Active Address: ")
		m.headerAddrY = 2
		m.headerAddrWidth = len(helpers.ShortenAddr(m.activeAddress))
	} else {
		addrDisplay = lipgloss.NewStyle().
			Foreground(styles.CMuted).
			Render("Active Address: No selection")
		m.headerAddrX = 0
		m.headerAddrY = 0
		m.headerAddrWidth = 0
	}

	// RPC Status with green dot
	var statusIcon string
	var statusColor lipgloss.Color
	var statusText string

	if m.rpcURL == "" {
		statusIcon = "○"
		statusColor = styles.CFail
		statusText = "No RPC"
	} else if m.rpcConnecting {
		statusIcon = "○"
		statusColor = styles.CFail
		statusText = "Connecting..."
	} else if !m.rpcConnected {
		statusIcon = "○"
		statusColor = styles.CFail
		statusText = "Connection Failed"
	} else {
		statusIcon = "●"
		statusColor = styles.CAccent
		// Find active RPC name
		for _, r := range m.rpcURLs {
			if r.Active && r.URL == m.rpcURL {
				statusText = r.Name
				break
			}
		}
		if statusText == "" {
			statusText = "Connected"
		}
	}

	rpcDisplay := lipgloss.NewStyle().
		Foreground(statusColor).
		Bold(true).
		Render(statusIcon + " " + statusText)

	// Center title
	titleStyle := lipgloss.NewStyle().
		Foreground(styles.CAccent).
		Bold(true)
	titleText := titleStyle.Render(helpers.FadeString("domestic system", "#7EE787", "#82CFFD"))

	// Calculate widths
	addrWidth := lipgloss.Width(addrDisplay)
	rpcWidth := lipgloss.Width(rpcDisplay)
	titleWidth := lipgloss.Width(titleText)

	// Calculate spacing to center the title
	totalOtherWidth := addrWidth + rpcWidth + titleWidth

	var headerLine string
	if totalOtherWidth+4 > availableWidth {
		// Not enough space, stack vertically
		headerLine = addrDisplay + "\n" + titleText + "\n" + rpcDisplay
	} else {
		// Three-column layout: Address | Title (centered) | RPC
		// Calculate how much space for padding
		remainingSpace := availableWidth - totalOtherWidth
		leftPadding := remainingSpace / 2
		rightPadding := remainingSpace - leftPadding

		leftSpacer := strings.Repeat(" ", helpers.Max(1, leftPadding))
		rightSpacer := strings.Repeat(" ", helpers.Max(1, rightPadding))

		headerLine = addrDisplay + leftSpacer + titleText + rightSpacer + rpcDisplay
	}

	// Add separator line
	separator := lipgloss.NewStyle().
		Foreground(styles.CBorder).
		Render(strings.Repeat("─", availableWidth))

	return headerLine + "\n" + separator
}

func (m *model) renderPoolInfoPopup() string {
	dialogBoxStyle := styles.DialogBox.Width(72)

	// Title
	title := lipgloss.NewStyle().
		Foreground(styles.CAccent2).
		Bold(true).
		Align(lipgloss.Center).
		Width(68).
		Render("Pool Info")

	// Pool ID (full, FadeString)
	poolIDLine := lipgloss.NewStyle().
		Align(lipgloss.Center).
		Width(68).
		Render(helpers.FadeString(m.poolInfoID, "#7EE787", "#82CFFD"))

	// Body
	var body string
	if m.poolInfoLoading {
		body = lipgloss.NewStyle().Foreground(styles.CMuted).Render(m.spin.View() + " Fetching pool data…")
	} else if m.poolInfoErr != "" {
		body = lipgloss.NewStyle().Foreground(styles.CError).Render("Error: " + m.poolInfoErr)
	} else if m.poolInfoData != nil {
		raw, err := json.MarshalIndent(m.poolInfoData, "", "  ")
		if err != nil {
			body = lipgloss.NewStyle().Foreground(styles.CError).Render("Error marshalling data")
		} else {
			body = lipgloss.NewStyle().Foreground(styles.CText).Render(string(raw))
		}
	}

	// Eth logs loading row (shown while fetching pool key)
	var keyRow string
	if m.poolInfoKeyLoading {
		keyRow = lipgloss.NewStyle().Foreground(styles.CMuted).Render(m.spin.View() + " Loading eth logs…")
	} else if m.poolInfoKeyErr != "" {
		keyRow = lipgloss.NewStyle().Foreground(styles.CWarn).Render("⚠ " + m.poolInfoKeyErr)
	}

	// "OK" button centred in 68 cols. ButtonPrimary has Padding(0,3) → 8 cols wide.
	// Centre offset: (68-8)/2 = 30.
	okBtnStyle := styles.ButtonPrimary
	if m.hoveredRegionID == "poolInfo.ok" {
		okBtnStyle = styles.ButtonActive
	}
	okButton := okBtnStyle.MarginTop(1).Render("OK")
	btnRowCentered := lipgloss.NewStyle().Align(lipgloss.Center).Width(68).Render(okButton)

	// "Pool ID Copied" confirmation shown above OK button when copy succeeds.
	var copiedRow string
	if m.poolInfoCopied {
		copiedRow = lipgloss.NewStyle().
			Foreground(styles.CAccent).
			Align(lipgloss.Center).
			Width(68).
			Render("✓ Pool ID Copied")
	}

	rows := []string{title, poolIDLine, "", body}
	if keyRow != "" {
		rows = append(rows, keyRow)
	}
	if copiedRow != "" {
		rows = append(rows, copiedRow)
	}
	rows = append(rows, btnRowCentered)
	ui := lipgloss.JoinVertical(lipgloss.Center, rows...)
	dialog := dialogBoxStyle.Render(ui)

	// Track click areas. Dialog content starts 3 cols from left edge (border+padding).
	// Pool ID line is at row 3 from dialog top (border+padding+title).
	// OK button is centred: offset (68-8)/2 = 30 from content left.
	dialogH := lipgloss.Height(dialog)
	dialogW := lipgloss.Width(dialog)
	dialogStartX := (m.w - dialogW) / 2
	dialogStartY := (m.h - dialogH) / 2
	contentLeft := dialogStartX + 3
	idLineY := dialogStartY + 3
	idLineX1 := contentLeft
	idLineX2 := contentLeft + 68
	btnRowY := dialogStartY + dialogH - 3 // one row above bottom padding+border
	btnX1 := contentLeft + 30
	btnX2 := contentLeft + 30 + 8

	m.registerRegion("poolInfo.idLine", uiRegionButton, idLineX1, idLineY, idLineX2, idLineY+1, func(m *model) (tea.Model, tea.Cmd) {
		return m, copyPoolIDToClipboard(m.poolInfoID)
	})
	m.registerRegion("poolInfo.ok", uiRegionButton, btnX1, btnRowY, btnX2, btnRowY+1, func(m *model) (tea.Model, tea.Cmd) {
		m.activeDialog = dialogNone
		m.poolInfoData = nil
		m.poolInfoErr = ""
		m.poolInfoID = ""
		m.poolInfoCopied = false
		m.poolInfoKeyLoading = false
		m.poolInfoKeyErr = ""
		return m, nil
	})

	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, dialog)
}

// renderActiveOverlay returns a full-screen overlay string when a dialog is open,
// or "" when no overlay applies. Checked before any page content is rendered.
func (m *model) renderActiveOverlay() string {
	if m.activePage == config.PageSettings && (m.settingsMode == "add" || m.settingsMode == "edit") && m.form != nil {
		return m.renderRPCFormPopup()
	}
	if m.activePage == config.PageWatchedTokens && (m.tokenFormMode == "add" || m.tokenFormMode == "edit") && m.tokenForm != nil {
		return m.renderTokenFormPopup()
	}

	switch m.activeDialog {
	case dialogTxResult:
		return m.renderTxResultPanel()
	case dialogScanTx:
		return m.renderScanTxPanel()
	case dialogPasteSignedTx:
		return m.renderPasteSignedTxPanel()
	case dialogDeleteWallet:
		return m.renderDeleteDialog()
	case dialogDeleteRPC:
		return m.renderRPCDeleteDialog()
	case dialogDeleteToken:
		return m.renderTokenDeleteDialog()
	case dialogOndoPicker:
		return m.renderOndoPickerPopup()
	case dialogAccountList:
		return m.renderAccountListPopup()
	case dialogPoolInfo:
		return m.renderPoolInfoPopup()
	case dialogTerraClaim:
		popup, geo := terra.RenderClaimPopup(m.w, m.h, m.terraNullMsgInput.View(), m.terraNullMsgError, m.terraNullFormFocused)
		m.registerRegion("terraClaim.submit", uiRegionButton, geo.ButtonX1, geo.ButtonY, geo.ButtonX2, geo.ButtonY+1,
			func(m *model) (tea.Model, tea.Cmd) { return m.submitTerraClaim() })
		m.registerRegion("terraClaim.input", uiRegionInput, geo.InputX1, geo.InputY, geo.InputX2, geo.InputY+1,
			func(m *model) (tea.Model, tea.Cmd) {
				if m.terraNullFormFocused != 0 {
					m.terraNullFormFocused = 0
					return m, m.terraNullMsgInput.Focus()
				}
				return m, nil
			})
		return popup
	case dialogEditWallet:
		return m.renderEditWalletDialog()
	case dialogAddWallet:
		return m.renderAddWalletDialog()
	case dialogSendTx:
		return m.renderSendTxPopup()
	}
	return ""
}

func (m *model) renderSendTxPopup() string {
	title := lipgloss.NewStyle().
		Foreground(styles.CAccent2).
		Bold(true).
		Align(lipgloss.Center).
		Width(SendFormPopupWidth - 8).
		Render("Send Transaction")

	formView := m.sendForm.View()

	btnStyle := styles.ButtonNormal
	if m.hoveredRegionID == "sendPopup.submit" || m.sendFormButtonFocused {
		btnStyle = styles.ButtonActive
	}
	submitBtn := btnStyle.Render("Submit")
	btnRow := lipgloss.NewStyle().
		Width(SendFormPopupWidth - 8).
		Align(lipgloss.Center).
		Render(submitBtn)

	hints := lipgloss.NewStyle().Foreground(styles.CMuted).Render(
		styles.HotkeyStyle.Render("Tab") + " next   " +
			styles.HotkeyStyle.Render("Enter") + " continue   " +
			styles.HotkeyStyle.Render("Esc") + " cancel",
	)

	var errLine string
	if m.sendFormError != "" && time.Since(m.sendFormErrTime) < 3*time.Second {
		errLine = "\n" + lipgloss.NewStyle().Foreground(styles.CWarn).Bold(true).
			Width(SendFormPopupWidth - 8).Align(lipgloss.Center).Render(m.sendFormError)
	}

	ui := lipgloss.JoinVertical(lipgloss.Left, title, "", formView, "", btnRow, errLine, "", hints)
	dialog := styles.DialogBox.Padding(1, 2).Render(ui)

	dialogW := lipgloss.Width(dialog)
	dialogH := lipgloss.Height(dialog)
	dialogStartX := (m.w - dialogW) / 2
	dialogStartY := (m.h - dialogH) / 2
	contentLeft := dialogStartX + 3

	// Rows above the button row inside ui: title(1) + blank(1) + form + blank(1).
	btnRowOffset := lipgloss.Height(title) + 1 + lipgloss.Height(formView) + 1
	btnRowY := dialogStartY + 2 + btnRowOffset

	rowWidth := SendFormPopupWidth - 8
	btnWidth := lipgloss.Width(submitBtn)
	btnX1 := contentLeft + (rowWidth-btnWidth)/2
	btnX2 := btnX1 + btnWidth
	m.registerRegion("sendPopup.submit", uiRegionButton, btnX1, btnRowY, btnX2, btnRowY+1, func(m *model) (tea.Model, tea.Cmd) {
		return m.trySubmitSendForm()
	})

	fieldsTop := dialogStartY + 2 + lipgloss.Height(title) + 1
	m.registerHuhFieldRegions("sendPopup", fieldsTop, contentLeft, m.sendForm, m.sendFormFields)

	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, dialog)
}

// registerHuhFieldRegions registers one click-to-focus region per field of a
// huh.Form, stacked top-to-bottom starting at (top, left) — matching the
// vertical layout huh.Group renders fields in (each field's own View(),
// separated by the theme's FieldSeparator). fields must be in the same order
// the form's group was built with. Used for the two-field forms (Send,
// RPC add/edit); single-field forms have nothing to click-to-focus between.
func (m *model) registerHuhFieldRegions(idPrefix string, top, left int, form *huh.Form, fields []huh.Field) {
	if form == nil || len(fields) < 2 {
		return
	}
	gap := lipgloss.Height(huh.ThemeCatppuccin().FieldSeparator.Render())
	y := top
	for i, f := range fields {
		fv := f.View()
		h := lipgloss.Height(fv)
		idx := i
		m.registerRegion(fmt.Sprintf("%s.field%d", idPrefix, idx), uiRegionInput, left, y, left+lipgloss.Width(fv), y+h,
			func(m *model) (tea.Model, tea.Cmd) { return m, focusHuhField(form, fields, idx) })
		y += h + gap
	}
}

// registerWalletFormInputRegions registers click-to-focus regions for the
// address/nickname fields shared by the Add and Edit Wallet dialogs, using
// the same title/inputView layout each renderer already builds.
func (m *model) registerWalletFormInputRegions(dialog string, title string) {
	dialogW := lipgloss.Width(dialog)
	dialogH := lipgloss.Height(dialog)
	dialogStartX := (m.w - dialogW) / 2
	dialogStartY := (m.h - dialogH) / 2
	contentLeft := dialogStartX + 3
	contentTop := dialogStartY + 2

	addrView := m.input.View()
	nickView := m.nicknameInput.View()
	addrY := contentTop + lipgloss.Height(title) + 1
	nickY := addrY + lipgloss.Height(addrView) + 1

	m.registerRegion("walletForm.address", uiRegionInput, contentLeft, addrY, contentLeft+lipgloss.Width(addrView), addrY+1,
		func(m *model) (tea.Model, tea.Cmd) { m.focusWalletFormField(0); return m, nil })
	m.registerRegion("walletForm.nickname", uiRegionInput, contentLeft, nickY, contentLeft+lipgloss.Width(nickView), nickY+1,
		func(m *model) (tea.Model, tea.Cmd) { m.focusWalletFormField(1); return m, nil })
}

func (m *model) renderAddWalletDialog() string {
	title := lipgloss.NewStyle().
		Foreground(styles.CAccent2).
		Bold(true).
		Align(lipgloss.Center).
		Width(54).
		Render("Add Account")

	inputView := m.input.View() + "\n\n" + m.nicknameInput.View()

	if m.ensLookupActive {
		inputView += "\n" + m.spin.View() + " ENS lookup…"
	}

	hints := lipgloss.NewStyle().Foreground(styles.CMuted).Render(
		styles.HotkeyStyle.Render("Tab") + " next   " +
			styles.HotkeyStyle.Render("Enter") + " save   " +
			styles.HotkeyStyle.Render("Esc") + " cancel   " +
			styles.HotkeyStyle.Render("Ctrl+v") + " paste",
	)

	var errLine string
	if m.addError != "" && time.Since(m.addErrTime) < 3*time.Second {
		errLine = "\n" + lipgloss.NewStyle().Foreground(styles.CWarn).Bold(true).Render(m.addError)
	}

	ui := lipgloss.JoinVertical(lipgloss.Left, title, "", inputView, "", hints+errLine)

	dialog := styles.DialogBox.Padding(1, 2).Render(ui)
	m.registerWalletFormInputRegions(dialog, title)

	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, dialog)
}

func (m *model) renderEditWalletDialog() string {
	title := lipgloss.NewStyle().
		Foreground(styles.CAccent2).
		Bold(true).
		Align(lipgloss.Center).
		Width(54).
		Render("Edit Account")

	inputView := m.input.View() + "\n\n" + m.nicknameInput.View()

	if m.ensLookupActive {
		inputView += "\n" + m.spin.View() + " ENS lookup…"
	}

	hints := lipgloss.NewStyle().Foreground(styles.CMuted).Render(
		styles.HotkeyStyle.Render("Tab") + " next   " +
			styles.HotkeyStyle.Render("Enter") + " save   " +
			styles.HotkeyStyle.Render("Esc") + " cancel   " +
			styles.HotkeyStyle.Render("Ctrl+v") + " paste",
	)

	var errLine string
	if m.addError != "" && time.Since(m.addErrTime) < 3*time.Second {
		errLine = "\n" + lipgloss.NewStyle().Foreground(styles.CWarn).Bold(true).Render(m.addError)
	}

	ui := lipgloss.JoinVertical(lipgloss.Left, title, "", inputView, "", hints+errLine)

	dialog := styles.DialogBox.Padding(1, 2).Render(ui)
	m.registerWalletFormInputRegions(dialog, title)

	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, dialog)
}

func (m *model) renderRPCFormPopup() string {
	var titleText string
	if m.settingsMode == "edit" {
		titleText = "Edit RPC Endpoint"
	} else {
		titleText = "Add RPC Endpoint"
	}

	title := lipgloss.NewStyle().
		Foreground(styles.CAccent2).
		Bold(true).
		Align(lipgloss.Center).
		Width(RPCFormPopupWidth - 8).
		Render(titleText)

	formView := m.form.View()

	btnStyle := styles.ButtonNormal
	if m.hoveredRegionID == "rpcForm.save" || m.formButtonFocused {
		btnStyle = styles.ButtonActive
	}
	saveBtn := btnStyle.Render("Save")
	btnRow := lipgloss.NewStyle().
		Width(RPCFormPopupWidth - 8).
		Align(lipgloss.Center).
		Render(saveBtn)

	hints := lipgloss.NewStyle().Foreground(styles.CMuted).Render(
		styles.HotkeyStyle.Render("Tab") + " next   " +
			styles.HotkeyStyle.Render("Enter") + " save   " +
			styles.HotkeyStyle.Render("Esc") + " cancel",
	)

	ui := lipgloss.JoinVertical(lipgloss.Left, title, "", formView, "", btnRow, "", hints)
	dialog := styles.DialogBox.Padding(1, 2).Render(ui)

	dialogW := lipgloss.Width(dialog)
	dialogH := lipgloss.Height(dialog)
	dialogStartX := (m.w - dialogW) / 2
	dialogStartY := (m.h - dialogH) / 2
	contentLeft := dialogStartX + 3
	fieldsTop := dialogStartY + 2 + lipgloss.Height(title) + 1
	m.registerHuhFieldRegions("rpcForm", fieldsTop, contentLeft, m.form, m.formFields)

	btnRowY := fieldsTop + lipgloss.Height(formView) + 1
	rowWidth := RPCFormPopupWidth - 8
	btnWidth := lipgloss.Width(saveBtn)
	btnX1 := contentLeft + (rowWidth-btnWidth)/2
	btnX2 := btnX1 + btnWidth
	m.registerRegion("rpcForm.save", uiRegionButton, btnX1, btnRowY, btnX2, btnRowY+1, func(m *model) (tea.Model, tea.Cmd) {
		return m.submitRPCForm()
	})

	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, dialog)
}

func (m *model) renderTokenFormPopup() string {
	var titleText string
	if m.tokenFormMode == "edit" {
		titleText = "Edit Watched Token"
	} else {
		titleText = "Add Watched Token"
	}

	title := lipgloss.NewStyle().
		Foreground(styles.CAccent2).
		Bold(true).
		Align(lipgloss.Center).
		Width(RPCFormPopupWidth - 8).
		Render(titleText)

	formView := m.tokenForm.View()
	if m.tokenLookupActive {
		formView += "\n\n" + m.spin.View() + " Looking up token on-chain…"
	}

	btnStyle := styles.ButtonNormal
	if m.hoveredRegionID == "tokenForm.save" || m.tokenFormButtonFocused {
		btnStyle = styles.ButtonActive
	}
	saveBtn := btnStyle.Render("Save")
	btnRow := lipgloss.NewStyle().
		Width(RPCFormPopupWidth - 8).
		Align(lipgloss.Center).
		Render(saveBtn)

	hints := lipgloss.NewStyle().Foreground(styles.CMuted).Render(
		styles.HotkeyStyle.Render("Tab") + " next   " +
			styles.HotkeyStyle.Render("Enter") + " save   " +
			styles.HotkeyStyle.Render("Esc") + " cancel",
	)

	var errLine string
	if m.tokenFormError != "" {
		errLine = "\n" + lipgloss.NewStyle().Foreground(styles.CWarn).Bold(true).
			Width(RPCFormPopupWidth - 8).Align(lipgloss.Center).Render(m.tokenFormError)
	}

	ui := lipgloss.JoinVertical(lipgloss.Left, title, "", formView, "", btnRow, errLine, "", hints)
	dialog := styles.DialogBox.Padding(1, 2).Render(ui)

	dialogW := lipgloss.Width(dialog)
	dialogH := lipgloss.Height(dialog)
	dialogStartX := (m.w - dialogW) / 2
	dialogStartY := (m.h - dialogH) / 2
	contentLeft := dialogStartX + 3
	fieldsTop := dialogStartY + 2 + lipgloss.Height(title) + 1
	m.registerHuhFieldRegions("tokenForm", fieldsTop, contentLeft, m.tokenForm, m.tokenFormFields)

	btnRowY := fieldsTop + lipgloss.Height(formView) + 1
	rowWidth := RPCFormPopupWidth - 8
	btnWidth := lipgloss.Width(saveBtn)
	btnX1 := contentLeft + (rowWidth-btnWidth)/2
	btnX2 := btnX1 + btnWidth
	m.registerRegion("tokenForm.save", uiRegionButton, btnX1, btnRowY, btnX2, btnRowY+1, func(m *model) (tea.Model, tea.Cmd) {
		return m.submitTokenForm()
	})

	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, dialog)
}

func (m *model) View() string {
	m.clickableAreas = nil
	m.uiRegions = nil
	globalHdr := m.globalHeader()
	headerPanel := styles.PanelStyle.Width(m.contentW).Render(globalHdr)

	if overlay := m.renderActiveOverlay(); overlay != "" {
		return overlay
	}

	pageContent, nav := m.renderPage(headerPanel)

	var logPanel string
	if m.logEnabled {
		usedHeight := lipgloss.Height(headerPanel) + lipgloss.Height(pageContent) + lipgloss.Height(nav)
		viewportHeight := helpers.Max(3, m.h-usedHeight-4)
		m.logViewport.Height = viewportHeight
		logFocused := m.poolEventMonitorActive && !m.uniswapShowingLiquidity &&
			m.activePage == config.PageUniswap && m.focusedPanel == focusedPanelLog
		logPanel = logview.Render(m.w, viewportHeight, m.logReady, m.logSpinner.View(), m.logViewport, logFocused)
		// log border top = m.h - height(logPanel); +3 for top border(1)+title(1)+blank(1)
		m.logScroll.PanelTop = (m.h - lipgloss.Height(logPanel)) + 3
		m.logScroll.TrackCol = m.logViewport.Width + 3
		content := lipgloss.JoinVertical(lipgloss.Left, headerPanel, pageContent, nav, logPanel)
		return styles.AppStyle.Render(content)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, headerPanel, pageContent, nav)
	return styles.AppStyle.Render(content)
}

func (m *model) renderPage(headerPanel string) (pageContent, nav string) {
	switch m.activePage {
	case config.PageHome:
		return styles.PanelStyle.Width(m.contentW).Render("Home view not implemented"), ""

	case config.PageWallets:
		return m.renderWalletsPage(headerPanel)

	case config.PageDappBrowser:
		c := dapps.Render(m.w-2, m.dapps, m.selectedDappIdx, m.chainID())
		return styles.PanelStyle.Width(m.contentW).Render(c), dapps.Nav(m.w-2, m.txIndexerActive)

	case config.PageDetails:
		c := details.Render(m.details, m.accounts, m.loading, m.copiedMsg, m.spin.View(), m.chainID())
		return styles.PanelStyle.Width(m.contentW).Render(c), details.Nav(m.w-2, m.txIndexerActive)

	case config.PageSettings:
		c := settings.Render(m.rpcURLs, m.selectedRPCIdx)
		return styles.PanelStyle.Width(m.contentW).Render(c), settings.Nav(m.w-2, m.settingsMode, m.txIndexerActive)

	case config.PageUniswap:
		return m.renderUniswapPage(headerPanel)

	case config.PageTerraNullius:
		return m.renderTerraPage(headerPanel)

	case config.PageWatchedTokens:
		return m.renderWatchedTokensPage(headerPanel)

	case config.PageOscillator:
		return m.renderOscillatorPage(headerPanel)
	}
	return "", ""
}

func (m *model) renderOscillatorPage(headerPanel string) (pageContent, nav string) {
	c, _ := oscillator.Render(m.w-2, m.h-8, m.oscillatorBackfillActive, m.oscillatorDays, m.oscillatorSeries, m.oscillatorSeriesErr)
	navStr := oscillator.Nav(m.w-2, m.oscillatorBackfillActive)
	return styles.PanelStyle.Width(m.contentW).Render(c), navStr
}

func (m *model) renderWatchedTokensPage(headerPanel string) (pageContent, nav string) {
	sorted := sortedWatchedTokens(m.tokenWatchForActiveChain(), m.details)
	content := watchedtokens.Render(sorted, m.details, m.selectedTokenIdx)
	m.tokenListViewport.SetContent(content)

	vpH := helpers.Max(1, m.h/2-4)
	m.tokenListViewport.Height = vpH

	track := scrollbar.Track(vpH, m.tokenListViewport.TotalLineCount(), m.tokenListViewport.YOffset)
	vpContent := scrollbar.Decorate(m.tokenListViewport.View(), track)

	c := styles.PanelStyle.Width(m.contentW).Render(vpContent)
	m.tokenListScroll.PanelTop = lipgloss.Height(headerPanel) + 2
	m.tokenListScroll.TrackCol = m.tokenListViewport.Width + 3

	return c, watchedtokens.Nav(m.w-2, m.tokenFormMode, m.txIndexerActive)
}

func (m *model) renderWalletsPage(headerPanel string) (pageContent, nav string) {
	walletsContent, walletsClickableAreas := wallets.Render(m.accounts, m.selectedWallet, m.addError)
	for _, area := range walletsClickableAreas {
		m.clickableAreas = append(m.clickableAreas, config.ClickableArea{
			X: area.X, Y: area.Y + 1, Width: area.Width, Height: area.Height, Address: area.Address,
		})
	}

	nav = wallets.Nav(m.w-2, m.txIndexerActive)

	if !m.detailsInWallets || len(m.accounts) == 0 {
		return styles.PanelStyle.Width(m.contentW).Render(walletsContent), nav
	}

	detailsContent := details.Render(m.details, m.accounts, m.loading, m.copiedMsg, m.spin.View(), m.chainID())

	var detailsBaseH int
	if m.details.EthWei != nil && m.details.EthWei.Cmp(big.NewInt(0)) > 0 {
		detailsBaseH = lipgloss.Height(detailsContent)
		var btnStyle lipgloss.Style
		if m.sendButtonFocused || m.hoveredRegionID == "wallets.sendButton" {
			btnStyle = styles.ButtonActive.MarginTop(2)
		} else {
			btnStyle = styles.ButtonNormal.MarginTop(2)
		}
		detailsContent += "\n\n" + btnStyle.Render("Send")
		hint := "Press Tab to select"
		if m.sendButtonFocused {
			hint = "Press Enter to send"
		}
		detailsContent += "\n" + lipgloss.NewStyle().Foreground(styles.CSubtle).MarginTop(1).Render(hint)
	}

	listWidth := helpers.Max(0, (m.w*4)/10-2)
	detailsWidth := helpers.Max(0, (m.w*6)/10-2)
	leftPanel := styles.PanelStyle.Width(listWidth).Render(walletsContent)
	leftPanelHeight := lipgloss.Height(leftPanel)

	if detailsBaseH > 0 {
		sendBtnX := listWidth + 5
		sendBtnY := lipgloss.Height(headerPanel) + 2 + detailsBaseH + 3
		sendBtnW := 10
		m.registerRegion("wallets.sendButton", uiRegionButton, sendBtnX, sendBtnY, sendBtnX+sendBtnW, sendBtnY+1, func(m *model) (tea.Model, tea.Cmd) {
			m.createSendForm()
			m.activeDialog = dialogSendTx
			m.sendButtonFocused = false
			return m, cmdEnableMouseCellMotion()
		})
	}

	rightPanel := styles.PanelStyle.Width(detailsWidth+1).Height(leftPanelHeight-2).Render(detailsContent)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel), nav
}

func (m *model) renderUniswapPage(headerPanel string) (pageContent, nav string) {
	tokens := m.buildTokenList()
	navStr := uniswap.Nav(m.w-2, m.poolEventMonitorActive, m.uniswapShowingLiquidity, m.v4BlockScanActive)

	if m.uniswapShowingSelector {
		c := uniswap.RenderTokenSelector(m.w, m.h-8, tokens, m.uniswapSelectorIdx, m.uniswapSelectorFor == 0)
		return c, navStr
	}
	if m.uniswapShowingLiquidity {
		c := uniswap.RenderLiquidity(m.w-2, m.h-8, m.liquidityPositions, m.liquidityLoading,
			m.liquidityFocusedIdx, m.liquidityErr, m.spin.View())
		return styles.PanelStyle.Width(m.contentW).Render(c), navStr
	}
	if m.poolEventMonitorActive {
		// PanelStyle adds 4 vertical lines; RenderV4Events overhead is 4 — so m.h/2-4 yields half-height.
		v4View := uniswap.RenderV4Events(m.w-2, helpers.Max(1, m.h/2-4), m.v4EventsViewport)
		borderColor := styles.CBorder
		if m.focusedPanel == focusedPanelV4Events {
			borderColor = styles.CAccent
		}
		c := styles.PanelStyle.BorderForeground(borderColor).Width(m.contentW).Render(v4View)
		m.v4Scroll.PanelTop = lipgloss.Height(headerPanel) + 4
		m.v4Scroll.TrackCol = m.v4EventsViewport.Width + 3
		return c, navStr
	}
	c, geo := uniswap.Render(m.w-2, m.h-8, tokens,
		m.uniswapFromTokenIdx, m.uniswapToTokenIdx,
		m.uniswapFromAmount, m.uniswapToAmount,
		m.uniswapFocusedField, m.uniswapEstimating, m.uniswapResolvingPair,
		m.uniswapPriceImpactWarn, m.uniswapHookWarn)

	// Content top-left within pageContent = PanelStyle's border(1)+padding(1,2).
	contentLeft := 3
	contentTop := lipgloss.Height(headerPanel) + 2

	m.registerRegion("uniswap.fromBox", uiRegionInput,
		contentLeft+geo.FromX1, contentTop+geo.FromY,
		contentLeft+geo.FromX2, contentTop+geo.FromY+geo.FromH,
		func(m *model) (tea.Model, tea.Cmd) { return m, m.focusUniswapField(0) })
	m.registerRegion("uniswap.toBox", uiRegionInput,
		contentLeft+geo.ToX1, contentTop+geo.ToY,
		contentLeft+geo.ToX2, contentTop+geo.ToY+geo.ToH,
		func(m *model) (tea.Model, tea.Cmd) { return m, m.focusUniswapField(1) })
	m.registerRegion("uniswap.swapButton", uiRegionButton,
		contentLeft+geo.SwapX1, contentTop+geo.SwapY,
		contentLeft+geo.SwapX2, contentTop+geo.SwapY+geo.SwapH,
		func(m *model) (tea.Model, tea.Cmd) {
			m.uniswapFocusedField = 2
			return m.executeUniswapSwap()
		})

	return styles.PanelStyle.Width(m.contentW).Render(c), navStr
}

func (m *model) renderTerraPage(headerPanel string) (pageContent, nav string) {
	var terraNullDesc string
	for _, d := range m.dapps {
		if d.Name == "Terra Nullius" {
			terraNullDesc = d.Description
			break
		}
	}
	c, geo := terra.Render(m.w-2, m.h-8, m.terraNullFocusedField, terraNullDesc,
		m.terraNullClaimsCount, m.terraNullClaimsLoading,
		m.terraNullClaimInput, m.terraNullClaimQuerying,
		m.terraNullLastQueriedIdx, m.terraNullClaimResult, m.terraNullClaimResultErr)

	contentLeft := 3
	contentTop := lipgloss.Height(headerPanel) + 2

	m.registerRegion("terra.claimsBox", uiRegionInput,
		contentLeft+geo.ClaimsX1, contentTop+geo.ClaimsY,
		contentLeft+geo.ClaimsX2, contentTop+geo.ClaimsY+geo.ClaimsH,
		func(m *model) (tea.Model, tea.Cmd) {
			m.terraNullFocusedField = 1
			return m, nil
		})
	m.registerRegion("terra.claimBox", uiRegionButton,
		contentLeft+geo.ClaimX1, contentTop+geo.ClaimY,
		contentLeft+geo.ClaimX2, contentTop+geo.ClaimY+geo.ClaimH,
		func(m *model) (tea.Model, tea.Cmd) {
			m.terraNullFocusedField = 2
			return m.openTerraClaimPopup()
		})

	return styles.PanelStyle.Width(m.contentW).Render(c), terra.Nav(m.w-2, m.txIndexerActive)
}


