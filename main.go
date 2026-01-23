package main

import (
	"fmt"
	"image/color"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm-wallet-tui/config"
	"charm-wallet-tui/helpers"
	"charm-wallet-tui/rpc"
	"charm-wallet-tui/styles"
	// View packages - ready to use when delegating rendering
	// "charm-wallet-tui/views/dapps"
	// "charm-wallet-tui/views/details"
	// "charm-wallet-tui/views/settings"
	// "charm-wallet-tui/views/wallets"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/lucasb-eyer/go-colorful"
	// "github.com/muesli/gamut"

	"github.com/ethereum/go-ethereum/common"
)

// -------------------- THEME (Lip Gloss) --------------------
// Styles now come from the styles package

var (
	cBg      = styles.CBg
	cPanel   = styles.CPanel
	cBorder  = styles.CBorder
	cMuted   = styles.CMuted
	cText    = styles.CText
	cAccent  = styles.CAccent
	cAccent2 = styles.CAccent2
	cWarn    = styles.CWarn

	appStyle       = styles.AppStyle
	titleStyle     = styles.TitleStyle
	panelStyle     = styles.PanelStyle
	navStyle       = styles.NavStyle
	hotkeyStyle    = lipgloss.NewStyle().Foreground(styles.CMuted)
	hotkeyKeyStyle = lipgloss.NewStyle().Foreground(styles.CAccent).Bold(true)
	helpRightStyle = lipgloss.NewStyle().Foreground(styles.CMuted)
)

// -------------------- DATA TYPES --------------------

type page int

// Temporary form field storage (package-level to avoid pointer-to-copy issues)
var (
	tempRPCFormName   string
	tempRPCFormURL    string
	tempNicknameField string
	tempDappName      string
	tempDappAddress   string
	tempDappIcon      string
	tempDappNetwork   string
)

const (
	pageHome page = iota
	pageWallets
	pageDetails
	pageSettings
	pageDappBrowser
)

// clickableArea represents a clickable region on screen for addresses
type clickableArea struct {
	X, Y          int    // top-left position
	Width, Height int    // dimensions
	Address       string // wallet address to navigate to
}

type walletItem struct {
	addr string
}

func (w walletItem) Title() string       { return helpers.ShortenAddr(w.addr) }
func (w walletItem) Description() string { return w.addr }
func (w walletItem) FilterValue() string { return w.addr }

type homeMenuItem struct {
	title string
	desc  string
	page  string
}

func (h homeMenuItem) Title() string       { return h.title }
func (h homeMenuItem) Description() string { return h.desc }
func (h homeMenuItem) FilterValue() string { return h.title + " " + h.desc }

type tokenBalance struct {
	Symbol   string
	Decimals uint8
	Balance  *big.Int
}

type walletDetails struct {
	Address    string
	EthWei     *big.Int
	Tokens     []tokenBalance
	LoadedAt   time.Time
	ErrMessage string
}

// -------------------- MODEL --------------------

type model struct {
	w, h int

	activePage page

	// main list
	wallets        []config.WalletEntry
	selectedWallet int

	// add-wallet input
	adding     bool
	input      textinput.Model
	addError   string    // error message when adding wallet (e.g., duplicate)
	addErrTime time.Time // time when error was shown

	// details state
	spin          spinner.Model
	loading       bool
	details       walletDetails
	detailsCache  map[string]walletDetails // cache wallet details by address
	rpcURL        string
	ethClient     *rpc.Client
	rpcConnected  bool // true if RPC is successfully connected
	rpcConnecting bool // true if connection attempt is in progress

	// token watchlist (simple starter set)
	// You can expand this (or load from config).
	tokenWatch []rpc.WatchedToken

	// clipboard feedback
	copiedMsg     string
	copiedMsgTime time.Time
	addressLineY  int // Y position of the address line in details view

	// settings state
	settingsMode   string // "list", "add", "edit"
	rpcURLs        []config.RPCUrl
	selectedRPCIdx int
	form           *huh.Form
	configPath     string

	// dApp browser state
	dapps           []config.DApp
	dappMode        string // "list", "add", "edit"
	selectedDappIdx int

	// home menu
	homeMenu      list.Model
	homeMenuReady bool

	// nickname editing
	nicknaming bool

	// currently highlighted address in wallet list
	highlightedAddress string
	// active address (the one marked with ★)
	activeAddress string

	// clickable areas for mouse support
	clickableAreas []clickableArea

	// debug log panel
	logEnabled  bool
	logEntries  []string
	logViewport viewport.Model
	logReady    bool
}

// -------------------- INIT --------------------

func newModel() model {
	// config path
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".charm-wallet-config.json")

	// load config
	cfg := config.Load(configPath)

	// Load wallet entries from config
	wallets := cfg.Wallets
	if wallets == nil {
		wallets = []config.WalletEntry{}
	}

	// Find active wallet or default to first
	selectedIdx := 0
	for i, w := range wallets {
		if w.Active {
			selectedIdx = i
			break
		}
	}

	// input
	in := textinput.New()
	in.Placeholder = "Paste Public Address 0x…"
	in.Prompt = "Add wallet: "
	in.PromptStyle = lipgloss.NewStyle().Foreground(cAccent)
	in.TextStyle = lipgloss.NewStyle().Foreground(cText)
	in.Cursor.Style = lipgloss.NewStyle().Foreground(cAccent2)
	in.CharLimit = 42
	in.Width = 48

	// spinner
	sp := spinner.New()
	sp.Spinner = spinner.Line
	sp.Style = lipgloss.NewStyle().Foreground(cAccent2)

	// rpc URL from environment
	rpcFromEnv := strings.TrimSpace(os.Getenv("ETH_RPC_URL"))

	// If no RPC in config but ENV is set, use ENV
	if len(cfg.RPCURLs) == 0 && rpcFromEnv != "" {
		cfg.RPCURLs = []config.RPCUrl{{Name: "Default", URL: rpcFromEnv, Active: true}}
	}

	// Find active RPC
	activeRPC := rpcFromEnv
	for _, r := range cfg.RPCURLs {
		if r.Active {
			activeRPC = r.URL
			break
		}
	}

	// starter token watchlist (Mainnet):
	// USDC, USDT, DAI, WETH
	watch := []rpc.WatchedToken{
		{Symbol: "WETH", Decimals: 18, Address: common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")},
		{Symbol: "USDC", Decimals: 6, Address: common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")},
		{Symbol: "USDT", Decimals: 6, Address: common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7")},
		{Symbol: "DAI", Decimals: 18, Address: common.HexToAddress("0x6B175474E89094C44Da98b954EedeAC495271d0F")},
	}

	// Initialize log viewport
	vp := viewport.New(0, 20) // Will be resized in Update on first WindowSizeMsg
	vp.Style = lipgloss.NewStyle().
		Foreground(cText).
		Background(cPanel)

	m := model{
		activePage:     pageWallets,
		wallets:        wallets,
		selectedWallet: selectedIdx,
		adding:         false,
		input:          in,
		spin:           sp,
		rpcURL:         activeRPC,
		tokenWatch:     watch,
		settingsMode:   "list",
		rpcURLs:        cfg.RPCURLs,
		selectedRPCIdx: 0,
		configPath:     configPath,
		logViewport:    vp,
		logEntries:     []string{},
		detailsCache:   make(map[string]walletDetails),
		dapps:          cfg.Dapps,
		dappMode:       "list",
		selectedDappIdx: 0,
	}

	// Set initial highlighted address and active address
	if len(wallets) > 0 {
		m.highlightedAddress = wallets[selectedIdx].Address
		// Find the active wallet (marked with ★)
		for _, w := range wallets {
			if w.Active {
				m.activeAddress = w.Address
				break
			}
		}
	}

	return m
}

func (m model) Init() tea.Cmd {
	// connect if rpc is set
	if m.rpcURL != "" {
		m.rpcConnecting = true
		return tea.Batch(m.spin.Tick, connectRPC(m.rpcURL))
	}
	return m.spin.Tick
}

// -------------------- COMMANDS / MESSAGES --------------------

type clipboardCopiedMsg struct{}

type logInitMsg struct{}

type rpcConnectedMsg struct {
	client *rpc.Client
	err    error
}

func connectRPC(url string) tea.Cmd {
	return func() tea.Msg {
		result := rpc.Connect(url)
		return rpcConnectedMsg{client: result.Client, err: result.Error}
	}
}

func initLogViewport() tea.Cmd {
	return func() tea.Msg {
		return logInitMsg{}
	}
}

type detailsLoadedMsg struct {
	d   walletDetails
	err error
}

func loadDetails(client *rpc.Client, addr common.Address, watch []rpc.WatchedToken) tea.Cmd {
	return func() tea.Msg {
		rpcDetails := rpc.LoadWalletDetails(client, addr, watch)

		// Convert rpc.WalletDetails to our walletDetails type
		d := walletDetails{
			Address:    rpcDetails.Address,
			EthWei:     rpcDetails.EthWei,
			LoadedAt:   rpcDetails.LoadedAt,
			ErrMessage: rpcDetails.ErrMessage,
		}

		// Convert token balances
		for _, t := range rpcDetails.Tokens {
			d.Tokens = append(d.Tokens, tokenBalance{
				Symbol:   t.Symbol,
				Decimals: t.Decimals,
				Balance:  t.Balance,
			})
		}

		return detailsLoadedMsg{d: d, err: nil}
	}
}

func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		err := clipboard.WriteAll(text)
		if err == nil {
			return clipboardCopiedMsg{}
		}
		return nil
	}
}

func clearClipboardMsg() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return struct{ clearClipboard bool }{true}
	})
}

// -------------------- LOG FUNCTIONS --------------------

// addLog adds a log entry with timestamp and type
func (m *model) addLog(logType, message string) {
	if !m.logEnabled {
		return
	}

	timestamp := time.Now().Format("15:04:05")
	var icon string
	switch logType {
	case "info":
		icon = "ℹ️"
	case "success":
		icon = "✅"
	case "error":
		icon = "❌"
	case "warning":
		icon = "⚠️"
	case "debug":
		icon = "🔍"
	default:
		icon = "📝"
	}

	entry := fmt.Sprintf("**%s** `%s` %s", icon, timestamp, message)
	m.logEntries = append(m.logEntries, entry)

	// Keep only last 100 entries to avoid memory bloat
	if len(m.logEntries) > 100 {
		m.logEntries = m.logEntries[1:]
	}

	// Update viewport content
	m.updateLogViewport()
}

// updateLogViewport refreshes the viewport content with rendered markdown
func (m *model) updateLogViewport() {
	if !m.logReady {
		return
	}

	// Join all log entries with newlines
	content := strings.Join(m.logEntries, "\n\n")

	// Render with glamour
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(m.logViewport.Width-2),
	)
	if err != nil {
		// Fallback to plain text if glamour fails
		m.logViewport.SetContent(content)
		return
	}

	rendered, err := renderer.Render(content)
	if err != nil {
		m.logViewport.SetContent(content)
		return
	}

	m.logViewport.SetContent(rendered)
	// Scroll to bottom to show latest entries
	m.logViewport.GotoBottom()
}

func (m *model) createHomeMenuList() {
	items := []list.Item{
		homeMenuItem{title: "Account List", desc: "Browse accounts and addresses", page: "accounts"},
		homeMenuItem{title: "RPC Settings", desc: "Manage RPC endpoints", page: "settings"},
		homeMenuItem{title: "dApp Browser", desc: "Browse saved dApps", page: "dapps"},
	}

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	delegate.Styles.NormalTitle = lipgloss.NewStyle().Foreground(cText)
	delegate.Styles.NormalDesc = lipgloss.NewStyle().Foreground(cMuted)
	delegate.Styles.SelectedTitle = lipgloss.NewStyle().Foreground(cAccent2).Bold(true)
	delegate.Styles.SelectedDesc = lipgloss.NewStyle().Foreground(cAccent2)
	delegate.Styles.DimmedTitle = lipgloss.NewStyle().Foreground(cMuted)
	delegate.Styles.DimmedDesc = lipgloss.NewStyle().Foreground(cMuted)
	delegate.Styles.FilterMatch = lipgloss.NewStyle().Underline(true).Foreground(cAccent)

	// Calculate available height:
	// - Global header: ~5 lines
	// - Page title + subtitle: ~3 lines
	// - Nav bar: ~2 lines
	// - Debug log reserve: 15 lines
	// Total overhead: ~25 lines
	menuHeight := max(0, m.h-25)
	menu := list.New(items, delegate, max(0, m.w-6), menuHeight)
	menu.SetShowTitle(false)
	menu.SetShowStatusBar(false)
	menu.SetShowPagination(false)
	menu.SetShowHelp(false)
	menu.SetFilteringEnabled(true)
	menu.Styles.FilterPrompt = lipgloss.NewStyle().Foreground(cAccent)
	menu.Styles.FilterCursor = lipgloss.NewStyle().Foreground(cAccent2)
	menu.Styles.NoItems = lipgloss.NewStyle().Foreground(cMuted)

	m.homeMenu = menu
	m.homeMenuReady = true
}

func (m *model) createAddRPCForm() {
	tempRPCFormName = ""
	tempRPCFormURL = ""

	m.form = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("RPC Name").
				Description("A friendly name for this RPC endpoint").
				Value(&tempRPCFormName).
				Placeholder("My Infura Node"),

			huh.NewInput().
				Title("RPC URL").
				Description("The complete RPC URL (https://...)").
				Value(&tempRPCFormURL).
				Placeholder("https://mainnet.infura.io/v3/..."),
		),
	).WithTheme(huh.ThemeCatppuccin())

	// Initialize the form
	m.form.Init()
}

func (m *model) createEditRPCForm(idx int) {
	if idx < 0 || idx >= len(m.rpcURLs) {
		return
	}

	rpc := m.rpcURLs[idx]
	tempRPCFormName = rpc.Name
	tempRPCFormURL = rpc.URL

	m.form = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("RPC Name").
				Value(&tempRPCFormName).
				Placeholder("My Node"),

			huh.NewInput().
				Title("RPC URL").
				Value(&tempRPCFormURL).
				Placeholder("https://..."),
		),
	).WithTheme(huh.ThemeCatppuccin())

	// Initialize the form
	m.form.Init()
}

func (m *model) createNicknameForm() {
	// Find current wallet's nickname
	tempNicknameField = ""
	for _, w := range m.wallets {
		if strings.EqualFold(w.Address, m.details.Address) {
			tempNicknameField = w.Name
			break
		}
	}

	placeholderText := "Enter nickname"
	if tempNicknameField != "" {
		placeholderText = tempNicknameField
	}

	m.form = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Wallet Nickname").
				Description("Set a friendly name for this wallet").
				Value(&tempNicknameField).
				Placeholder(placeholderText),
		),
	).WithTheme(huh.ThemeCatppuccin())

	// Initialize the form
	m.form.Init()
}

func (m *model) createAddDappForm() {
	tempDappName = ""
	tempDappAddress = ""
	tempDappIcon = ""
	tempDappNetwork = ""

	// Build network options from RPC URLs
	networkOptions := []huh.Option[string]{}
	for _, rpcURL := range m.rpcURLs {
		networkOptions = append(networkOptions, huh.NewOption(rpcURL.Name, rpcURL.Name))
	}

	// Find the active RPC URL name as default
	defaultNetwork := ""
	for _, rpcURL := range m.rpcURLs {
		if rpcURL.Active {
			defaultNetwork = rpcURL.Name
			break
		}
	}
	tempDappNetwork = defaultNetwork

	m.form = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("dApp Name").
				Description("A friendly name for this dApp").
				Value(&tempDappName).
				Placeholder("Uniswap"),

			huh.NewInput().
				Title("dApp Address").
				Description("The URL or address of the dApp").
				Value(&tempDappAddress).
				Placeholder("https://app.uniswap.org"),

			huh.NewInput().
				Title("Icon").
				Description("Icon or emoji for the dApp (optional)").
				Value(&tempDappIcon).
				Placeholder("🦄"),

			huh.NewSelect[string]().
				Options(networkOptions...).
				Title("Network").
				Description("Choose the network for this dApp").
				Value(&tempDappNetwork),
		),
	).WithTheme(huh.ThemeCatppuccin())

	m.form.Init()
}

func (m *model) createEditDappForm(idx int) {
	if idx < 0 || idx >= len(m.dapps) {
		return
	}

	dapp := m.dapps[idx]
	tempDappName = dapp.Name
	tempDappAddress = dapp.Address
	tempDappIcon = dapp.Icon
	tempDappNetwork = dapp.Network

	// Build network options from RPC URLs
	networkOptions := []huh.Option[string]{}
	for _, rpcURL := range m.rpcURLs {
		networkOptions = append(networkOptions, huh.NewOption(rpcURL.Name, rpcURL.Name))
	}

	// If current network is empty, use active RPC URL as default
	if tempDappNetwork == "" {
		for _, rpcURL := range m.rpcURLs {
			if rpcURL.Active {
				tempDappNetwork = rpcURL.Name
				break
			}
		}
	}

	m.form = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("dApp Name").
				Value(&tempDappName).
				Placeholder("Uniswap"),

			huh.NewInput().
				Title("dApp Address").
				Value(&tempDappAddress).
				Placeholder("https://app.uniswap.org"),

			huh.NewInput().
				Title("Icon").
				Value(&tempDappIcon).
				Placeholder("🦄"),

			huh.NewSelect[string]().
				Options(networkOptions...).
				Title("Network").
				Description("Choose the network for this dApp").
				Value(&tempDappNetwork),
		),
	).WithTheme(huh.ThemeCatppuccin())

	m.form.Init()
}

// -------------------- UPDATE --------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if m.activePage == pageHome {
		if !m.homeMenuReady {
			m.createHomeMenuList()
		}
		var cmd tea.Cmd
		m.homeMenu, cmd = m.homeMenu.Update(msg)
		if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "enter" && m.homeMenu.FilterState() != list.Filtering {
			if item, ok := m.homeMenu.SelectedItem().(homeMenuItem); ok {
				switch item.page {
				case "accounts":
					m.activePage = pageWallets
				case "settings":
					m.activePage = pageSettings
					m.settingsMode = "list"
				case "dapps":
					m.activePage = pageDappBrowser
					m.dappMode = "list"
				}
			}
			m.createHomeMenuList()
			return m, nil
		}
		return m, cmd
	}

	// Handle form updates first (before message switching)
	if m.activePage == pageDetails && m.nicknaming && m.form != nil {
		form, cmd := m.form.Update(msg)
		if f, ok := form.(*huh.Form); ok {
			m.form = f

			// Check if form is completed
			if m.form.State == huh.StateCompleted {
				// Save nickname to wallet entry
				for i := range m.wallets {
					if strings.EqualFold(m.wallets[i].Address, m.details.Address) {
						oldName := m.wallets[i].Name
						m.wallets[i].Name = strings.TrimSpace(tempNicknameField)
						if oldName == "" && m.wallets[i].Name != "" {
							m.addLog("success", fmt.Sprintf("Set nickname `%s` for wallet `%s`", m.wallets[i].Name, helpers.ShortenAddr(m.details.Address)))
						} else if m.wallets[i].Name == "" {
							m.addLog("info", fmt.Sprintf("Cleared nickname for wallet `%s`", helpers.ShortenAddr(m.details.Address)))
						} else {
							m.addLog("success", fmt.Sprintf("Updated nickname to `%s` for wallet `%s`", m.wallets[i].Name, helpers.ShortenAddr(m.details.Address)))
						}
						break
					}
				}
				config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.wallets, Dapps: m.dapps})
				m.nicknaming = false
				m.form = nil
				return m, nil
			}

			// Check if form was aborted (ESC pressed)
			if m.form.State == huh.StateAborted {
				m.nicknaming = false
				m.form = nil
				return m, nil
			}
		}
		return m, cmd
	}

	if (m.activePage == pageDetails || m.activePage == pageDappBrowser) && (m.dappMode == "add" || m.dappMode == "edit") && m.form != nil {
		form, cmd := m.form.Update(msg)
		if f, ok := form.(*huh.Form); ok {
			m.form = f

			// Check if form is completed
			if m.form.State == huh.StateCompleted {
				if m.dappMode == "add" {
					if tempDappName != "" && tempDappAddress != "" {
						newDapp := config.DApp{Name: tempDappName, Address: tempDappAddress, Icon: tempDappIcon, Network: tempDappNetwork}
						m.dapps = append(m.dapps, newDapp)
						config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.wallets, Dapps: m.dapps})
						m.addLog("success", fmt.Sprintf("Added dApp: `%s`", tempDappName))
					}
				} else if m.dappMode == "edit" {
					if m.selectedDappIdx >= 0 && m.selectedDappIdx < len(m.dapps) {
						m.dapps[m.selectedDappIdx].Name = tempDappName
						m.dapps[m.selectedDappIdx].Address = tempDappAddress
						m.dapps[m.selectedDappIdx].Icon = tempDappIcon
						m.dapps[m.selectedDappIdx].Network = tempDappNetwork
						config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.wallets, Dapps: m.dapps})
						m.addLog("success", fmt.Sprintf("Updated dApp: `%s`", tempDappName))
					}
				}
				m.dappMode = "list"
				m.form = nil
				return m, nil
			}

			// Check if form was aborted (ESC pressed)
			if m.form.State == huh.StateAborted {
				m.dappMode = "list"
				m.form = nil
				return m, nil
			}
		}
		return m, cmd
	}

	if m.activePage == pageSettings && (m.settingsMode == "add" || m.settingsMode == "edit") && m.form != nil {
		form, cmd := m.form.Update(msg)
		if f, ok := form.(*huh.Form); ok {
			m.form = f

			// Check if form is completed
			if m.form.State == huh.StateCompleted {
				if m.settingsMode == "add" {
					if tempRPCFormName != "" && tempRPCFormURL != "" {
						newRPC := config.RPCUrl{Name: tempRPCFormName, URL: tempRPCFormURL, Active: false}
						m.rpcURLs = append(m.rpcURLs, newRPC)
						config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.wallets, Dapps: m.dapps})
						m.addLog("success", fmt.Sprintf("Added RPC endpoint: `%s` (%s)", tempRPCFormName, tempRPCFormURL))
					}
				} else if m.settingsMode == "edit" {
					if m.selectedRPCIdx >= 0 && m.selectedRPCIdx < len(m.rpcURLs) {
						m.rpcURLs[m.selectedRPCIdx].Name = tempRPCFormName
						m.rpcURLs[m.selectedRPCIdx].URL = tempRPCFormURL
						config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.wallets, Dapps: m.dapps})
						m.addLog("success", fmt.Sprintf("Updated RPC endpoint: `%s`", tempRPCFormName))
					}
				}
				m.settingsMode = "list"
				m.form = nil
				// Return without the form's cmd to ensure we're back in list mode
				return m, nil
			}

			// Check if form was aborted (ESC pressed)
			if m.form.State == huh.StateAborted {
				m.settingsMode = "list"
				m.form = nil
				return m, nil
			}
		}
		return m, cmd
	}

	switch msg := msg.(type) {

	case logInitMsg:
		if !m.logEnabled {
			return m, nil
		}
		m.logReady = true
		m.addLog("info", "Debug log enabled")
		return m, nil

	case rpcConnectedMsg:
		m.rpcConnecting = false
		if msg.err != nil {
			// Connection failed
			m.ethClient = nil
			m.rpcConnected = false
			m.addLog("error", fmt.Sprintf("RPC connection failed: `%s`", msg.err.Error()))
		} else {
			// Connection successful
			m.ethClient = msg.client
			m.rpcConnected = true
			m.addLog("success", fmt.Sprintf("RPC connected to `%s`", msg.client.URL))
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height

		// Only initialize viewport if log is enabled
		if m.logEnabled {
			// Reserve space for log panel (20 lines + borders)
			logPanelHeight := 20

			// Update log viewport dimensions
			m.logViewport.Width = msg.Width - 4
			m.logViewport.Height = logPanelHeight
			if m.logReady {
				m.updateLogViewport()
			}
		}

		if m.homeMenuReady {
			// Use same calculation as createHomeMenuList: total overhead ~25 lines
			menuHeight := max(0, m.h-25)
			m.homeMenu.SetSize(max(0, m.w-6), menuHeight)
		}

		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case detailsLoadedMsg:
		m.loading = false
		m.details = msg.d
		// Cache the loaded details
		if m.details.Address != "" {
			m.detailsCache[strings.ToLower(m.details.Address)] = m.details
		}
		if msg.err != nil && m.details.ErrMessage == "" {
			m.details.ErrMessage = "Failed to load wallet details."
			m.addLog("error", fmt.Sprintf("Failed to load details for `%s`", helpers.ShortenAddr(m.details.Address)))
		} else if m.details.ErrMessage != "" {
			m.addLog("error", fmt.Sprintf("Wallet `%s`: %s", helpers.ShortenAddr(m.details.Address), m.details.ErrMessage))
		} else {
			m.addLog("success", fmt.Sprintf("Loaded details for `%s` - ETH: %s", helpers.ShortenAddr(m.details.Address), helpers.FormatETH(m.details.EthWei)))
		}
		return m, nil

	case tea.KeyMsg:
		// global keys
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit

		case "l":
			// Toggle debug log
			m.logEnabled = !m.logEnabled
			if m.logEnabled {
				// Initialize viewport when enabling
				if m.w > 0 {
					m.logViewport.Width = m.w - 4
					m.logViewport.Height = 20
				}
				m.logReady = false
				return m, initLogViewport()
			}
			// Clear logs and de-initialize when disabling
			m.logEntries = []string{}
			m.logReady = false
			return m, nil
		}

		// page-specific behavior
		switch m.activePage {

		case pageHome:
			// Home page - form handles its own keys
			// No additional key handling needed
			return m, nil

		case pageWallets:
			// adding flow
			if m.adding {
				switch msg.String() {
				case "enter":
					val := strings.TrimSpace(m.input.Value())
					if helpers.IsValidEthAddress(val) {
						newAddr := common.HexToAddress(val).Hex()

						// Check for duplicates
						for _, w := range m.wallets {
							if strings.EqualFold(w.Address, newAddr) {
								m.addError = "Duplicate address - wallet already exists"
								m.addErrTime = time.Now()
								m.input.SetValue("")
								return m, nil
							}
						}

						// Create new wallet entry (name can be edited later)
						newWallet := config.WalletEntry{
							Address: newAddr,
							Name:    "",
							Active:  false,
						}
						m.wallets = append(m.wallets, newWallet)
						m.selectedWallet = len(m.wallets) - 1
						m.highlightedAddress = newAddr
						m.adding = false
						m.input.SetValue("")
						m.input.Blur()
						m.addError = ""
						// Save wallets to config
						config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.wallets, Dapps: m.dapps})
						m.addLog("success", fmt.Sprintf("Added wallet `%s`", helpers.ShortenAddr(newAddr)))
						return m, nil
					}
					// invalid -> keep input, maybe later show toast
					return m, nil

				case "esc":
					m.adding = false
					m.input.SetValue("")
					m.input.Blur()
					m.addError = ""
					return m, nil

				case "ctrl+v":
					// Paste from clipboard
					clipContent, err := clipboard.ReadAll()
					if err == nil {
						m.input.SetValue(strings.TrimSpace(clipContent))
					}
					return m, nil
				}

				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}

			// normal list controls
			switch msg.String() {
			case "up", "k":
				if m.selectedWallet > 0 {
					m.selectedWallet--
					if len(m.wallets) > 0 {
						m.highlightedAddress = m.wallets[m.selectedWallet].Address
					}
				}
				return m, nil

			case "down", "j":
				if m.selectedWallet < len(m.wallets)-1 {
					m.selectedWallet++
					if len(m.wallets) > 0 {
						m.highlightedAddress = m.wallets[m.selectedWallet].Address
					}
				}
				return m, nil

			case "a":
				m.adding = true
				m.input.Focus()
				return m, nil

			case " ":
				// Set selected wallet as active
				if len(m.wallets) > 0 {
					for i := range m.wallets {
						m.wallets[i].Active = (i == m.selectedWallet)
					}
					// Update active address to the newly activated wallet
					m.activeAddress = m.wallets[m.selectedWallet].Address
					config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.wallets, Dapps: m.dapps})
					m.addLog("info", fmt.Sprintf("Activated wallet `%s`", helpers.ShortenAddr(m.activeAddress)))
				}
				return m, nil

			case "s":
				m.activePage = pageSettings
				m.settingsMode = "list"
				return m, nil

			case "b":
				m.activePage = pageDappBrowser
				m.dappMode = "list"
				return m, nil

			case "h":
				m.activePage = pageHome
				m.createHomeMenuList()
				return m, nil

			case "esc":
				return m, tea.Quit

			case "enter":
				if len(m.wallets) == 0 {
					return m, nil
				}
				addr := m.wallets[m.selectedWallet].Address
				m.highlightedAddress = addr
				m.activePage = pageDetails
				
				// Check if we have cached details for this address
				cachedDetails, hasCached := m.detailsCache[strings.ToLower(addr)]
				if hasCached {
					// Use cached details
					m.details = cachedDetails
					m.loading = false
					m.addLog("info", fmt.Sprintf("Showing cached details for `%s`", helpers.ShortenAddr(addr)))
					return m, nil
				}
				
				// No cached data, load fresh
				m.loading = true
				m.details = walletDetails{Address: addr}
				ethAddr := common.HexToAddress(addr)
				return m, loadDetails(m.ethClient, ethAddr, m.tokenWatch)

			case "d":
				// delete selected
				if len(m.wallets) == 0 {
					return m, nil
				}
				idx := m.selectedWallet
				deletedAddr := m.wallets[idx].Address
				m.wallets = append(m.wallets[:idx], m.wallets[idx+1:]...)
				// Update selected index
				if m.selectedWallet >= len(m.wallets) && m.selectedWallet > 0 {
					m.selectedWallet--
				}
				// Update highlighted address and check if active was deleted
				if len(m.wallets) > 0 {
					m.highlightedAddress = m.wallets[m.selectedWallet].Address
					// Update active address if needed
					m.activeAddress = ""
					for _, w := range m.wallets {
						if w.Active {
							m.activeAddress = w.Address
							break
						}
					}
				} else {
					m.highlightedAddress = ""
					m.activeAddress = ""
				}
				// Save wallets to config
				config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.wallets, Dapps: m.dapps})
				m.addLog("warning", fmt.Sprintf("Deleted wallet `%s`", helpers.ShortenAddr(deletedAddr)))
				return m, nil
			}
			return m, nil

		case pageDetails:
			// Don't handle keys if nicknaming form is active
			if !m.nicknaming {
				switch msg.String() {
				case "esc", "backspace":
					m.activePage = pageWallets
					return m, nil

				case "r":
					// refresh
					addr := common.HexToAddress(m.details.Address)
					m.loading = true
					m.addLog("info", fmt.Sprintf("Refreshing details for `%s`", helpers.ShortenAddr(m.details.Address)))
					return m, loadDetails(m.ethClient, addr, m.tokenWatch)

				case "n":
					// nickname
					m.nicknaming = true
					m.createNicknameForm()
					return m, nil
				}
			}

		case pageDappBrowser:
			// Only handle list mode controls here (form handled at top of Update)
			if m.dappMode == "list" {
				switch msg.String() {
				case "esc", "backspace":
					m.activePage = pageWallets
					return m, nil

				case "up", "k":
					if m.selectedDappIdx > 0 {
						m.selectedDappIdx--
					}
					return m, nil

				case "down", "j":
					if m.selectedDappIdx < len(m.dapps)-1 {
						m.selectedDappIdx++
					}
					return m, nil

				case "a":
					m.dappMode = "add"
					m.createAddDappForm()
					return m, nil

				case "e":
					if len(m.dapps) > 0 {
						m.dappMode = "edit"
						m.createEditDappForm(m.selectedDappIdx)
					}
					return m, nil

				case "x":
					// Delete selected dApp
					if len(m.dapps) > 0 && m.selectedDappIdx < len(m.dapps) {
						deletedDapp := m.dapps[m.selectedDappIdx].Name
						m.dapps = append(m.dapps[:m.selectedDappIdx], m.dapps[m.selectedDappIdx+1:]...)
						if m.selectedDappIdx >= len(m.dapps) && m.selectedDappIdx > 0 {
							m.selectedDappIdx--
						}
						config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.wallets, Dapps: m.dapps})
						m.addLog("warning", fmt.Sprintf("Deleted dApp `%s`", deletedDapp))
					}
					return m, nil
				}
			}

		case pageSettings:
			// Only handle list mode controls here (form handled at top of Update)
			if m.settingsMode == "list" {
				switch msg.String() {
				case "esc", "backspace":
					m.activePage = pageWallets
					return m, nil

				case "a":
					m.settingsMode = "add"
					m.createAddRPCForm()
					return m, nil

				case "e":
					if len(m.rpcURLs) > 0 {
						m.settingsMode = "edit"
						m.createEditRPCForm(m.selectedRPCIdx)
					}
					return m, nil

				case "d", "x":
					// Delete selected RPC
					if len(m.rpcURLs) > 0 && m.selectedRPCIdx < len(m.rpcURLs) {
						m.rpcURLs = append(m.rpcURLs[:m.selectedRPCIdx], m.rpcURLs[m.selectedRPCIdx+1:]...)
						if m.selectedRPCIdx >= len(m.rpcURLs) && m.selectedRPCIdx > 0 {
							m.selectedRPCIdx--
						}
						config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.wallets, Dapps: m.dapps})
					}
					return m, nil

				case "up", "k":
					if m.selectedRPCIdx > 0 {
						m.selectedRPCIdx--
					}
					return m, nil

				case "down", "j":
					if m.selectedRPCIdx < len(m.rpcURLs)-1 {
						m.selectedRPCIdx++
					}
					return m, nil

				case "enter", " ":
					// Set as active
					if len(m.rpcURLs) > 0 && m.selectedRPCIdx < len(m.rpcURLs) {
						for i := range m.rpcURLs {
							m.rpcURLs[i].Active = (i == m.selectedRPCIdx)
						}
						m.rpcURL = m.rpcURLs[m.selectedRPCIdx].URL
						config.Save(m.configPath, config.Config{RPCURLs: m.rpcURLs, Wallets: m.wallets, Dapps: m.dapps})
						// Set connecting state and reconnect with new RPC
						m.rpcConnecting = true
						m.rpcConnected = false
						return m, connectRPC(m.rpcURL)
					}
					return m, nil
				}
			}
		}

	case tea.MouseMsg:
		if msg.Type == tea.MouseLeft {
			// Check if click is on any registered clickable address
			for _, area := range m.clickableAreas {
				if msg.X >= area.X && msg.X < area.X+area.Width &&
					msg.Y >= area.Y && msg.Y < area.Y+area.Height {
					// If on details page and clicking same address, copy to clipboard
					if m.activePage == pageDetails && area.Address == m.details.Address {
						return m, copyToClipboard(area.Address)
					}
					// Otherwise navigate to wallet details
					// Find wallet index
					for i, w := range m.wallets {
						if strings.EqualFold(w.Address, area.Address) {
							m.selectedWallet = i
							break
						}
					}
					m.highlightedAddress = area.Address
					m.activePage = pageDetails
					m.loading = true
					m.details = walletDetails{Address: area.Address}
					ethAddr := common.HexToAddress(area.Address)
					return m, loadDetails(m.ethClient, ethAddr, m.tokenWatch)
				}
			}

			// Legacy: handle address click on details page if no area matched
			if m.activePage == pageDetails && m.details.Address != "" {
				if msg.Y == m.addressLineY {
					return m, copyToClipboard(m.details.Address)
				}
			}
		}

	case clipboardCopiedMsg:
		m.copiedMsg = "✓ Copied address to clipboard"
		m.copiedMsgTime = time.Now()
		return m, clearClipboardMsg()

	default:
		// Clear clipboard message after timeout
		if msg, ok := msg.(struct{ clearClipboard bool }); ok && msg.clearClipboard {
			if time.Since(m.copiedMsgTime) >= 2*time.Second {
				m.copiedMsg = ""
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// -------------------- VIEW --------------------

func (m model) globalHeader() string {
	// Active Address (the one marked with ★)
	var addrDisplay string
	if m.activeAddress != "" {
		addrDisplay = lipgloss.NewStyle().
			Foreground(cAccent2).
			Bold(true).
			Render("Active Address: " + helpers.FadeString(helpers.ShortenAddr(m.activeAddress), "#F25D94", "#EDFF82"))
	} else {
		addrDisplay = lipgloss.NewStyle().
			Foreground(cMuted).
			Render("Active Address: No selection")
	}

	// RPC Status with green dot
	var statusIcon string
	var statusColor lipgloss.Color
	var statusText string

	if m.rpcURL == "" {
		statusIcon = "○"
		statusColor = lipgloss.Color("#c01c28")
		statusText = "No RPC"
	} else if m.rpcConnecting {
		statusIcon = "○"
		statusColor = lipgloss.Color("#c01c28")
		statusText = "Connecting..."
	} else if !m.rpcConnected {
		statusIcon = "○"
		statusColor = lipgloss.Color("#c01c28")
		statusText = "Connection Failed"
	} else {
		statusIcon = "●"
		statusColor = cAccent
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

	// Calculate spacing
	availableWidth := max(0, m.w-8) // Account for panel padding
	addrWidth := lipgloss.Width(addrDisplay)
	rpcWidth := lipgloss.Width(rpcDisplay)
	spacerWidth := max(2, availableWidth-addrWidth-rpcWidth)

	var headerLine string
	if spacerWidth < 2 {
		// Not enough space, stack vertically
		headerLine = addrDisplay + "\n" + rpcDisplay
	} else {
		// Layout horizontally: Address <space> RPC Status
		spacer := strings.Repeat(" ", spacerWidth)
		headerLine = addrDisplay + spacer + rpcDisplay
	}

	// Add separator line
	separator := lipgloss.NewStyle().
		Foreground(cBorder).
		Render(strings.Repeat("─", max(0, m.w-8)))

	return headerLine + "\n" + separator
}

func (m model) View() string {
	// Clear clickable areas for fresh render
	m.clickableAreas = nil

	// Render global header outside of page content
	globalHdr := m.globalHeader()
	headerPanel := panelStyle.Width(max(0, m.w-2)).Render(globalHdr)

	// Register global header address as clickable (approximate position)
	if m.activeAddress != "" {
		// Header address is at approximately (4, 1) accounting for panel padding
		m.clickableAreas = append(m.clickableAreas, clickableArea{
			X:       4,
			Y:       1,
			Width:   42, // Ethereum address length
			Height:  1,
			Address: m.activeAddress,
		})
	}

	var pageContent string
	var nav string

	switch m.activePage {
	case pageHome:
		homeContent := m.renderHome()
		pageContent = panelStyle.Width(max(0, m.w-2)).Render(homeContent)
		nav = m.navHome()

	case pageWallets:
		header := titleStyle.Render("Account List")
		subtitle := lipgloss.NewStyle().Foreground(cMuted).Render("Browse accounts and addresses")

		// Render wallet list using lipgloss
		var listItems []string
		if len(m.wallets) == 0 {
			listItems = append(listItems, lipgloss.NewStyle().Foreground(cMuted).Render("No wallets added yet. Press 'a' to add one."))
		} else {
			// Starting Y position accounting for:
			// - global header panel (varies, ~5 lines)
			// - page content panel padding (1 line top)
			// - title line (1)
			// - subtitle (1)
			// - blank line (1)
			// Total ~9 lines from top
			currentY := 9
			var fullAddr string
			var shortAddr string
			for i, wallet := range m.wallets {
				var itemStyle lipgloss.Style
				var marker string
				if i == m.selectedWallet {
					marker = lipgloss.NewStyle().Foreground(cAccent2).Bold(true).Render("▶ ")
					itemStyle = lipgloss.NewStyle().Foreground(cAccent2).Bold(true)
					fullAddr = lipgloss.NewStyle().Foreground(cText).Render(wallet.Address)
					shortAddr = helpers.ShortenAddr(wallet.Address)

				} else {
					marker = "  "
					itemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#e1a2aa"))
					fullAddr = lipgloss.NewStyle().Foreground(lipgloss.Color("#ba3fd7")).Render(helpers.FadeString(wallet.Address, "#7D5AFC", "#FF87D7"))
					shortAddr = helpers.FadeString(helpers.ShortenAddr(wallet.Address), "#F25D94", "#EDFF82")
				}

				// Add name if present
				if wallet.Name != "" {
					shortAddr = wallet.Name + " - " + shortAddr
				}
				// Add active indicator
				if wallet.Active {
					shortAddr = "✓ " + shortAddr
				}
				listItems = append(listItems, marker+itemStyle.Render(shortAddr)+"\n  "+fullAddr)

				// Register both short and full address lines as clickable
				// Short address line
				m.clickableAreas = append(m.clickableAreas, clickableArea{
					X:       4,
					Y:       currentY,
					Width:   lipgloss.Width(shortAddr) + 2,
					Height:  1,
					Address: wallet.Address,
				})
				currentY++

				// Full address line
				m.clickableAreas = append(m.clickableAreas, clickableArea{
					X:       4,
					Y:       currentY,
					Width:   42, // Full Ethereum address width
					Height:  1,
					Address: wallet.Address,
				})
				currentY += 2 // Account for blank line between items
			}
		}
		listView := strings.Join(listItems, "\n\n")

		// Status bar
		statusBar := lipgloss.NewStyle().Foreground(cMuted).Render(
			fmt.Sprintf("%d wallets", len(m.wallets)),
		)

		var addBoxView string
		if m.adding {
			inputView := m.input.View() + "\n" +
				hotkeyStyle.Render("Enter") + " save   " +
				hotkeyStyle.Render("Esc") + " cancel   " +
				hotkeyStyle.Render("Ctrl+V") + " paste"

			// Show error message if present and recent
			if m.addError != "" && time.Since(m.addErrTime) < 3*time.Second {
				errorStyle := lipgloss.NewStyle().Foreground(cWarn).Bold(true)
				inputView += "\n" + errorStyle.Render(m.addError)
			}

			addBoxView = "\n\n" + panelStyle.
				BorderForeground(cAccent2).
				Render(inputView)
		}

		walletsContent := header + "\n" + subtitle + "\n\n" + listView + "\n\n" + statusBar + addBoxView
		pageContent = panelStyle.Width(max(0, m.w-2)).Render(walletsContent)
		nav = m.navWallets()

	case pageDetails:
		detailsContent := m.detailsView()
		
		// Render details panel only (dApp browser moved to its own page)
		pageContent = panelStyle.Width(max(0, m.w-2)).Render(detailsContent)
		
		// Calculate address line Y position (accounting for panel padding + global header)
		m.addressLineY = 5 // 1 for panel padding + 2 for global header + 1 for blank line + 1 for title line
		
		nav = m.navDetails()

	case pageDappBrowser:
		dappBrowserContent := m.dAppBrowserView()
		pageContent = panelStyle.Width(max(0, m.w-2)).Render(dappBrowserContent)
		nav = m.navDappBrowser()

	case pageSettings:
		settingsContent := m.settingsView()
		pageContent = panelStyle.Width(max(0, m.w-2)).Render(settingsContent)
		nav = m.navSettings()
	}

	// Render log panel only if enabled
	var logPanel string
	if m.logEnabled {
		logPanel = m.renderLogPanel()
		content := lipgloss.JoinVertical(lipgloss.Left, headerPanel, pageContent, nav, logPanel)
		return appStyle.Render(content)
	}

	// Use lipgloss to join sections vertically (without log panel)
	content := lipgloss.JoinVertical(lipgloss.Left, headerPanel, pageContent, nav)
	return appStyle.Render(content)
}

func (m model) renderHome() string {
	header := titleStyle.Render("Main Menu")
	subtitle := lipgloss.NewStyle().Foreground(cMuted).Render("Select a view to navigate to")
	if m.homeMenuReady {
		return header + "\n" + subtitle + "\n\n" + m.homeMenu.View()
	}
	return header + "\n" + subtitle + "\n\n" + "Loading menu..."
}

func (m model) navHome() string {
	left := strings.Join([]string{
		key("↑/↓") + " select",
		key("Enter") + " go",
		key("/") + " search",
		key("l") + " debug log",
		key("Esc") + " quit",
	}, "   ")

	return navStyle.Width(max(0, m.w-2)).Render(left)
}

func (m model) navWallets() string {
	left := strings.Join([]string{
		key("↑/↓") + " move",
		key("Enter") + " open",
		key("Space") + " activate",
		key("a") + " add",
		key("d") + " delete",
		key("h") + " home",
		key("s") + " settings",
		key("b") + " dApps",
		key("l") + " debug log",
		key("Esc") + " quit",
	}, "   ")

	return navStyle.Width(max(0, m.w-2)).Render(left)
}

func (m model) navDetails() string {
	var left string
	left = strings.Join([]string{
		key("r") + " refresh",
		key("n") + " nickname",
		key("l") + " debug log",
		key("Esc") + " back",
	}, "   ")

	right := helpRightStyle.Render(
		fmt.Sprintf("Loaded: %s", helpers.LoadedAt(m.details.LoadedAt, m.loading)),
	)

	return navStyle.Width(max(0, m.w-2)).Render(
		lipgloss.JoinHorizontal(lipgloss.Top,
			left,
			lipgloss.NewStyle().Width(max(0, m.w-lipgloss.Width(left)-4)).Align(lipgloss.Right).Render(right),
		),
	)
}

func (m model) navDappBrowser() string {
	var left string
	if m.dappMode == "add" || m.dappMode == "edit" {
		left = strings.Join([]string{
			key("Esc") + " cancel",
		}, "   ")
	} else {
		left = strings.Join([]string{
			key("↑/↓") + " select",
			key("a") + " add",
			key("e") + " edit",
			key("x") + " delete",
			key("l") + " debug log",
			key("Esc") + " back",
		}, "   ")
	}

	return navStyle.Width(max(0, m.w-2)).Render(left)
}

func (m model) detailsView() string {
	h := titleStyle.Render("Account Details")

	// Show form if in nicknaming mode
	if m.nicknaming && m.form != nil {
		return h + "\n\n" + m.form.View()
	}

	// Find nickname for current wallet
	var nickname string
	for _, w := range m.wallets {
		if strings.EqualFold(w.Address, m.details.Address) {
			nickname = w.Name
			break
		}
	}

	// Make address clickable with underline hint
	addrStyle := lipgloss.NewStyle().Foreground(cMuted).Underline(true)
	sub := addrStyle.Render(m.details.Address)

	// Add nickname if it exists
	if nickname != "" {
		nicknameStyle := lipgloss.NewStyle().Foreground(cAccent2).Italic(true)
		sub = nicknameStyle.Render("\""+nickname+"\"") + "  " + sub
	}

	if m.copiedMsg != "" {
		sub += "  " + lipgloss.NewStyle().Foreground(cAccent).Render(m.copiedMsg)
	}

	if m.loading {
		return h + "\n" + sub + "\n\n" + m.spin.View() + " fetching balances…"
	}

	if m.details.ErrMessage != "" {
		msg := lipgloss.NewStyle().Foreground(cWarn).Render("⚠ " + m.details.ErrMessage)
		hint := hotkeyStyle.Render("Tip: set ") + lipgloss.NewStyle().Foreground(cAccent).Render("ETH_RPC_URL") +
			hotkeyStyle.Render(" then press ") + key("r") + hotkeyStyle.Render(" to refresh.")
		return h + "\n" + sub + "\n\n" + msg + "\n\n" + hint
	}

	ethLine := fmt.Sprintf("%s  %s",
		lipgloss.NewStyle().Foreground(cAccent2).Bold(true).Render("ETH"),
		lipgloss.NewStyle().Foreground(cText).Render(helpers.FormatETH(m.details.EthWei)),
	)

	lines := []string{h, sub, "", ethLine, ""}

	if len(m.details.Tokens) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(cMuted).Render("No watched token balances found (non-zero)."))
		lines = append(lines, hotkeyStyle.Render("Edit tokenWatch in code (or add config) to track more tokens."))
		return strings.Join(lines, "\n")
	}

	lines = append(lines, lipgloss.NewStyle().Foreground(cMuted).Render("Tokens (watchlist)"))

	// table-ish rendering
	for _, t := range m.details.Tokens {
		row := fmt.Sprintf("%-6s  %s",
			lipgloss.NewStyle().Foreground(cAccent).Render(t.Symbol),
			lipgloss.NewStyle().Foreground(cText).Render(helpers.FormatToken(t.Balance, t.Decimals, t.Symbol)),
		)
		lines = append(lines, row)
	}

	return strings.Join(lines, "\n")
}

func (m model) dAppBrowserView() string {
	h := titleStyle.Render("dApp Browser")

	// Show form if in add/edit mode
	if (m.dappMode == "add" || m.dappMode == "edit") && m.form != nil {
		return h + "\n\n" + m.form.View()
	}

	lines := []string{h, ""}

	if len(m.dapps) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(cMuted).Render("No dApps configured."))
		lines = append(lines, "")
		lines = append(lines, hotkeyStyle.Render("Press ") + key("a") + hotkeyStyle.Render(" to add your first dApp."))
	} else {
		lines = append(lines, lipgloss.NewStyle().Foreground(cMuted).Render("Available dApps:"))
		lines = append(lines, "")

		for i, dapp := range m.dapps {
			var marker string
			nameStyle := lipgloss.NewStyle().Foreground(cText)
			addrStyle := lipgloss.NewStyle().Foreground(cMuted)

			if i == m.selectedDappIdx {
				nameStyle = nameStyle.Background(cPanel).Foreground(cAccent2).Bold(true)
				addrStyle = addrStyle.Background(cPanel)
				marker = lipgloss.NewStyle().Foreground(cAccent2).Render("▶ ")
			} else {
				marker = "  "
			}

			icon := dapp.Icon
			if icon == "" {
				icon = "🌐"
			}

			line := marker + icon + " " + nameStyle.Render(dapp.Name)
			lines = append(lines, line)
			lines = append(lines, "  "+addrStyle.Render(dapp.Address))
			lines = append(lines, "")
		}
	}

	return strings.Join(lines, "\n")
}

func (m model) settingsView() string {
	h := titleStyle.Render("RPC Settings")

	if m.settingsMode == "add" || m.settingsMode == "edit" {
		if m.form != nil {
			return h + "\n\n" + m.form.View()
		}
	}

	// List mode
	lines := []string{h, ""}

	if len(m.rpcURLs) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(cMuted).Render("No RPC URLs configured."))
		lines = append(lines, "")
		lines = append(lines, hotkeyStyle.Render("Press ")+key("a")+hotkeyStyle.Render(" to add your first RPC URL."))
	} else {
		lines = append(lines, lipgloss.NewStyle().Foreground(cMuted).Render("Configured RPC Endpoints:"))
		lines = append(lines, "")

		for i, rpc := range m.rpcURLs {
			var marker string
			if rpc.Active {
				marker = lipgloss.NewStyle().Foreground(cAccent).Render("● ")
			} else {
				marker = lipgloss.NewStyle().Foreground(cMuted).Render("○ ")
			}

			nameStyle := lipgloss.NewStyle().Foreground(cText)
			urlStyle := lipgloss.NewStyle().Foreground(cMuted)

			if i == m.selectedRPCIdx {
				nameStyle = nameStyle.Background(cPanel).Foreground(cAccent2).Bold(true)
				urlStyle = urlStyle.Background(cPanel)
				marker = lipgloss.NewStyle().Foreground(cAccent2).Render("▶ ")
			}

			line := marker + nameStyle.Render(rpc.Name)
			lines = append(lines, line)
			lines = append(lines, "  "+urlStyle.Render(rpc.URL))
			lines = append(lines, "")
		}
	}

	return strings.Join(lines, "\n")
}

func (m model) navSettings() string {
	var left string
	if m.settingsMode == "add" || m.settingsMode == "edit" {
		left = strings.Join([]string{
			key("l") + " debug log",
			key("Esc") + " cancel",
		}, "   ")
	} else {
		left = strings.Join([]string{
			key("↑/↓") + " select",
			key("Enter") + " activate",
			key("a") + " add",
			key("e") + " edit",
			key("d") + " delete",
			key("l") + " debug log",
			key("Esc") + " back",
		}, "   ")
	}

	return navStyle.Width(max(0, m.w-2)).Render(left)
}

func (m model) renderLogPanel() string {
	title := lipgloss.NewStyle().
		Foreground(cAccent2).
		Bold(true).
		Render("Debug Log")

	border := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1).
		Width(max(0, m.w-4))

	if !m.logReady {
		return border.Render(title + "\n\n" + "initializing...")
	}

	return border.Render(title + "\n\n" + m.logViewport.View())
}

func key(s string) string {
	return hotkeyKeyStyle.Render(s)
}

func rpcStatus(url string, c *rpc.Client) string {
	if url == "" {
		return "not set"
	}
	if c == nil {
		return "connecting/failed"
	}
	return "connected"
}

func rainbow(base lipgloss.Style, s string, colors []color.Color) string {
	var str string
	for i, ss := range s {
		color, _ := colorful.MakeColor(colors[i%len(colors)])
		str = str + base.Foreground(lipgloss.Color(color.Hex())).Render(string(ss))
	}
	return str
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// -------------------- MAIN --------------------

func main() {
	m := newModel()
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}
