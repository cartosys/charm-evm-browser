package main

import (
	"image"
	"math/big"

	"charm-wallet-tui/helpers"
	"charm-wallet-tui/indexer"
	"charm-wallet-tui/rpc"
	"charm-wallet-tui/store"
	"charm-wallet-tui/webcam/capture"

	"github.com/ethereum/go-ethereum/common"
)

// -------------------- TEA MESSAGES --------------------
// All custom message types for The Elm Architecture

// clipboardCopiedMsg indicates clipboard copy completed
type clipboardCopiedMsg struct{}

// txJsonCopiedMsg indicates transaction JSON was copied to clipboard
type txJsonCopiedMsg struct{}

// poolIDCopiedMsg indicates the pool ID was copied to clipboard from the Pool Info popup
type poolIDCopiedMsg struct{}

// erc20TokenIndexedMsg signals that one or more ERC-20 token lookups completed
type erc20TokenIndexedMsg struct{}

// ensLookupResultMsg contains result of reverse ENS lookup (address -> name)
type ensLookupResultMsg struct {
	address   string
	ensName   string
	err       error
	debugInfo string
}

// ensForwardResolveMsg contains result of forward ENS resolution (name -> address)
type ensForwardResolveMsg struct {
	ensName   string // The .eth name that was resolved
	address   string // The resolved Ethereum address
	err       error
	debugInfo string
}

// uniswapQuoteMsg contains result of Uniswap price quote fetch
type uniswapQuoteMsg struct {
	quote *helpers.SwapQuote
	err   error
}

// pairLookupResultMsg carries the result of an on-chain Uniswap V2/V3
// factory lookup for a token pair (see resolvePairOnChain in model_uniswap.go).
type pairLookupResultMsg struct {
	cacheKey       string
	fromIdx, toIdx int  // token indices active when the lookup was dispatched
	reverse        bool // which quote direction triggered this lookup
	resolution     pairResolution
	ok             bool
}

// logInitMsg signals that log viewport should be initialized
type logInitMsg struct{}

// rpcConnectedMsg contains result of RPC connection attempt
type rpcConnectedMsg struct {
	client *rpc.Client
	err    error
}

// detailsLoadedMsg contains wallet balance details after loading
type detailsLoadedMsg struct {
	d   rpc.WalletDetails
	err error
}

// packageTransactionMsg contains packaged transaction ready for QR display
type packageTransactionMsg struct {
	txDisplay    string
	txJSON       string
	qrData       string
	format       string
	approveQRData string // non-empty when an approve tx must be signed first
	approveJSON  string
	err          error
}

// terraNullClaimsCountMsg contains the result of a number_of_claims() call
type terraNullClaimsCountMsg struct {
	count *big.Int
	err   error
}

// terraNullClaimQueryMsg contains the result of a claims(uint256) call
type terraNullClaimQueryMsg struct {
	result *helpers.TerraClaimResult
	err    error
}

// poolEventLineMsg carries a single formatted pool event line for the log panel
type poolEventLineMsg struct {
	line string
}

// poolEventMonitorStoppedMsg signals that the pool event monitor has stopped
type poolEventMonitorStoppedMsg struct{}

// poolMonitorEventMsg carries a structured V4PoolEvent from the live monitor for SQLite indexing
type poolMonitorEventMsg struct {
	event indexer.V4PoolEvent
}

// poolInfoResultMsg carries the result of a FetchPoolInfo call
type poolInfoResultMsg struct {
	poolID string
	info   *helpers.PoolInfo
	err    error
}

// poolKeyResultMsg carries the result of a FetchPoolKey call
type poolKeyResultMsg struct {
	poolID string
	key    *helpers.PoolKeyInfo
	err    error
}

// indexedEventMsg carries a single ERC-20 Transfer event from the address indexer
type indexedEventMsg struct {
	event indexer.IndexedEvent
}

// indexerStoppedMsg signals that the address indexer has stopped
type indexerStoppedMsg struct{}

// v4PoolEventMsg carries a single Uniswap V4 PoolManager event from the indexer
type v4PoolEventMsg struct {
	event indexer.V4PoolEvent
}

// v4PoolIndexerStoppedMsg signals that the V4 pool events channel has closed
type v4PoolIndexerStoppedMsg struct{}

// indexerProgressMsg carries a backward-scan progress block number
type indexerProgressMsg struct {
	block uint64
}

// recentEventsMsg carries historical events loaded from the local SQLite store
type recentEventsMsg struct {
	events []indexer.IndexedEvent
	count  int64 // total rows in DB at time of query
	err    error
}

// v4BlockScanLineMsg carries a single formatted line from V4BlockScanner.
type v4BlockScanLineMsg struct {
	line string
}

// v4BlockScanDoneMsg signals that the V4BlockScanner has finished.
type v4BlockScanDoneMsg struct{}

// liquidityPositionsMsg carries the result of a V4 liquidity position lookup
type liquidityPositionsMsg struct {
	positions   []helpers.LiquidityPosition
	nftCount    uint64    // total NFTs reported by balanceOf before filtering
	diagnostics []string  // per-step diagnostic lines for the logger
	err         error
}

// v4PoolTableMsg carries a freshly-queried snapshot of indexed V4 pools for the events panel
type v4PoolTableMsg struct {
	rows []store.PoolRow
}

// webcamReadyMsg signals that the camera opened and streaming began
type webcamReadyMsg struct {
	cam *capture.Camera
	ch  <-chan image.Image
}

// webcamFrameMsg carries a decoded camera frame and any QR text found in it
type webcamFrameMsg struct {
	img    image.Image
	qrText string
}

// webcamErrMsg signals a camera or stream error
type webcamErrMsg struct{ err error }

// txQRAnimTickMsg advances the animated QR display to the next frame.
type txQRAnimTickMsg struct{}

// signedTxBroadcastMsg carries the result of relaying a pasted signed
// transaction to the RPC endpoint via eth_sendRawTransaction.
type signedTxBroadcastMsg struct {
	txHash string
	err    error
}

// signedTxPollResultMsg carries the result of checking whether a broadcast
// transaction has been mined yet. found=false with err=nil means "not yet
// mined" — the expected state while polling.
type signedTxPollResultMsg struct {
	info  *rpc.TxOnChainInfo
	found bool
	err   error
}

// signedTxCountdownTickMsg fires once a second to drive the "next check in
// Ns" countdown while polling for on-chain confirmation.
type signedTxCountdownTickMsg struct{}

// tokenMetadataMsg carries the result of an ERC-20 name()/symbol()/decimals()/
// totalSupply() lookup triggered by the Watched Tokens add/edit form.
type tokenMetadataMsg struct {
	address     common.Address
	symbol      string
	name        string
	decimals    uint8
	totalSupply *big.Int
	err         error
}

// oscillatorBackscanLineMsg carries a single progress/error line from the
// McClellan Oscillator's basket backscan (store.IndexOscillatorBackfill).
type oscillatorBackscanLineMsg struct {
	line string
}

// oscillatorBackscanDoneMsg signals that the backscan channel closed.
type oscillatorBackscanDoneMsg struct{}

// oscillatorSeriesMsg carries a freshly recomputed McClellan Oscillator
// series (one value per day after the earliest day with data).
type oscillatorSeriesMsg struct {
	days   []string
	values []float64
	err    error
}
