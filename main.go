package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/huh"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/muesli/gamut"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// -------------------- THEME (Lip Gloss) --------------------

var (
	cBg      = lipgloss.Color("#0B0F14") // near-black
	cPanel   = lipgloss.Color("#0F1720") // slightly lighter
	cBorder  = lipgloss.Color("#874BFD")
	cMuted   = lipgloss.Color("#8AA0B6")
	cText    = lipgloss.Color("#D6E2F0")
	cAccent  = lipgloss.Color("#7EE787") // green-ish
	cAccent2 = lipgloss.Color("#79C0FF") // blue-ish
	cWarn    = lipgloss.Color("#FFA657") // orange

	blends    = gamut.Blends(lipgloss.Color("#F25D94"), lipgloss.Color("#EDFF82"), 50)

	appStyle = lipgloss.NewStyle().
			Background(cBg).
			Foreground(cText)

	titleStyle = lipgloss.NewStyle().
			Foreground(cAccent2).
			Bold(true)

	panelStyle = lipgloss.NewStyle().
			Background(cPanel).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(cBorder).
			Padding(1, 2)

	navStyle = lipgloss.NewStyle().
			Background(cPanel).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(cBorder).
			Padding(0, 1)

	hotkeyStyle = lipgloss.NewStyle().
			Foreground(cMuted)

	hotkeyKeyStyle = lipgloss.NewStyle().
			Foreground(cAccent).
			Bold(true)

	helpRightStyle = lipgloss.NewStyle().
			Foreground(cMuted)
)

// -------------------- DATA TYPES --------------------

type page int

const (
	pageWallets page = iota
	pageDetails
	pageSettings
)

// clickableArea represents a clickable region on screen for addresses
type clickableArea struct {
	X, Y         int    // top-left position
	Width, Height int    // dimensions
	Address      string // wallet address to navigate to
}

type walletItem struct {
	addr string
}

func (w walletItem) Title() string       { return shortenAddr(w.addr) }
func (w walletItem) Description() string { return w.addr }
func (w walletItem) FilterValue() string { return w.addr }

type tokenBalance struct {
	Symbol   string
	Decimals uint8
	Balance  *big.Int
}

type details struct {
	Address    string
	EthWei     *big.Int
	Tokens     []tokenBalance
	LoadedAt   time.Time
	ErrMessage string
}

type rpcURL struct {
	Name   string
	URL    string
	Active bool
}

type walletEntry struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
	Active  bool   `json:"active"`
}

type config struct {
	RPCURLs []rpcURL       `json:"rpc_urls"`
	Wallets []walletEntry `json:"wallets"`
}

// -------------------- MODEL --------------------

type model struct {
	w, h int

	activePage page

	// main list
	wallets        []walletEntry
	selectedWallet int

	// add-wallet input
	adding    bool
	input     textinput.Model
	addError  string // error message when adding wallet (e.g., duplicate)
	addErrTime time.Time // time when error was shown

	// details state
	spin      spinner.Model
	loading   bool
	details   details
	rpcURL    string
	ethClient *ethclient.Client

	// token watchlist (simple starter set)
	// You can expand this (or load from config).
	tokenWatch []watchedToken

	// clipboard feedback
	copiedMsg      string
	copiedMsgTime  time.Time
	addressLineY   int // Y position of the address line in details view

	// settings state
	settingsMode   string // "list", "add", "edit"
	rpcURLs        []rpcURL
	selectedRPCIdx int
	form           *huh.Form
	configPath     string

	// currently highlighted address in wallet list
	highlightedAddress string
	// active address (the one marked with ★)
	activeAddress string
	
	// clickable areas for mouse support
	clickableAreas []clickableArea
}

type watchedToken struct {
	Symbol   string
	Decimals uint8
	Address  common.Address
}

// -------------------- INIT --------------------

func newModel() model {
	// config path
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".charm-wallet-config.json")

	// load config
	cfg := loadConfig(configPath)
	
	// Load wallet entries from config
	wallets := cfg.Wallets
	if wallets == nil {
		wallets = []walletEntry{}
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
	in.Placeholder = "0x…"
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

	// rpc
	rpc := strings.TrimSpace(os.Getenv("ETH_RPC_URL"))
	
	// If no RPC in config but ENV is set, use ENV
	if len(cfg.RPCURLs) == 0 && rpc != "" {
		cfg.RPCURLs = []rpcURL{{Name: "Default", URL: rpc, Active: true}}
	}
	
	// Find active RPC
	activeRPC := rpc
	for _, r := range cfg.RPCURLs {
		if r.Active {
			activeRPC = r.URL
			break
		}
	}

	// starter token watchlist (Mainnet):
	// USDC, USDT, DAI, WETH
	watch := []watchedToken{
		{Symbol: "WETH", Decimals: 18, Address: common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")},
		{Symbol: "USDC", Decimals: 6, Address: common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")},
		{Symbol: "USDT", Decimals: 6, Address: common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7")},
		{Symbol: "DAI", Decimals: 18, Address: common.HexToAddress("0x6B175474E89094C44Da98b954EedeAC495271d0F")},
	}

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
		return tea.Batch(m.spin.Tick, connectRPC(m.rpcURL))
	}
	return m.spin.Tick
}

// -------------------- COMMANDS / MESSAGES --------------------

type clipboardCopiedMsg struct{}

type rpcConnectedMsg struct {
	client *ethclient.Client
	err    error
}

func connectRPC(url string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		c, err := ethclient.DialContext(ctx, url)
		return rpcConnectedMsg{client: c, err: err}
	}
}

type detailsLoadedMsg struct {
	d   details
	err error
}

func loadDetails(client *ethclient.Client, addr common.Address, watch []watchedToken) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()

		d := details{
			Address:  addr.Hex(),
			EthWei:   big.NewInt(0),
			LoadedAt: time.Now(),
		}

		if client == nil {
			d.ErrMessage = "No RPC client (set ETH_RPC_URL)."
			return detailsLoadedMsg{d: d, err: nil}
		}

		// ETH balance
		wei, err := client.BalanceAt(ctx, addr, nil)
		if err != nil {
			d.ErrMessage = "Failed to load ETH balance."
			return detailsLoadedMsg{d: d, err: err}
		}
		d.EthWei = wei

		// ERC20 balances (simple sequential calls)
		// For speed later: replace with Multicall3 batching.
		var toks []tokenBalance
		for _, t := range watch {
			bal, err := erc20BalanceOf(ctx, client, t.Address, addr)
			if err != nil {
				// skip token silently; you can surface in UI if desired
				continue
			}
			if bal.Sign() > 0 {
				toks = append(toks, tokenBalance{
					Symbol:   t.Symbol,
					Decimals: t.Decimals,
					Balance:  bal,
				})
			}
		}

		sort.Slice(toks, func(i, j int) bool {
			return strings.ToLower(toks[i].Symbol) < strings.ToLower(toks[j].Symbol)
		})
		d.Tokens = toks

		return detailsLoadedMsg{d: d, err: nil}
	}
}

// Minimal ERC20 balanceOf via eth_call.
var (
	// balanceOf(address) methodID = keccak256("balanceOf(address)")[:4]
	balanceOfSelector = []byte{0x70, 0xa0, 0x82, 0x31}
)

func erc20BalanceOf(ctx context.Context, client *ethclient.Client, token common.Address, owner common.Address) (*big.Int, error) {
	// calldata = selector + 32-byte left-padded address
	padded := common.LeftPadBytes(owner.Bytes(), 32)
	data := append(balanceOfSelector, padded...)

	// go-ethereum CallContract wants a CallMsg
	msg := ethereum.CallMsg{
		To:   &token,
		Data: data,
	}
	out, err := client.CallContract(ctx, msg, nil)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(out), nil
}

// Tiny helper to avoid importing the full ethereum package name in the user’s face.
type callMsg struct {
	To   *common.Address
	Data []byte
}

func ethereumCallMsg(to common.Address, data []byte) callMsg {
	return callMsg{To: &to, Data: data}
}

// Adapter: ethclient.CallContract expects ethereum.CallMsg. We mirror fields and cast using a small trick:
// We'll just re-create the struct using the same field names via an inline struct that matches.
func (c callMsg) toEthereumCallMsg() interface{} {
	// This shape matches ethereum.CallMsg fields that CallContract uses: To, Data.
	return struct {
		To   *common.Address
		Data []byte
	}{To: c.To, Data: c.Data}
}

// We need to satisfy the actual type at compile time. The easiest is to import the ethereum package.
// But to keep this file compact, we’ll do it properly: import "github.com/ethereum/go-ethereum".
// (We’ll add it below as a normal import via alias.)
//
// NOTE: Because Go requires exact types, we’ll do the real thing:
func init() {}

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

// -------------------- CONFIG FUNCTIONS --------------------

func loadConfig(path string) config {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{RPCURLs: []rpcURL{}}
	}
	
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{RPCURLs: []rpcURL{}}
	}
	
	return cfg
}

func saveConfig(path string, cfg config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (m *model) createAddRPCForm() {
	name := ""
	url := ""
	
	m.form = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("RPC Name").
				Description("A friendly name for this RPC endpoint").
				Value(&name).
				Placeholder("My Infura Node"),
			
			huh.NewInput().
				Title("RPC URL").
				Description("The complete RPC URL (https://...)").
				Value(&url).
				Placeholder("https://mainnet.infura.io/v3/..."),
		),
	).WithTheme(huh.ThemeCatppuccin())
	
	// Store values for after form completion
	m.form.State = huh.StateNormal
}

func (m *model) createEditRPCForm(idx int) {
	if idx < 0 || idx >= len(m.rpcURLs) {
		return
	}
	
	rpc := m.rpcURLs[idx]
	name := rpc.Name
	url := rpc.URL
	
	m.form = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("RPC Name").
				Value(&name).
				Placeholder("My Node"),
			
			huh.NewInput().
				Title("RPC URL").
				Value(&url).
				Placeholder("https://..."),
		),
	).WithTheme(huh.ThemeCatppuccin())
}

// -------------------- UPDATE --------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case rpcConnectedMsg:
		if msg.err != nil {
			// keep running without client
			m.ethClient = nil
		} else {
			m.ethClient = msg.client
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case detailsLoadedMsg:
		m.loading = false
		m.details = msg.d
		if msg.err != nil && m.details.ErrMessage == "" {
			m.details.ErrMessage = "Failed to load wallet details."
		}
		return m, nil

	case tea.KeyMsg:
		// global keys
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}

		// page-specific behavior
		switch m.activePage {

		case pageWallets:
			// adding flow
			if m.adding {
				switch msg.String() {
				case "enter":
					val := strings.TrimSpace(m.input.Value())
					if isValidEthAddress(val) {
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
						newWallet := walletEntry{
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
						saveConfig(m.configPath, config{RPCURLs: m.rpcURLs, Wallets: m.wallets})
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
					saveConfig(m.configPath, config{RPCURLs: m.rpcURLs, Wallets: m.wallets})
				}
				return m, nil

			case "s":
				m.activePage = pageSettings
				m.settingsMode = "list"
				return m, nil

			case "enter":
				if len(m.wallets) == 0 {
					return m, nil
				}
				addr := m.wallets[m.selectedWallet].Address
				m.highlightedAddress = addr
				m.activePage = pageDetails
				m.loading = true
				m.details = details{Address: addr}
				ethAddr := common.HexToAddress(addr)
				return m, loadDetails(m.ethClient, ethAddr, m.tokenWatch)

			case "d":
				// delete selected
				if len(m.wallets) == 0 {
					return m, nil
				}
				idx := m.selectedWallet
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
				saveConfig(m.configPath, config{RPCURLs: m.rpcURLs, Wallets: m.wallets})
				return m, nil
			}
			return m, nil

		case pageDetails:
			switch msg.String() {
			case "esc", "backspace":
				m.activePage = pageWallets
				return m, nil

			case "r":
				// refresh
				addr := common.HexToAddress(m.details.Address)
				m.loading = true
				return m, loadDetails(m.ethClient, addr, m.tokenWatch)
			}

		case pageSettings:
			// Handle form input if in add/edit mode
			if m.settingsMode == "add" || m.settingsMode == "edit" {
				if m.form != nil {
					form, cmd := m.form.Update(msg)
					if f, ok := form.(*huh.Form); ok {
						m.form = f
						
						// Check if form is completed
						if m.form.State == huh.StateCompleted {
							// Extract values from form
							if m.settingsMode == "add" {
								// Get form values
								name := m.form.GetString("RPC Name")
								url := m.form.GetString("RPC URL")
								if name != "" && url != "" {
									m.rpcURLs = append(m.rpcURLs, rpcURL{Name: name, URL: url, Active: false})
									saveConfig(m.configPath, config{RPCURLs: m.rpcURLs, Wallets: m.wallets})
								}
							} else if m.settingsMode == "edit" {
								name := m.form.GetString("RPC Name")
								url := m.form.GetString("RPC URL")
								if m.selectedRPCIdx >= 0 && m.selectedRPCIdx < len(m.rpcURLs) {
									m.rpcURLs[m.selectedRPCIdx].Name = name
									m.rpcURLs[m.selectedRPCIdx].URL = url
									saveConfig(m.configPath, config{RPCURLs: m.rpcURLs, Wallets: m.wallets})
								}
							}
							m.settingsMode = "list"
							m.form = nil
						}
					}
					return m, cmd
				}
			}

			// List mode controls
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
						saveConfig(m.configPath, config{RPCURLs: m.rpcURLs, Wallets: m.wallets})
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
						saveConfig(m.configPath, config{RPCURLs: m.rpcURLs, Wallets: m.wallets})
						// Reconnect with new RPC
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
					m.details = details{Address: area.Address}
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
			Render("Active Address: " + shortenAddr(m.activeAddress))
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
		statusColor = cWarn
		statusText = "No RPC"
	} else if m.ethClient == nil {
		statusIcon = "○"
		statusColor = cWarn
		statusText = "Connecting..."
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
	headerPanel := panelStyle.Render(globalHdr)
	
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
	case pageWallets:
		header := titleStyle.Render("Charm Wallets")
		subtitle := lipgloss.NewStyle().Foreground(cMuted).Render("Ethereum account holdings browser")

		// Render wallet list using lipgloss
		var listItems []string
		var foregroundFullAddrColor = cText
		if len(m.wallets) == 0 {
			listItems = append(listItems, lipgloss.NewStyle().Foreground(cMuted).Render("No wallets added yet. Press 'a' to add one."))
		} else {
			// Starting Y position: headerPanel (3) + title line (1) + subtitle (1) + padding (2) = 7
			currentY := 7
			
			for i, wallet := range m.wallets {
				var itemStyle lipgloss.Style
				var marker string
				if i == m.selectedWallet {
					marker = lipgloss.NewStyle().Foreground(cAccent2).Bold(true).Render("▶ ")
					itemStyle = lipgloss.NewStyle().Foreground(cAccent2).Bold(true)
					foregroundFullAddrColor = cText
				} else {
					marker = "  "
					itemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#e1a2aa"))
					foregroundFullAddrColor = lipgloss.Color("#ba3fd7")
				}
				shortAddr := shortenAddr(wallet.Address)
				// Add name if present
				if wallet.Name != "" {
					shortAddr = wallet.Name + " - " + shortAddr
				}
				// Add active indicator
				if wallet.Active {
					shortAddr = "✓ " + shortAddr
				}
				fullAddr := lipgloss.NewStyle().Foreground(foregroundFullAddrColor).Render(wallet.Address)
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
		pageContent = panelStyle.Render(walletsContent)
		nav = m.navWallets()

	case pageDetails:
		detailsContent := m.detailsView()
		// Calculate address line Y position (accounting for panel padding + global header)
		m.addressLineY = 5 // 1 for panel padding + 2 for global header + 1 for blank line + 1 for title line
		pageContent = panelStyle.Render(detailsContent)
		nav = m.navDetails()

	case pageSettings:
		settingsContent := m.settingsView()
		pageContent = panelStyle.Render(settingsContent)
		nav = m.navSettings()
	}

	// Use lipgloss to join sections vertically
	content := lipgloss.JoinVertical(lipgloss.Left, headerPanel, pageContent, nav)
	return appStyle.Render(content)
}

func (m model) navWallets() string {
	left := strings.Join([]string{
		key("↑/↓") + " move",
		key("Enter") + " open",
		key("Space") + " activate",
		key("a") + " add",
		key("d") + " delete",
		key("s") + " settings",
		key("q") + " quit",
	}, "   ")

	right := helpRightStyle.Render(
		fmt.Sprintf("RPC: %s", rpcStatus(m.rpcURL, m.ethClient)),
	)

	return navStyle.Width(max(0, m.w-2)).Render(
		lipgloss.JoinHorizontal(lipgloss.Top,
			left,
			lipgloss.NewStyle().Width(max(0, m.w-lipgloss.Width(left)-4)).Align(lipgloss.Right).Render(right),
		),
	)
}

func (m model) navDetails() string {
	left := strings.Join([]string{
		key("Esc") + " back",
		key("r") + " refresh",
		key("click addr") + " copy",
		key("q") + " quit",
	}, "   ")

	right := helpRightStyle.Render(
		fmt.Sprintf("RPC: %s  ·  Loaded: %s", rpcStatus(m.rpcURL, m.ethClient), loadedAt(m.details.LoadedAt, m.loading)),
	)

	return navStyle.Width(max(0, m.w-2)).Render(
		lipgloss.JoinHorizontal(lipgloss.Top,
			left,
			lipgloss.NewStyle().Width(max(0, m.w-lipgloss.Width(left)-4)).Align(lipgloss.Right).Render(right),
		),
	)
}

func (m model) detailsView() string {
	h := titleStyle.Render("Wallet Details")
	// Make address clickable with underline hint
	addrStyle := lipgloss.NewStyle().Foreground(cMuted).Underline(true)
	sub := addrStyle.Render(m.details.Address)
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
		lipgloss.NewStyle().Foreground(cText).Render(formatETH(m.details.EthWei)),
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
			lipgloss.NewStyle().Foreground(cText).Render(formatUnits(t.Balance, t.Decimals)),
		)
		lines = append(lines, row)
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
		lines = append(lines, hotkeyStyle.Render("Press ") + key("a") + hotkeyStyle.Render(" to add your first RPC URL."))
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
			key("Esc") + " cancel",
		}, "   ")
	} else {
		left = strings.Join([]string{
			key("↑/↓") + " select",
			key("Enter") + " activate",
			key("a") + " add",
			key("e") + " edit",
			key("d") + " delete",
			key("Esc") + " back",
		}, "   ")
	}
	
	right := helpRightStyle.Render(
		fmt.Sprintf("RPC: %s", rpcStatus(m.rpcURL, m.ethClient)),
	)
	
	return navStyle.Width(max(0, m.w-2)).Render(
		lipgloss.JoinHorizontal(lipgloss.Top,
			left,
			lipgloss.NewStyle().Width(max(0, m.w-lipgloss.Width(left)-4)).Align(lipgloss.Right).Render(right),
		),
	)
}

func key(s string) string {
	return hotkeyKeyStyle.Render(s)
}

func rpcStatus(url string, c *ethclient.Client) string {
	if url == "" {
		return "not set"
	}
	if c == nil {
		return "connecting/failed"
	}
	return "connected"
}

func loadedAt(t time.Time, loading bool) string {
	if loading {
		return "loading…"
	}
	if t.IsZero() {
		return "—"
	}
	return t.Format("15:04:05")
}

// -------------------- HELPERS --------------------

func shortenAddr(a string) string {
	a = strings.TrimSpace(a)
	if len(a) < 12 {
		return a
	}
	return a[:6] + "…" + a[len(a)-4:]
}

var reEthAddr = regexp.MustCompile(`^(0x)?[0-9a-fA-F]{40}$`)

func isValidEthAddress(s string) bool {
	s = strings.TrimSpace(s)
	if !reEthAddr.MatchString(s) {
		return false
	}
	if !strings.HasPrefix(s, "0x") {
		s = "0x" + s
	}
	// additional sanity check
	_, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	return err == nil
}

func formatETH(wei *big.Int) string {
	if wei == nil {
		return "0"
	}
	// ETH has 18 decimals
	return formatUnits(wei, 18) + " ETH"
}

func formatUnits(amount *big.Int, decimals uint8) string {
	if amount == nil {
		return "0"
	}
	dec := int(decimals)
	if dec == 0 {
		return amount.String()
	}

	// integer + fractional splitting
	base := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)
	intPart := new(big.Int).Div(amount, base)
	fracPart := new(big.Int).Mod(amount, base)

	// show up to 6 decimals, trimmed
	maxDP := 6
	if dec < maxDP {
		maxDP = dec
	}
	fracStr := fracPart.Text(10)
	fracStr = strings.Repeat("0", dec-len(fracStr)) + fracStr
	fracStr = fracStr[:maxDP]
	fracStr = strings.TrimRight(fracStr, "0")

	if fracStr == "" {
		return intPart.String()
	}
	return fmt.Sprintf("%s.%s", intPart.String(), fracStr)
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
