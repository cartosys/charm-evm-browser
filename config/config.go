package config

import (
	"encoding/json"
	"math/big"
	"os"
)

// Config represents the application configuration
type Config struct {
	RPCURLs       []RPCUrl      `json:"rpc_urls"`
	Wallets       []WalletEntry `json:"wallets"`
	Logger        bool          `json:"logger"`
	WatchedTokens []WatchedToken `json:"watched_tokens,omitempty"`
}

// WatchedToken represents a persisted ERC-20 token entry in the watchlist.
type WatchedToken struct {
	Symbol      string   `json:"symbol"`
	Name        string   `json:"name,omitempty"`
	Decimals    uint8    `json:"decimals"`
	Address     string   `json:"address"`
	TotalSupply *big.Int `json:"total_supply,omitempty"`
	// ChainID is the network this entry's Address belongs to. Nil means
	// mainnet — entries saved before chain-awareness was added never had
	// this field, and every one of them is a mainnet address.
	ChainID *big.Int `json:"chain_id,omitempty"`
}

// RPCUrl represents an RPC endpoint
type RPCUrl struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// WalletEntry represents a wallet in the config
type WalletEntry struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
	Active  bool   `json:"active"`
}

// DApp represents a dApp in the config
type DApp struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	Icon        string `json:"icon,omitempty"`
	Network     string `json:"network,omitempty"`
	Description string `json:"description,omitempty"`
}

// -------------------- UI TYPE DEFINITIONS --------------------

// Page represents a page/view in the application
type Page int

const (
	PageHome Page = iota
	PageWallets
	PageDetails
	PageSettings
	PageDappBrowser
	PageUniswap
	PageTerraNullius
	PageWatchedTokens
	PageOscillator
)

// ClickableArea represents a clickable region on screen for addresses
type ClickableArea struct {
	X, Y          int    // top-left position
	Width, Height int    // dimensions
	Address       string // wallet address to navigate to
}


// -------------------- CONFIG MANAGEMENT --------------------

// Load reads the config from the specified path
func Load(path string) Config {
	data, err := os.ReadFile(path)
	if err != nil {
		// If file doesn't exist, create default config
		if os.IsNotExist(err) {
			cfg := DefaultConfig()
			Save(path, cfg)
			return cfg
		}
		return Config{}
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}
	}

	return cfg
}

// DefaultConfig returns a new configuration with sensible defaults
func DefaultConfig() Config {
	return Config{
		RPCURLs: []RPCUrl{
			{
				Name:   "Sepolia Testnet",
				URL:    "https://ethereum-sepolia-rpc.publicnode.com",
				Active: true,
			},
			{
				Name:   "Mainnet",
				URL:    "https://ethereum-rpc.publicnode.com/",
				Active: false,
			},
		},
		Wallets: []WalletEntry{
			{
				Name:    "Demo Wallet",
				Address: "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
				Active:  true,
			},
			{
				Name:    "vitalik.eth",
				Address: "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
				Active:  false,
			},
		},
		Logger: true,
	}
}

// DefaultDapps returns the built-in dapp list. Dapps are managed here, not in the config file.
func DefaultDapps() []DApp {
	return []DApp{
		{
			Name:    "Uniswap v4",
			Address: "0x000000009B1D0aF20D8C6d0A44e162d11F9b8f00",
			Icon:    "🦄",
			// Network intentionally left blank: Uniswap v4 is supported on
			// both mainnet and Sepolia (see helpers.UniswapAddressesForChain),
			// so its card shows whichever network is actively connected
			// instead of a fixed label (see views/dapps.Render).
			Description: "Uniswap is a leading decentralized cryptocurrency exchange (DEX) on the Ethereum blockchain that utilizes an automated market maker (AMM) system to allow users to swap tokens directly from their wallets without intermediaries. It enables anyone to provide liquidity to pools, earning fees in a non-custodial manner.",
		},
		{
			Name:    "Terra Nullius",
			Address: "0x6e38A457C722C6011B2DfA06d49240e797844d66",
			Icon:    "🌵",
			Network: "Mainnet",
			Description: "The Ethereum Message Board from Block 49,880 (August 7, 2015) — Still Getting Claims\n\n" +
				"Two weeks after Ethereum's genesis block, a Reddit user named \"Semiel\" deployed one of the earliest smart contracts on the network: TerraNullius.\n\n" +
				"What it does: Anyone can \"claim\" a hex coordinate and attach a message to it — a permanent, uncensorable message board on the blockchain. No tokens, no governance, no economic incentive. Just messages, forever.",
		},
		{
			Name:    "McClellan Oscillator",
			Icon:    "📈",
			Network: "Mainnet",
			Description: "A market-breadth indicator (EMA19 − EMA39 of net advancing-vs-declining tokens) computed over your mainnet watched-token basket, using real Uniswap V2/V3/V4 swap history rather than a single reference price.\n\n" +
				"Opening this page runs a one-time historical backscan (90 days of blocks) to seed the basket's daily closing prices — this can take a while on first visit. The oscillator needs at least 39 days of data before its reading is meaningful.",
		},
	}
}

// Save writes the config to the specified path
func Save(path string, cfg Config) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}
