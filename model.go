package main

import (
	"context"
	"image"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"image/color"

	"charm-wallet-tui/anim"
	"charm-wallet-tui/config"
	"charm-wallet-tui/helpers"
	"charm-wallet-tui/indexer"
	"charm-wallet-tui/rpc"
	"charm-wallet-tui/store"
	"charm-wallet-tui/styles"
	"charm-wallet-tui/views/scrollbar"
	"charm-wallet-tui/webcam/capture"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/ethereum/go-ethereum/common"
)

// -------------------- MODEL --------------------

// focusedPanelKind identifies which scrollable panel (if any) currently has keyboard/wheel focus.
type focusedPanelKind uint8

const (
	focusedPanelV4Events focusedPanelKind = iota
	focusedPanelLog
)

// dialogKind identifies which overlay dialog (if any) is currently visible.
// Only one dialog can be shown at a time.
type dialogKind uint8

const (
	dialogNone          dialogKind = iota
	dialogDeleteWallet             // wallet delete confirmation
	dialogDeleteRPC                // RPC endpoint delete confirmation
	dialogTxResult                 // transaction result / QR panel
	dialogPoolInfo                 // Uniswap pool info popup
	dialogAccountList              // account selector popup
	dialogTerraClaim               // Terra Nullius claim form
	dialogScanTx                   // webcam scan for signed transaction
	dialogPasteSignedTx            // paste + broadcast a signed transaction
	dialogEditWallet               // edit address + nickname for an existing wallet
	dialogAddWallet                // add a new wallet (same popup as edit)
	dialogSendTx                   // send transaction form
	dialogDeleteToken              // watched token delete confirmation
	dialogOndoPicker               // Ondo Global Markets token picker (Watched Tokens page)
)

// pasteTxPhaseKind identifies which step of the paste-signed-transaction
// flow is currently shown inside dialogPasteSignedTx.
type pasteTxPhaseKind uint8

const (
	pasteTxPhaseForm    pasteTxPhaseKind = iota // pasting + previewing the signed tx
	pasteTxPhaseSending                         // broadcasting via eth_sendRawTransaction
	pasteTxPhasePolling                         // waiting for the tx to be mined
	pasteTxPhaseResult                          // on-chain data found, awaiting dismissal
)

// model represents the application state following The Elm Architecture
type model struct {
	w, h     int
	contentW int // helpers.Max(0, w-2) — pre-computed each WindowSizeMsg

	activePage config.Page

	// main list
	accounts       []config.WalletEntry
	selectedWallet int

	// add-wallet input
	input           textinput.Model // address input
	nicknameInput   textinput.Model // nickname input
	focusedInput    int             // 0 = address, 1 = nickname
	addError        string          // error message when adding wallet (e.g., duplicate)
	addErrTime      time.Time       // time when error was shown
	ensLookupActive bool            // true if ENS lookup is in progress
	ensLookupAddr   string          // address being looked up

	// details state
	spin          *anim.Anim
	loading       bool
	details       rpc.WalletDetails
	detailsCache  map[string]rpc.WalletDetails // cache wallet details by address
	rpcURL        string
	ethClient     *rpc.Client
	rpcConnected  bool // true if RPC is successfully connected
	rpcConnecting bool // true if connection attempt is in progress

	// token watchlist (persisted to config)
	tokenWatch []rpc.WatchedToken

	// Watched Tokens page state
	selectedTokenIdx             int
	tokenFormMode                string // "list", "add", "edit"
	tokenForm                    *huh.Form
	tokenFormFields              []huh.Field
	tokenFormButtonFocused       bool
	tokenFormError               string
	tokenFormErrTime             time.Time
	tokenLookupActive            bool
	editingTokenIdx              int
	deleteTokenDialogIdx         int
	deleteTokenDialogName        string
	deleteTokenDialogYesSelected bool
	tokenListViewport            viewport.Model
	tokenListScroll              scrollbar.State

	// Ondo Global Markets token picker (dialogOndoPicker), opened from the
	// Watched Tokens page. Selecting an entry only autofills the existing
	// add-token form's address field — the on-chain symbol()/decimals()
	// lookup in submitTokenForm still runs, so nothing here is trusted
	// directly into the persisted watchlist.
	ondoPickerFilter string
	ondoPickerIdx    int

	// clipboard feedback
	copiedMsg     string
	copiedMsgTime time.Time
	addressLineY  int // Y position of the address line in details view

	// transaction result panel copy feedback
	txCopiedMsg     string
	txCopiedMsgTime time.Time

	// settings state
	settingsMode   string // "list", "add", "edit"
	rpcURLs        []config.RPCUrl
	selectedRPCIdx int
	form           *huh.Form
	formFields     []huh.Field // for click-to-focus stepping, see focusHuhField
	formButtonFocused bool // true once Tab has moved focus past the last RPC form field onto Save
	configPath     string

	// dApp browser state
	dapps           []config.DApp
	selectedDappIdx int

	// home form
	homeForm *huh.Form

	// currently highlighted address in wallet list
	highlightedAddress string
	// active address (the one marked with ★)
	activeAddress string

	// clickable areas for mouse support
	clickableAreas []config.ClickableArea

	// generic clickable/focusable region registry, rebuilt every View() call
	uiRegions       []uiRegion
	hoveredRegionID string
	focusedRegionID string

	// tracks which mouse mode is currently requested from the terminal, so
	// Update() only sends a mode-switch command on an actual transition
	mouseAllMotionActive bool

	// logger panel
	logEnabled  bool
	logger      *log.Logger
	logBuffer   *strings.Builder
	logViewport viewport.Model
	logReady    bool
	logSpinner  *anim.Anim

	// split view flag for wallets page
	detailsInWallets bool // when true, show details panel alongside wallet list

	// active overlay dialog (only one at a time)
	activeDialog dialogKind

	// edit wallet dialog state
	editingIdx int // index in m.accounts of the wallet being edited

	// delete confirmation dialog state
	deleteDialogAddr        string
	deleteDialogIdx         int
	deleteDialogYesSelected bool // true = Yes button, false = No button
	deleteRPCDialogName     string
	deleteRPCDialogIdx      int
	deleteRPCDialogYesSelected bool

	// send button state
	sendButtonFocused  bool
	sendForm           *huh.Form
	sendFormFields     []huh.Field // for click-to-focus stepping, see focusHuhField

	// send tx popup state
	sendFormError         string
	sendFormErrTime       time.Time
	sendFormButtonFocused bool // true once Tab has moved focus past the last field onto Submit

	// transaction result panel state
	txResultPackaging  bool
	txResultHex        string
	txResultEIP681     string
	txResultFormat     string
	txResultError      string
	txQRViewport       viewport.Model
	txQRFrames         []string // pre-rendered QR ASCII art for the active step
	txQRFrameIdx       int      // index of the currently visible frame
	txApproveQRFrames  []string // step-1 approve QR frames (nil when no approve needed)
	txApproveJSON      string   // approve tx JSON for display
	txSwapQRFrames     []string // step-2 swap QR frames (populated when approve is present)
	txSwapJSON         string   // swap tx JSON for display
	txSwapSummary      string   // human-readable swap summary for step-2 content
	txSwapStep         bool     // false=showing approve (step 1), true=showing swap (step 2)

	// Uniswap swap state
	uniswapFromTokenIdx    int    // index in available tokens
	uniswapToTokenIdx      int    // index in available tokens
	uniswapFromAmount      string // user input amount
	uniswapToAmount        string // estimated output amount
	uniswapFocusedField    int    // 0=from, 1=to, 2=swap button
	uniswapShowingSelector bool   // true when showing token selector popup
	uniswapSelectorFor     int    // 0=from, 1=to
	uniswapSelectorIdx     int    // selected index in token selector
	uniswapEstimating      bool   // true when estimating swap output
	uniswapQuote           *helpers.SwapQuote // current swap quote
	uniswapQuoteError      string             // error from quote fetch
	uniswapPriceImpactWarn string             // warning message for high price impact
	uniswapHookWarn        string             // warning shown when the resolved V4 pool has a non-zero hook address
	uniswapEditingFrom     bool               // true if user has been editing From field
	uniswapEditingTo       bool               // true if user has been editing To field
	// Track last quote parameters to avoid unnecessary fetches
	lastQuoteFromAmount   string              // last amount used for forward quote
	lastQuoteToAmount     string              // last amount used for reverse quote
	lastQuoteFromTokenIdx int                 // last from token index used for quote
	lastQuoteToTokenIdx   int                 // last to token index used for quote
	uniswapLastVersion    helpers.PoolVersion // which Uniswap version the last resolved pair used
	uniswapLastFee        uint32              // valid when uniswapLastVersion == PoolVersionV3
	uniswapLastV4Key      helpers.V4PoolKey   // valid when uniswapLastVersion == PoolVersionV4
	uniswapLastV4PoolID   common.Hash         // valid when uniswapLastVersion == PoolVersionV4

	// On-chain pair/pool resolution (replaces the old hardcoded pair table)
	pairCache            map[string]pairCacheEntry
	uniswapResolvingPair bool // true while an on-chain factory lookup is in flight

	// Liquidity positions view (within Uniswap page)
	uniswapShowingLiquidity bool
	liquidityPositions      []helpers.LiquidityPosition
	liquidityLoading        bool
	liquidityFocusedIdx     int
	liquidityErr            string

	// Terra Nullius dapp state
	terraNullFocusedField   int                    // 1=Claims, 2=Claim
	terraNullClaimsCount    string                 // display value from number_of_claims()
	terraNullClaimsLoading  bool
	terraNullClaimInput     string                 // typed index for claims() query
	terraNullClaimResult    *helpers.TerraClaimResult
	terraNullClaimQuerying  bool
	terraNullLastQueriedIdx string // index used for the current/last result
	terraNullClaimResultErr string
	// Terra Nullius claim popup state
	terraNullMsgInput      textinput.Model
	terraNullFormFocused   int    // 0=message input, 1=submit button
	terraNullMsgError      string

	// McClellan Oscillator page state
	oscillatorBackfillActive bool
	oscillatorLines          <-chan string
	oscillatorCancel         context.CancelFunc
	oscillatorDays           []string
	oscillatorSeries         []float64
	oscillatorEthCloses      []float64
	oscillatorEthChange      []float64
	oscillatorSeriesErr      string

	// Pool Event Monitor state
	poolEventMonitorActive bool
	poolEventMonitor       *helpers.PoolEventMonitor

	// V4 Block Scanner state (one-shot historical scan)
	v4BlockScanActive  bool
	v4BlockScanner     *helpers.V4BlockScanner

	// Address indexer state (toggleable via "i")
	txIndexerActive bool
	txIndexer       *indexer.Indexer

	// Persistent event store (SQLite)
	eventStore    *store.Store
	eventStoreErr string // set if store failed to open

	// V4 Events panel (shown when pool event monitor is active)
	v4PoolRows       []store.PoolRow
	v4EventsViewport viewport.Model
	focusedPanel     focusedPanelKind // which panel (V4 events or log) has scroll focus
	v4Scroll         scrollbar.State  // scrollbar state for the V4 events panel

	// Pool Info popup state
	poolInfoLoading    bool
	poolInfoID         string
	poolInfoData       *helpers.PoolInfo
	poolInfoErr        string
	poolInfoKeyLoading bool
	poolInfoKeyErr     string
	poolInfoCopied   bool // true briefly after a successful copy

	logScroll   scrollbar.State // scrollbar state for the log panel
	txQRScroll  scrollbar.State // scrollbar state for the txQR result dialog

	// Double-click detection for header address
	lastClickTime time.Time
	lastClickX    int
	lastClickY    int

	// Account list popup state
	accountListSelectedIdx int
	headerAddrX            int // X position of active address in header
	headerAddrY            int // Y position of active address in header
	headerAddrWidth        int // Width of active address display in header

	// Webcam scan state (used by dialogScanTx)
	webcamActive    bool
	webcamCam       *capture.Camera
	webcamFrameCh   <-chan image.Image
	webcamRendered  string
	webcamScanLog   []string
	webcamErrStr    string
	webcamLogVP     viewport.Model
	webcamLogScroll scrollbar.State
	urReassembler   *rpc.URReassembler // in-progress multi-part UR scan, if any

	// Paste-signed-transaction dialog state (used by dialogPasteSignedTx)
	pasteTxForm          *huh.Form
	pasteTxFormField      huh.Field // the form's single field, for direct Focus()/Blur() — see pasteTxButtonFocused
	pasteTxButtonFocused bool       // true once Tab has moved focus past the field onto Submit
	pasteTxPhase     pasteTxPhaseKind
	pasteTxHash      string
	pasteTxSendErr   string
	pasteTxCountdown int
	pasteTxPollErr   string
	pasteTxOnChainInfo *rpc.TxOnChainInfo
	pasteTxChainID   *big.Int // captured at submit time, picks the Etherscan subdomain

	// Tx hash hit-test (clickable in the polling phase — opens Etherscan)
	pasteTxHashLineY  int
	pasteTxHashLineX1 int
	pasteTxHashLineX2 int
}

// -------------------- INIT --------------------

// newModel creates and initializes a new model with configuration from disk
func newModel() model {
	// config path
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".charm-wallet-config.json")

	// event store
	dbPath := filepath.Join(homeDir, ".charm-wallet-events.db")
	eventStore, storeErr := store.Open(dbPath)
	var eventStoreErrMsg string
	if storeErr != nil {
		eventStoreErrMsg = storeErr.Error()
	}

	// load config
	cfg := config.Load(configPath)

	// Load wallet entries from config
	accounts := cfg.Wallets
	if accounts == nil {
		accounts = []config.WalletEntry{}
	}

	// Find active wallet or default to first
	selectedIdx := 0
	activeAddr := ""
	for i, w := range accounts {
		if w.Active {
			selectedIdx = i
			activeAddr = w.Address
			break
		}
	}

	// input for address
	in := textinput.New()
	in.Placeholder = "Paste Public Address 0x…"
	in.Prompt = "Address: "
	in.PromptStyle = lipgloss.NewStyle().Foreground(styles.CAccent)
	in.TextStyle = lipgloss.NewStyle().Foreground(styles.CText)
	in.Cursor.Style = lipgloss.NewStyle().Foreground(styles.CAccent2)
	in.CharLimit = 42
	in.Width = 48

	// input for nickname
	nicknameIn := textinput.New()
	nicknameIn.Placeholder = "Optional nickname"
	nicknameIn.Prompt = "Nickname: "
	nicknameIn.PromptStyle = lipgloss.NewStyle().Foreground(styles.CAccent)
	nicknameIn.TextStyle = lipgloss.NewStyle().Foreground(styles.CText)
	nicknameIn.Cursor.Style = lipgloss.NewStyle().Foreground(styles.CAccent2)
	nicknameIn.CharLimit = 50
	nicknameIn.Width = 48

	// spinner
	sp := anim.New(anim.Settings{
		Size:       8,
		GradColorA: color.RGBA{R: 0x87, G: 0x4B, B: 0xFD, A: 0xFF}, // CBorder #874BFD
		GradColorB: color.RGBA{R: 0x7E, G: 0xE7, B: 0x87, A: 0xFF}, // CAccent #7EE787
	})

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

	// Token watchlist: loaded from persisted config. On first run (no entries
	// saved yet) it's seeded from the starter set (WETH/USDC/USDT/DAI, tagged
	// per-chain for both mainnet and Sepolia — see buildTokenWatchlist) and
	// saved immediately. From then on it's entirely user-editable via the
	// Watched Tokens page and is not rebuilt on RPC reconnect (see
	// handleRPCConnected); m.tokenWatchForActiveChain() filters it down to
	// whichever network is currently connected.
	var watch []rpc.WatchedToken
	if len(cfg.WatchedTokens) == 0 {
		watch = buildTokenWatchlist()
		cfg.WatchedTokens = tokenWatchToConfigList(watch)
		config.Save(configPath, cfg)
	} else {
		watch = configListToTokenWatch(cfg.WatchedTokens)
	}

	// Terra Nullius message input (for claim popup)
	terraNullInput := textinput.New()
	terraNullInput.Placeholder = "Enter your message…"
	terraNullInput.Prompt = ""
	terraNullInput.TextStyle = lipgloss.NewStyle().Foreground(styles.CText)
	terraNullInput.Cursor.Style = lipgloss.NewStyle().Foreground(styles.CAccent2)
	terraNullInput.CharLimit = 256
	terraNullInput.Width = 44

	// Initialize log viewport
	vp := viewport.New(0, 20) // Will be resized in Update on first WindowSizeMsg
	vp.Style = lipgloss.NewStyle().
		Foreground(styles.CText).
		Background(styles.CPanel)

	// Initialize V4 events viewport
	v4vp := viewport.New(0, 20) // Will be resized on first WindowSizeMsg
	v4vp.Style = lipgloss.NewStyle().
		Foreground(styles.CText).
		Background(styles.CPanel)

	// Initialize Watched Tokens list viewport
	tokenListVP := viewport.New(0, 20) // Will be resized on first WindowSizeMsg
	tokenListVP.Style = lipgloss.NewStyle().
		Foreground(styles.CText).
		Background(styles.CPanel)

	// Initialize QR transaction result viewport
	txqrvp := viewport.New(0, 20) // Will be resized on first WindowSizeMsg
	txqrvp.Style = lipgloss.NewStyle().
		Foreground(styles.CText).
		Background(styles.CPanel)

	// Initialize log spinner
	logSpin := anim.New(anim.Settings{
		Size:       4,
		GradColorA: color.RGBA{R: 0x79, G: 0xC0, B: 0xFF, A: 0xFF}, // CAccent2 #79C0FF
		GradColorB: color.RGBA{R: 0x7E, G: 0xE7, B: 0x87, A: 0xFF}, // CAccent #7EE787
	})

	m := model{
		mouseAllMotionActive: true, // matches the cmdEnableMouseAllMotion() issued by Init()
		activePage:           config.PageWallets,
		accounts:           accounts,
		selectedWallet:     selectedIdx,
		highlightedAddress: activeAddr,
		activeAddress:      activeAddr,

		input:              in,
		nicknameInput:      nicknameIn,
		focusedInput:       0,
		spin:               sp,
		rpcURL:             activeRPC,
		tokenWatch:         watch,
		tokenFormMode:      "list",
		settingsMode:       "list",
		rpcURLs:            cfg.RPCURLs,
		selectedRPCIdx:     0,
		configPath:         configPath,
		logEnabled:         cfg.Logger,
		logViewport:        vp,
		v4EventsViewport:   v4vp,
		tokenListViewport:  tokenListVP,
		txQRViewport:       txqrvp,
		logBuffer:          &strings.Builder{},
		logSpinner:         logSpin,
		detailsCache:       make(map[string]rpc.WalletDetails),
		pairCache:          make(map[string]pairCacheEntry),
		dapps:           config.DefaultDapps(),
		selectedDappIdx: 0,
		detailsInWallets:   true, // Enable split panel view by default
		terraNullFocusedField: 1,
		terraNullClaimInput:   "0",
		terraNullMsgInput:     terraNullInput,
		eventStore:            eventStore,
		eventStoreErr:         eventStoreErrMsg,
	}

	return m
}

// Init implements tea.Model interface and returns initial commands
func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spin.Start(), cmdEnableMouseAllMotion()}
	if m.logEnabled {
		cmds = append(cmds, initLogViewport(), m.logSpinner.Start())
	}
	// connect if rpc is set
	if m.rpcURL != "" {
		m.rpcConnecting = true
		cmds = append(cmds, connectRPC(m.rpcURL))
	}
	return tea.Batch(cmds...)
}
