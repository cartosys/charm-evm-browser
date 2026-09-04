package main

import (
	"fmt"
	"strings"
	"time"

	"charm-wallet-tui/config"
	"charm-wallet-tui/helpers"
	"charm-wallet-tui/indexer"
	"charm-wallet-tui/rpc"
	"charm-wallet-tui/views/uniswap"

	"charm-wallet-tui/anim"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ethereum/go-ethereum/common"
)

// -------------------- TEMP FORM STORAGE --------------------
// Package-level vars avoid pointer-to-copy bugs when binding huh form fields.
var (
	tempRPCFormName   string
	tempRPCFormURL    string
	tempSendToAddr    string
	tempSendAmount    string
	tempTokenFormAddr string
)

// -------------------- UPDATE --------------------

// Update is the tea.Model entry point. It delegates to updateInner and then
// applies the mouse-mode toggle (Phase C hover) on top of every code path,
// so no early return inside updateInner has to remember to do it itself.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.updateInner(msg)
	mm, ok := updated.(*model)
	if !ok {
		return updated, cmd
	}
	wantAllMotion := enableHoverAllMotion && !mm.textInputActive()
	if wantAllMotion == mm.mouseAllMotionActive {
		return mm, cmd
	}
	mm.mouseAllMotionActive = wantAllMotion
	var modeCmd tea.Cmd
	if wantAllMotion {
		modeCmd = cmdEnableMouseAllMotion()
	} else {
		modeCmd = cmdEnableMouseCellMotion()
	}
	return mm, tea.Batch(cmd, modeCmd)
}

func (m *model) updateInner(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Generic clickable/hoverable region dispatch: must run before the
	// text-input mouse-drop guard below, since several dialogs (e.g. the Send
	// Transaction popup) make textInputActive() true and would otherwise
	// swallow clicks on their own buttons before they're ever seen. A click
	// landing on a registered region always wins regardless of focus state;
	// anything that doesn't hit a region falls through unhandled.
	if mouseMsg, ok := msg.(tea.MouseMsg); ok {
		if updated, cmd, handled := m.dispatchRegionClick(mouseMsg); handled {
			return updated, cmd
		}
	}

	// Drop ALL mouse events when a text field is capturing input.
	// In all-motion mode the terminal fires an escape sequence on every cursor
	// move (e.g. "\x1b[<35;238;1M"). Bubble Tea normally classifies these as
	// tea.MouseMsg, but on some Linux terminal emulators they arrive before the
	// parser recognises them and come through as tea.KeyMsg containing the raw
	// coordinate bytes — which the huh textinput then faithfully types into the
	// field. Dropping everything at this level is the only reliable fix.
	if _, ok := msg.(tea.MouseMsg); ok && m.textInputActive() {
		return m, nil
	}

	// Mouse motion consumed next — prevents hover/drag sequences from reaching
	// any non-text handler while all-motion mode is active.
	if mm, ok := msg.(tea.MouseMsg); ok && mm.Type == tea.MouseMotion {
		return m.handleMouseMotion(mm)
	}

	if updated, cmd, handled := m.handleWebcamMsg(msg); handled {
		return updated, cmd
	}

	// Spinner ticks must be handled before any dialog-specific early return;
	// those handlers don't forward anim.StepMsg to handleSpinnerTick, so the
	// tick chain silently dies whenever a form or dialog is open.
	if stepMsg, ok := msg.(anim.StepMsg); ok {
		return m.handleSpinnerTick(stepMsg)
	}

	if m.activeDialog == dialogPasteSignedTx {
		return m.handlePasteSignedTxMsg(msg)
	}

	if m.activePage == config.PageWallets && m.activeDialog == dialogSendTx && m.sendForm != nil {
		return m.handleSendFormMsg(msg)
	}
	if m.activePage == config.PageHome {
		return m, m.navigateTo(config.PageWallets)
	}
	if m.activePage == config.PageTerraNullius && m.activeDialog == dialogTerraClaim {
		return m.handleTerraClaimPopupMsg(msg)
	}
	if m.activePage == config.PageSettings && (m.settingsMode == "add" || m.settingsMode == "edit") && m.form != nil {
		return m.handleSettingsFormMsg(msg)
	}
	if m.activePage == config.PageWatchedTokens && (m.tokenFormMode == "add" || m.tokenFormMode == "edit") && m.tokenForm != nil && !m.tokenLookupActive {
		return m.handleWatchedTokensFormMsg(msg)
	}

	switch msg := msg.(type) {
	case logInitMsg:
		return m.handleLogInit()
	case rpcConnectedMsg:
		return m.handleRPCConnected(msg)
	case tokenMetadataMsg:
		return m.handleTokenMetadataMsg(msg)
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)
	case detailsLoadedMsg:
		return m.handleDetailsLoaded(msg)
	case packageTransactionMsg:
		return m.handlePackageTransaction(msg)
	case txQRAnimTickMsg:
		return m.handleQRAnimTick()
	case terraNullClaimsCountMsg:
		return m.handleTerraClaimsCount(msg)
	case terraNullClaimQueryMsg:
		return m.handleTerraClaimQuery(msg)
	case poolEventLineMsg:
		return m.handlePoolEventLine(msg)
	case poolMonitorEventMsg:
		return m.handlePoolMonitorEvent(msg)
	case v4PoolTableMsg:
		return m.handleV4PoolTable(msg)
	case poolEventMonitorStoppedMsg:
		return m.handlePoolMonitorStopped()
	case v4BlockScanLineMsg:
		return m.handleV4BlockScanLine(msg)
	case v4BlockScanDoneMsg:
		return m.handleV4BlockScanDone()
	case indexedEventMsg:
		return m.handleIndexedEvent(msg)
	case indexerStoppedMsg:
		return m.handleIndexerStopped()
	case erc20TokenIndexedMsg:
		return m, nil
	case v4PoolEventMsg:
		return m.handleV4PoolEvent(msg)
	case v4PoolIndexerStoppedMsg:
		return m, nil
	case indexerProgressMsg:
		return m.handleIndexerProgress(msg)
	case recentEventsMsg:
		return m.handleRecentEvents(msg)
	case poolInfoResultMsg:
		return m.handlePoolInfoResult(msg)
	case poolKeyResultMsg:
		return m.handlePoolKeyResult(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case poolIDCopiedMsg:
		m.poolInfoCopied = true
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return struct{ clearPoolIDCopied bool }{true} })
	case struct{ clearPoolIDCopied bool }:
		m.poolInfoCopied = false
		return m, nil
	case clipboardCopiedMsg:
		m.copiedMsg = "✓ Copied address to clipboard"
		m.copiedMsgTime = time.Now()
		return m, clearClipboardMsg()
	case txJsonCopiedMsg:
		m.txCopiedMsg = "✓ Copied to clipboard"
		m.txCopiedMsgTime = time.Now()
		return m, clearClipboardMsg()
	case uniswapQuoteMsg:
		return m.handleUniswapQuote(msg)
	case pairLookupResultMsg:
		return m.handlePairLookupResult(msg)
	case liquidityPositionsMsg:
		return m.handleLiquidityPositions(msg)
	case ensLookupResultMsg:
		return m.handleENSLookupResult(msg)
	case ensForwardResolveMsg:
		return m.handleENSForwardResolve(msg)
	case oscillatorBackscanLineMsg:
		return m.handleOscillatorBackscanLine(msg)
	case oscillatorBackscanDoneMsg:
		return m.handleOscillatorBackscanDone()
	case oscillatorSeriesMsg:
		return m.handleOscillatorSeries(msg)
	default:
		if msg, ok := msg.(struct{ clearClipboard bool }); ok && msg.clearClipboard {
			if time.Since(m.copiedMsgTime) >= 2*time.Second {
				m.copiedMsg = ""
			}
			if time.Since(m.txCopiedMsgTime) >= 2*time.Second {
				m.txCopiedMsg = ""
			}
		}
	}
	return m, nil
}

// -------------------- MOUSE MOTION --------------------

func (m *model) handleMouseMotion(mm tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.webcamLogScroll.Dragging {
		m.webcamLogScroll.ApplyDrag(mm.Y, &m.webcamLogVP)
		return m, nil
	}
	if m.logScroll.Dragging {
		m.logScroll.ApplyDrag(mm.Y, &m.logViewport)
		return m, nil
	}
	if m.v4Scroll.Dragging {
		m.v4Scroll.ApplyDrag(mm.Y, &m.v4EventsViewport)
		return m, nil
	}
	if m.txQRScroll.Dragging {
		m.txQRScroll.ApplyDrag(mm.Y, &m.txQRViewport)
		return m, nil
	}
	return m, nil
}

// -------------------- KEY HANDLER --------------------

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Pool info popup: dismiss on Enter or Esc.
	if m.activeDialog == dialogPoolInfo {
		switch msg.String() {
		case "enter", "esc":
			m.activeDialog = dialogNone
			m.poolInfoData = nil
			m.poolInfoErr = ""
			m.poolInfoID = ""
			m.poolInfoKeyLoading = false
			m.poolInfoKeyErr = ""
		}
		return m, nil
	}

	if m.activeDialog == dialogTxResult {
		var vpCmd tea.Cmd
		m.txQRViewport, vpCmd = m.txQRViewport.Update(msg)
		switch msg.String() {
		case "ctrl+c":
			if m.txResultHex != "" {
				m.logInfo("Copied EIP-4527 transaction to clipboard")
				return m, tea.Batch(vpCmd, copyTxJsonToClipboard(m.txResultHex))
			}
			return m, vpCmd
		case "tab":
			if m.txApproveQRFrames != nil {
				m.txSwapStep = !m.txSwapStep
				m.txQRFrameIdx = 0
				if m.txSwapStep {
					m.txQRFrames = m.txSwapQRFrames
					m.txResultHex = m.txSwapJSON
					m.txResultEIP681 = ""
				} else {
					m.txQRFrames = m.txApproveQRFrames
					m.txResultHex = m.txApproveJSON
				}
				m.setTxViewportContent()
			}
			return m, vpCmd
		case "enter":
			return m.openScanTxDialog()
		case "esc":
			m.activeDialog = dialogNone
			m.txResultHex = ""
			m.txResultEIP681 = ""
			m.txResultError = ""
			m.txResultPackaging = false
			m.txResultFormat = ""
			m.txCopiedMsg = ""
			m.txApproveQRFrames = nil
			m.txApproveJSON = ""
			m.txSwapQRFrames = nil
			m.txSwapJSON = ""
			m.txSwapStep = false
			// Refresh watched-token balances — this is the shared dismiss path
			// for swaps, ETH sends, and Terra claims, any of which can have
			// just changed the active wallet's on-chain balances.
			return m, m.loadSelectedWalletDetailsFresh()
		}
		return m, vpCmd
	}

	if m.activeDialog == dialogScanTx {
		return m.handleScanTxKey(msg)
	}

	if m.activeDialog == dialogAccountList {
		switch msg.String() {
		case "up", "k":
			if m.accountListSelectedIdx > 0 {
				m.accountListSelectedIdx--
			}
		case "down", "j":
			if m.accountListSelectedIdx < len(m.accounts)-1 {
				m.accountListSelectedIdx++
			}
		case "enter":
			if m.accountListSelectedIdx >= 0 && m.accountListSelectedIdx < len(m.accounts) {
				selectedAddr := m.accounts[m.accountListSelectedIdx].Address
				for i := range m.accounts {
					m.accounts[i].Active = (i == m.accountListSelectedIdx)
				}
				m.activeAddress = selectedAddr
				m.highlightedAddress = selectedAddr
				m.selectedWallet = m.accountListSelectedIdx
				config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.accounts, Logger: m.logEnabled})
				m.logSuccess(fmt.Sprintf("Activated account: %s", helpers.ShortenAddr(selectedAddr)))
				m.activeDialog = dialogNone
				return m, m.loadSelectedWalletDetailsFresh()
			}
		case "esc":
			m.activeDialog = dialogNone
		}
		return m, nil
	}

	if !m.textInputActive() {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "l", "L":
			m.logEnabled = !m.logEnabled
			if m.logEnabled {
				if m.w > 0 {
					m.logViewport.Width = m.w - 6
				}
				m.logReady = false
				config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.accounts, Logger: m.logEnabled})
				return m, tea.Batch(initLogViewport(), m.logSpinner.Start())
			}
			if m.logBuffer != nil {
				m.logBuffer.Reset()
			}
			m.logger = nil
			m.logReady = false
			config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.accounts, Logger: m.logEnabled})
			return m, nil

		case "i", "I":
			return m.handleIndexerToggle()

		case "pageup", "pagedown", "up", "down":
			v4Visible := m.activePage == config.PageUniswap && m.poolEventMonitorActive && !m.uniswapShowingLiquidity
			bothVisible := v4Visible && m.logEnabled && m.logReady
			var cmd tea.Cmd
			switch {
			case bothVisible && m.focusedPanel == focusedPanelLog:
				m.logViewport, cmd = m.logViewport.Update(msg)
			case v4Visible:
				m.v4EventsViewport, cmd = m.v4EventsViewport.Update(msg)
			case m.logEnabled && m.logReady:
				m.logViewport, cmd = m.logViewport.Update(msg)
			}
			if cmd != nil {
				return m, cmd
			}
		}
	}

	switch m.activePage {
	case config.PageHome:
		return m, nil
	case config.PageWallets:
		return m.handleWalletsKey(msg)
	case config.PageDetails:
		return m.handleDetailsKey(msg)
	case config.PageDappBrowser:
		return m.handleDappsKey(msg)
	case config.PageSettings:
		return m.handleSettingsKey(msg)
	case config.PageUniswap:
		return m.handleUniswapKey(msg)
	case config.PageTerraNullius:
		return m.handleTerraKey(msg)
	case config.PageWatchedTokens:
		return m.handleWatchedTokensKey(msg)
	case config.PageOscillator:
		return m.handleOscillatorKey(msg)
	}
	return m, nil
}

// handleIndexerToggle starts or stops the address indexer.
func (m *model) handleIndexerToggle() (tea.Model, tea.Cmd) {
	if m.txIndexerActive {
		if m.txIndexer != nil {
			m.txIndexer.Stop()
		}
		m.txIndexerActive = false
		m.logInfo("Address indexer stopped")
		return m, nil
	}
	if !m.rpcConnected || m.ethClient == nil {
		m.logWarn("Indexer requires an active RPC connection")
		return m, nil
	}
	if len(m.accounts) == 0 {
		m.logWarn("No saved addresses to index")
		return m, nil
	}
	addrs := make([]common.Address, len(m.accounts))
	labels := make([]string, len(m.accounts))
	for i, a := range m.accounts {
		addrs[i] = common.HexToAddress(a.Address)
		labels[i] = helpers.HyperAddr(addrs[i])
	}
	m.txIndexer = indexer.New()
	m.txIndexer.Start(m.rpcURL, addrs, m.tokenWatchForActiveChain())
	m.txIndexerActive = true
	m.logInfo(fmt.Sprintf("Address indexer started — scanning backward from current block, watching: %s", strings.Join(labels, "  ")))
	cmds := []tea.Cmd{waitForIndexedEvent(m.txIndexer), waitForV4PoolEvent(m.txIndexer), waitForIndexerProgress(m.txIndexer)}
	if m.eventStore != nil {
		cmds = append(cmds, loadRecentEvents(m.eventStore, 50))
	} else if m.eventStoreErr != "" {
		m.logWarn(fmt.Sprintf("[indexer] event store unavailable: %s", m.eventStoreErr))
	}
	return m, tea.Batch(cmds...)
}

// -------------------- MOUSE HANDLER --------------------

func (m *model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Pool info popup captures all other clicks while open (its OK button and
	// ID-copy line are handled generically via dispatchRegionClick).
	if m.activeDialog == dialogPoolInfo {
		return m, nil
	}

	// Tx hash in the "Message Sent — Awaiting Confirmation" popup: double-click
	// or ctrl-click opens the transaction on the correct Etherscan subdomain.
	if m.activeDialog == dialogPasteSignedTx && m.pasteTxPhase == pasteTxPhasePolling && msg.Type == tea.MouseLeft {
		inHash := msg.Y == m.pasteTxHashLineY && msg.X >= m.pasteTxHashLineX1 && msg.X < m.pasteTxHashLineX2
		if inHash && (msg.Ctrl || m.isDoubleClick(msg.X, msg.Y)) {
			return m, openInBrowser(etherscanTxURL(m.pasteTxChainID, m.pasteTxHash))
		}
	}

	if msg.Type == tea.MouseWheelUp || msg.Type == tea.MouseWheelDown {
		if m.activeDialog == dialogScanTx {
			var cmd tea.Cmd
			m.webcamLogVP, cmd = m.webcamLogVP.Update(msg)
			return m, cmd
		}
		if m.activeDialog == dialogTxResult {
			var cmd tea.Cmd
			m.txQRViewport, cmd = m.txQRViewport.Update(msg)
			return m, cmd
		}
		if m.activePage == config.PageWatchedTokens {
			var cmd tea.Cmd
			m.tokenListViewport, cmd = m.tokenListViewport.Update(msg)
			return m, cmd
		}
		v4Visible := m.activePage == config.PageUniswap && m.poolEventMonitorActive && !m.uniswapShowingLiquidity
		bothVisible := v4Visible && m.logEnabled && m.logReady
		var cmd tea.Cmd
		switch {
		case bothVisible && m.focusedPanel == focusedPanelLog:
			m.logViewport, cmd = m.logViewport.Update(msg)
		case v4Visible:
			m.v4EventsViewport, cmd = m.v4EventsViewport.Update(msg)
		case m.logEnabled && m.logReady:
			m.logViewport, cmd = m.logViewport.Update(msg)
		}
		if cmd != nil {
			return m, cmd
		}
	}

	if msg.Type == tea.MouseRelease {
		m.logScroll.Dragging = false
		m.v4Scroll.Dragging = false
		m.txQRScroll.Dragging = false
		m.webcamLogScroll.Dragging = false
		m.tokenListScroll.Dragging = false
		return m, nil
	}

	if msg.Type == tea.MouseLeft {
		return m.handleMouseLeft(msg)
	}
	return m, nil
}

func (m *model) handleMouseLeft(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Panel focus when both V4 events and log are visible.
	if m.activePage == config.PageUniswap && m.poolEventMonitorActive &&
		!m.uniswapShowingLiquidity && m.logEnabled && m.logScroll.PanelTop > 3 {
		if msg.Y >= m.logScroll.PanelTop-3 {
			m.focusedPanel = focusedPanelLog
		} else {
			m.focusedPanel = focusedPanelV4Events
		}
	}

	// Scrollbar hit tests.
	if m.activeDialog == dialogScanTx && m.webcamLogScroll.PanelTop > 0 {
		if m.webcamLogScroll.HitTest(msg.X, msg.Y, m.webcamLogScroll.PanelTop+m.webcamLogVP.Height-1) {
			m.webcamLogScroll.Dragging = true
			m.webcamLogScroll.ApplyDrag(msg.Y, &m.webcamLogVP)
			return m, nil
		}
	}
	if m.activeDialog == dialogTxResult && m.txQRScroll.PanelTop > 0 {
		if m.txQRScroll.HitTest(msg.X, msg.Y, m.txQRScroll.PanelTop+m.txQRViewport.Height-1) {
			m.txQRScroll.Dragging = true
			m.txQRScroll.ApplyDrag(msg.Y, &m.txQRViewport)
			return m, nil
		}
	}
	if m.activePage == config.PageWatchedTokens && m.tokenListScroll.PanelTop > 0 {
		if m.tokenListScroll.HitTest(msg.X, msg.Y, m.tokenListScroll.PanelTop+m.tokenListViewport.Height-1) {
			m.tokenListScroll.Dragging = true
			m.tokenListScroll.ApplyDrag(msg.Y, &m.tokenListViewport)
			return m, nil
		}
	}
	v4Visible := m.activePage == config.PageUniswap && m.poolEventMonitorActive && !m.uniswapShowingLiquidity
	if v4Visible && m.v4Scroll.PanelTop > 0 {
		if m.v4Scroll.HitTest(msg.X, msg.Y, m.v4Scroll.PanelTop+m.v4EventsViewport.Height-1) {
			m.v4Scroll.Dragging = true
			m.v4Scroll.ApplyDrag(msg.Y, &m.v4EventsViewport)
			return m, nil
		}
	}
	if m.logEnabled && m.logReady && m.logScroll.PanelTop > 0 {
		if m.logScroll.HitTest(msg.X, msg.Y, m.logScroll.PanelTop+m.logViewport.Height-1) {
			m.logScroll.Dragging = true
			m.logScroll.ApplyDrag(msg.Y, &m.logViewport)
			return m, nil
		}
	}

	// Header address double-click → account list popup.
	if m.activeAddress != "" && m.headerAddrX > 0 &&
		msg.X >= m.headerAddrX && msg.X < m.headerAddrX+m.headerAddrWidth &&
		msg.Y == m.headerAddrY {
		m.logDebug(fmt.Sprintf("Click on header address at (%d,%d)", msg.X, msg.Y))
		if m.isDoubleClick(msg.X, msg.Y) {
			m.activeDialog = dialogAccountList
			m.accountListSelectedIdx = 0
			for i, w := range m.accounts {
				if strings.EqualFold(w.Address, m.activeAddress) {
					m.accountListSelectedIdx = i
					break
				}
			}
			m.logInfo("Opening account list popup")
			return m, nil
		}
		m.logDebug("Single click registered, waiting for double-click")
		return m, nil
	}

	// Log panel OSC 8 hyperlink click.
	if m.logEnabled && m.logReady && m.logScroll.PanelTop > 0 && m.logBuffer != nil {
		lines := strings.Split(m.logBuffer.String(), "\n")
		vpLine := msg.Y - m.logScroll.PanelTop
		absLine := vpLine + m.logViewport.YOffset
		if vpLine >= 0 && absLine < len(lines) {
			if url := urlAtCol(lines[absLine], msg.X-2); url != "" {
				return m.handleURLClick(url)
			}
		}
	}

	// V4 events panel OSC 8 hyperlink click.
	if v4Visible && m.v4Scroll.PanelTop > 0 {
		vpH := m.v4EventsViewport.Height
		if msg.Y >= m.v4Scroll.PanelTop && msg.Y < m.v4Scroll.PanelTop+vpH {
			vpLine := msg.Y - m.v4Scroll.PanelTop
			absLine := vpLine + m.v4EventsViewport.YOffset
			lines := strings.Split(uniswap.V4EventsContent(m.w-2, m.v4PoolRows), "\n")
			if absLine < len(lines) {
				if url := urlAtCol(lines[absLine], msg.X-3); url != "" {
					return m.handleURLClick(url)
				}
			}
		}
	}

	if !m.logScroll.Dragging && !m.v4Scroll.Dragging && !m.txQRScroll.Dragging {
		m.logDebug(fmt.Sprintf("Click at (%d,%d) - header check: addr='%s', X=%d, Y=%d",
			msg.X, msg.Y, m.activeAddress, m.headerAddrX, m.headerAddrY))
		m.logDebug(fmt.Sprintf("Registered %d clickable areas", len(m.clickableAreas)))
	}

	// Clickable address areas.
	for idx, area := range m.clickableAreas {
		if msg.X >= area.X && msg.X < area.X+area.Width &&
			msg.Y >= area.Y && msg.Y < area.Y+area.Height {
			m.logDebug(fmt.Sprintf("Click matched area %d: addr=%s at (%d,%d) size=%dx%d",
				idx, helpers.ShortenAddr(area.Address), area.X, area.Y, area.Width, area.Height))

			if m.activePage == config.PageWallets || m.activeDialog == dialogAccountList {
				if m.isDoubleClick(msg.X, msg.Y) {
					for i, w := range m.accounts {
						if strings.EqualFold(w.Address, area.Address) {
							for j := range m.accounts {
								m.accounts[j].Active = false
							}
							m.accounts[i].Active = true
							m.activeAddress = area.Address
							m.highlightedAddress = area.Address
							m.selectedWallet = i
							config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.accounts, Logger: m.logEnabled})
							m.logSuccess(fmt.Sprintf("Activated account: %s", helpers.ShortenAddr(area.Address)))
							if m.activeDialog == dialogAccountList {
								m.activeDialog = dialogNone
							}
							return m, m.loadSelectedWalletDetailsFresh()
						}
					}
					return m, nil
				}
				for i, w := range m.accounts {
					if strings.EqualFold(w.Address, area.Address) {
						m.selectedWallet = i
						m.highlightedAddress = area.Address
						if m.activeDialog == dialogAccountList {
							m.accountListSelectedIdx = i
						}
						break
					}
				}
				m.logDebug("Single click on account - waiting for double-click to activate")
				return m, nil
			}

			if m.activePage == config.PageDetails && area.Address == m.details.Address {
				return m, copyToClipboard(area.Address)
			}
			for i, w := range m.accounts {
				if strings.EqualFold(w.Address, area.Address) {
					m.selectedWallet = i
					break
				}
			}
			m.highlightedAddress = area.Address
			m.activePage = config.PageDetails
			m.loading = true
			m.details = rpc.WalletDetails{Address: area.Address}
			return m, loadDetails(m.ethClient, common.HexToAddress(area.Address), m.tokenWatchForActiveChain())
		}
	}

	if m.activeDialog == dialogTxResult && m.txResultHex != "" {
		m.logInfo("Copied transaction JSON to clipboard")
		return m, copyTxJsonToClipboard(m.txResultHex)
	}
	if m.activePage == config.PageDetails && m.details.Address != "" && msg.Y == m.addressLineY {
		return m, copyToClipboard(m.details.Address)
	}
	return m, nil
}

// handleURLClick opens a URL from an OSC 8 hyperlink click — either a poolinfo:// deep link
// or an external browser URL.
func (m *model) handleURLClick(url string) (tea.Model, tea.Cmd) {
	if strings.HasPrefix(url, "poolinfo://") {
		poolIDHex := strings.TrimPrefix(url, "poolinfo://")
		m.activeDialog = dialogPoolInfo
		m.poolInfoLoading = true
		m.poolInfoID = poolIDHex
		m.poolInfoData = nil
		m.poolInfoErr = ""
		m.logInfo(fmt.Sprintf("Pool Info: querying pool %s", shortPoolID(poolIDHex)))
		return m, fetchPoolInfo(m.rpcURL, poolIDHex)
	}
	return m, openInBrowser(url)
}

// -------------------- HELPERS --------------------

// appendNumericInput appends a digit or '.' to value.
// On the first keystroke after a non-zero prior value, the field is cleared.
// Returns (newValue, newEditing, ok) — ok is false when a duplicate '.' is rejected.
func appendNumericInput(value string, editing bool, char string) (string, bool, bool) {
	if !editing && value != "" && value != "0" {
		value = ""
	}
	if char == "." && strings.Contains(value, ".") {
		return value, editing, false
	}
	return value + char, true, true
}
