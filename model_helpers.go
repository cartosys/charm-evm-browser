package main

import (
	"context"
	"math/big"
	"sort"
	"strings"
	"time"

	"charm-wallet-tui/config"
	"charm-wallet-tui/helpers"
	"charm-wallet-tui/rpc"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ethereum/go-ethereum/common"
)

// buildTokenWatchlist returns the starter token watchlist (WETH/USDC/USDT/DAI)
// for both mainnet and Sepolia, each entry tagged with its ChainID, so a
// fresh install has working defaults on whichever network is active.
func buildTokenWatchlist() []rpc.WatchedToken {
	var tokens []rpc.WatchedToken

	mainnet := helpers.UniswapAddressesForChain(nil)
	tokens = append(tokens,
		rpc.WatchedToken{Symbol: "WETH", Decimals: 18, Address: mainnet.WETH, ChainID: big.NewInt(1)},
		rpc.WatchedToken{Symbol: "USDC", Decimals: 6, Address: mainnet.USDC, ChainID: big.NewInt(1)},
		rpc.WatchedToken{Symbol: "USDT", Decimals: 6, Address: mainnet.USDT, ChainID: big.NewInt(1)},
		rpc.WatchedToken{Symbol: "DAI", Decimals: 18, Address: mainnet.DAI, ChainID: big.NewInt(1)},
	)
	if mainnet.SPCXon != (common.Address{}) {
		tokens = append(tokens, rpc.WatchedToken{Symbol: "SPCXon", Decimals: 18, Address: mainnet.SPCXon, ChainID: big.NewInt(1)})
		// helpers.OndoLiquidTokens (vendored by cmd/discoverondoliquidity) is
		// mainnet-only, matching SPCXon above — gate on the same "is this
		// mainnet" signal rather than adding a second network check.
		for _, t := range helpers.OndoLiquidTokens {
			if t.Address == mainnet.SPCXon {
				continue // already added above
			}
			tokens = append(tokens, rpc.WatchedToken{Symbol: t.Symbol, Name: t.Name, Decimals: t.Decimals, Address: t.Address, ChainID: big.NewInt(1)})
		}
	}

	sepolia := helpers.UniswapAddressesForChain(big.NewInt(helpers.SepoliaChainID))
	sepoliaChainID := big.NewInt(helpers.SepoliaChainID)
	tokens = append(tokens,
		rpc.WatchedToken{Symbol: "WETH", Decimals: 18, Address: sepolia.WETH, ChainID: sepoliaChainID},
		rpc.WatchedToken{Symbol: "USDC", Decimals: 6, Address: sepolia.USDC, ChainID: sepoliaChainID},
		rpc.WatchedToken{Symbol: "USDT", Decimals: 6, Address: sepolia.USDT, ChainID: sepoliaChainID},
		rpc.WatchedToken{Symbol: "DAI", Decimals: 18, Address: sepolia.DAI, ChainID: sepoliaChainID},
	)

	return tokens
}

// tokenWatchToConfigList converts the in-memory watchlist to its persisted form.
func tokenWatchToConfigList(watch []rpc.WatchedToken) []config.WatchedToken {
	out := make([]config.WatchedToken, len(watch))
	for i, t := range watch {
		out[i] = config.WatchedToken{Symbol: t.Symbol, Name: t.Name, Decimals: t.Decimals, Address: t.Address.Hex(), TotalSupply: t.TotalSupply, ChainID: t.ChainID}
	}
	return out
}

// configListToTokenWatch converts the persisted watchlist to its in-memory form.
func configListToTokenWatch(entries []config.WatchedToken) []rpc.WatchedToken {
	out := make([]rpc.WatchedToken, len(entries))
	for i, e := range entries {
		out[i] = rpc.WatchedToken{Symbol: e.Symbol, Name: e.Name, Decimals: e.Decimals, Address: common.HexToAddress(e.Address), TotalSupply: e.TotalSupply, ChainID: e.ChainID}
	}
	return out
}

// chainIDOrMainnet treats a nil chain ID (undetected connection, or a
// pre-chain-aware saved entry) as mainnet, matching
// helpers.UniswapAddressesForChain's existing nil-defaults-to-mainnet rule.
func chainIDOrMainnet(id *big.Int) *big.Int {
	if id == nil {
		return big.NewInt(1)
	}
	return id
}

// tokensForChain returns the subset of watch whose ChainID matches chainID
// (nil-safe on both sides — see chainIDOrMainnet).
func tokensForChain(watch []rpc.WatchedToken, chainID *big.Int) []rpc.WatchedToken {
	want := chainIDOrMainnet(chainID)
	var out []rpc.WatchedToken
	for _, t := range watch {
		if chainIDOrMainnet(t.ChainID).Cmp(want) == 0 {
			out = append(out, t)
		}
	}
	return out
}

// sortedWatchedTokens returns watch ordered by the active wallet's raw on-chain
// balance for each token, highest first. Tokens with no loaded balance sort last;
// ties keep their original watchlist order.
func sortedWatchedTokens(watch []rpc.WatchedToken, details rpc.WalletDetails) []rpc.WatchedToken {
	balanceFor := func(addr common.Address) *big.Int {
		for _, tb := range details.Tokens {
			if tb.Address == addr {
				return tb.Balance
			}
		}
		return nil
	}

	sorted := make([]rpc.WatchedToken, len(watch))
	copy(sorted, watch)
	sort.SliceStable(sorted, func(i, j int) bool {
		bi := balanceFor(sorted[i].Address)
		bj := balanceFor(sorted[j].Address)
		if bi == nil {
			bi = big.NewInt(0)
		}
		if bj == nil {
			bj = big.NewInt(0)
		}
		return bi.Cmp(bj) > 0
	})
	return sorted
}

// isDoubleClick returns true when (x, y) matches the previous click within 500 ms.
// Always updates last-click state so a single call both detects and advances the window.
func (m *model) isDoubleClick(x, y int) bool {
	const doubleClickWindow = 500 * time.Millisecond
	now := time.Now()
	isDouble := now.Sub(m.lastClickTime) < doubleClickWindow &&
		m.lastClickX == x && m.lastClickY == y
	m.lastClickTime = now
	m.lastClickX = x
	m.lastClickY = y
	return isDouble
}

// textInputActive returns true if any text input is currently capturing keystrokes.
func (m model) textInputActive() bool {
	return m.activeDialog == dialogAddWallet ||
		m.activeDialog == dialogEditWallet ||
		(m.activeDialog == dialogSendTx && m.sendForm != nil) ||
		((m.settingsMode == "add" || m.settingsMode == "edit") && m.form != nil) ||
		((m.tokenFormMode == "add" || m.tokenFormMode == "edit") && m.tokenForm != nil) ||
		(m.activeDialog == dialogPasteSignedTx && m.pasteTxPhase == pasteTxPhaseForm && m.pasteTxForm != nil) ||
		m.activeDialog == dialogTerraClaim
}

// loadSelectedWalletDetails loads details for the currently selected wallet,
// serving from the cache when available.
func (m *model) loadSelectedWalletDetails() tea.Cmd {
	if !m.detailsInWallets || len(m.accounts) == 0 {
		return nil
	}
	addr := m.accounts[m.selectedWallet].Address
	if cached, ok := m.detailsCache[strings.ToLower(addr)]; ok {
		m.details = cached
		m.loading = false
		return nil
	}
	m.loading = true
	m.details = rpc.WalletDetails{Address: addr}
	return loadDetails(m.ethClient, common.HexToAddress(addr), m.tokenWatchForActiveChain())
}

// loadSelectedWalletDetailsFresh drops the cached details for the selected
// wallet before reloading, so a just-added/edited watched token's balance is
// picked up immediately instead of being masked by a stale cache hit.
func (m *model) loadSelectedWalletDetailsFresh() tea.Cmd {
	if len(m.accounts) == 0 {
		return nil
	}
	addr := m.accounts[m.selectedWallet].Address
	delete(m.detailsCache, strings.ToLower(addr))
	return m.loadSelectedWalletDetails()
}

// navigateTo sets the active page and issues any initial Cmds required for that page.
func (m *model) navigateTo(page config.Page) tea.Cmd {
	m.activePage = page
	switch page {
	case config.PageWallets:
		return m.loadSelectedWalletDetails()
	case config.PageSettings:
		m.settingsMode = "list"
	case config.PageWatchedTokens:
		m.tokenFormMode = "list"
		m.selectedTokenIdx = 0
	case config.PageUniswap:
		m.uniswapFromTokenIdx = 0
		m.uniswapToTokenIdx = 1
		m.uniswapFromAmount = ""
		m.uniswapToAmount = ""
		m.uniswapFocusedField = 0
		m.uniswapShowingSelector = false
		m.uniswapSelectorFor = 0
		m.uniswapSelectorIdx = 0
		m.uniswapEstimating = false
		m.uniswapQuote = nil
		m.uniswapQuoteError = ""
		m.uniswapPriceImpactWarn = ""
		m.lastQuoteFromAmount = ""
		m.lastQuoteFromTokenIdx = -1
		m.lastQuoteToTokenIdx = -1
	case config.PageOscillator:
		m.oscillatorDays = nil
		m.oscillatorSeries = nil
		m.oscillatorSeriesErr = ""
		return m.startOscillatorBackscan()

	case config.PageTerraNullius:
		m.terraNullFocusedField = 1
		m.terraNullClaimsCount = ""
		m.terraNullClaimsLoading = true
		m.terraNullClaimInput = "0"
		m.terraNullClaimResult = nil
		m.terraNullClaimQuerying = false
		m.terraNullClaimResultErr = ""
		m.activeDialog = dialogNone
		m.logInfo("Terra Nullius: loading number of claims…")
		return fetchTerraNumberOfClaims(m.ethClient)
	}
	return nil
}

// oscillatorBasket returns the mainnet-only subset of the watchlist the
// McClellan Oscillator basket is built from — pinned to mainnet regardless
// of the currently connected network (tokenWatchForActiveChain would
// silently empty the basket while connected to Sepolia).
func (m *model) oscillatorBasket() []rpc.WatchedToken {
	return tokensForChain(m.tokenWatch, big.NewInt(1))
}

// startOscillatorBackscan (re)starts the McClellan Oscillator's basket
// backscan and returns the Cmd that begins pumping its progress channel.
// Shared by navigateTo's PageOscillator case and the page's manual
// re-resolve action ("r").
func (m *model) startOscillatorBackscan() tea.Cmd {
	if m.eventStore == nil {
		m.oscillatorSeriesErr = "event store unavailable"
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.oscillatorCancel = cancel
	m.oscillatorBackfillActive = true
	ch := m.eventStore.IndexOscillatorBackfill(ctx, m.rpcURL, helpers.UniswapAddressesForChain(big.NewInt(1)), m.oscillatorBasket())
	m.oscillatorLines = ch
	m.logInfo("McClellan Oscillator: starting basket backscan…")
	return waitForOscillatorLine(ch)
}

// loadDetails fetches ETH and token balances for an address.
func loadDetails(client *rpc.Client, addr common.Address, watch []rpc.WatchedToken) tea.Cmd {
	return func() tea.Msg {
		return detailsLoadedMsg{d: rpc.LoadWalletDetails(client, addr, watch)}
	}
}
