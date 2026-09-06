package store

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"charm-wallet-tui/helpers"
	"charm-wallet-tui/indexer"
	"charm-wallet-tui/rpc"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	oscillatorBackfillWindowDays = 90
	// oscillatorBackfillChunk is smaller than IndexV4Backfill's 10k-block
	// chunk since this scan filters across the whole basket's pool addresses
	// at once (a wider log match per call) rather than one singleton
	// contract — keep this a starting value, tune once run against a real
	// RPC provider (see store/oscillator_backfill_test.go).
	oscillatorBackfillChunk = uint64(2_000)
	avgBlockTimeSecs        = int64(12) // post-Merge fixed slot time
	blocksPerDay            = uint64(7_200)
	calibIntervalBlocks     = blocksPerDay // recalibrate the block-time estimate roughly once per day

	rateLimitMaxRetries = 6
	rateLimitBaseDelay  = 500 * time.Millisecond
	rateLimitMaxDelay   = 20 * time.Second
)

// isRateLimited reports whether err looks like a 429 Too Many Requests
// response from the RPC provider — go-ethereum's HTTP transport surfaces the
// status code directly in the error text, the same way the archive-access
// 403s already seen from public RPC providers do.
func isRateLimited(err error) bool {
	return err != nil && strings.Contains(err.Error(), "429")
}

// retryWithBackoff calls fn, retrying with exponential backoff whenever it
// fails with a 429 from the RPC provider (up to rateLimitMaxRetries times)
// instead of losing that chunk's data to a single failed call. Any other
// error (e.g. an archive-access 403) is returned immediately — this only
// throttles the specific failure mode that's actually recoverable by waiting.
func retryWithBackoff[T any](ctx context.Context, emit func(string), label string, fn func() (T, error)) (T, error) {
	delay := rateLimitBaseDelay
	for attempt := 1; ; attempt++ {
		result, err := fn()
		if err == nil || !isRateLimited(err) || attempt > rateLimitMaxRetries {
			return result, err
		}
		emit(fmt.Sprintf("[Oscillator] rate limited on %s, backing off %s (attempt %d/%d)", label, delay, attempt, rateLimitMaxRetries))
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}
		delay *= 2
		if delay > rateLimitMaxDelay {
			delay = rateLimitMaxDelay
		}
	}
}

// versionLabel formats a helpers.PoolVersion for progress logging.
func versionLabel(v helpers.PoolVersion) string {
	switch v {
	case helpers.PoolVersionV2:
		return "V2"
	case helpers.PoolVersionV3:
		return "V3"
	case helpers.PoolVersionV4:
		return "V4"
	default:
		return "?"
	}
}

// poolKeyString returns the persisted pool_key for a resolved pool — the
// pair/pool contract address for V2/V3, or the pool_id hex for V4.
func poolKeyString(pool helpers.ResolvedPool) string {
	if pool.Version == helpers.PoolVersionV4 {
		return pool.V4PoolID.Hex()
	}
	return pool.PairAddr.Hex()
}

// oscRefScan pairs a resolved pool ref with the block it still needs
// scanning from this run — LastScannedBlock+1 for an already-cached ref
// (incremental catch-up), or the full window start for a token resolved for
// the first time this run (no history yet).
type oscRefScan struct {
	ref       OscillatorPoolRef
	fromBlock uint64
}

// dueKeys returns the map keys (pool addresses or V4 pool IDs) whose ref
// still needs scanning as of chunkEnd — i.e. its fromBlock has been reached.
// Pure and RPC-free so the chunk-inclusion logic is unit-testable on its own.
func dueKeys[K comparable](m map[K]oscRefScan, chunkEnd uint64) []K {
	keys := make([]K, 0, len(m))
	for k, sc := range m {
		if sc.fromBlock <= chunkEnd {
			keys = append(keys, k)
		}
	}
	return keys
}

// tokenPriceFromToken0Price converts "token0 price in token1 units" (the
// convention shared by V2ReservesToPrice/SqrtPriceX96ToPrice/V4TickToPrice)
// into "basket token price in ref-token units", given which side of the pool
// the basket token is on.
func tokenPriceFromToken0Price(token0PriceInToken1 float64, tokenIsToken0 bool) float64 {
	if tokenIsToken0 {
		return token0PriceInToken1
	}
	if token0PriceInToken1 == 0 {
		return 0
	}
	return 1 / token0PriceInToken1
}

func (s *Store) getERC20Decimals(address common.Address) (uint8, error) {
	var decimals int
	err := s.db.QueryRow(`SELECT decimals FROM erc20_tokens WHERE address = ?`, address.Hex()).Scan(&decimals)
	return uint8(decimals), err
}

// IndexOscillatorBackfill resolves any not-yet-cached basket token to a pool
// (V2/V3/V4, priced against a stablecoin fallback chain — see
// helpers.ResolveBasketToken), then scans the last
// oscillatorBackfillWindowDays of blocks in oscillatorBackfillChunk-block
// chunks, saving one oscillator_swaps row per V2 Sync / V3 Swap / V4 Swap
// event found on a basket pool. Progress and error text is streamed on the
// returned channel, closed on completion or ctx cancellation.
//
// The httpURL must use the http:// or https:// scheme (not WebSocket),
// mirroring IndexV4Backfill.
func (s *Store) IndexOscillatorBackfill(ctx context.Context, httpURL string, addrs helpers.UniswapNetworkAddresses, tokens []rpc.WatchedToken) <-chan string {
	return s.indexOscillatorBackfillWindow(ctx, httpURL, addrs, tokens, oscillatorBackfillWindowDays*blocksPerDay, oscillatorBackfillChunk)
}

// indexOscillatorBackfillWindow is IndexOscillatorBackfill's implementation,
// parameterized over the scan window (in blocks) and chunk size so tests can
// run a small, fast scan instead of the full 90-day window.
func (s *Store) indexOscillatorBackfillWindow(ctx context.Context, httpURL string, addrs helpers.UniswapNetworkAddresses, tokens []rpc.WatchedToken, windowBlocks, chunkSize uint64) <-chan string {
	out := make(chan string, 512)
	go func() {
		defer close(out)

		emit := func(msg string) {
			select {
			case out <- msg:
			case <-ctx.Done():
			}
		}

		if strings.HasPrefix(httpURL, "ws://") || strings.HasPrefix(httpURL, "wss://") {
			emit("[Oscillator] ERROR: HTTP URL required, got WebSocket URL")
			return
		}

		client, err := ethclient.DialContext(ctx, httpURL)
		if err != nil {
			emit(fmt.Sprintf("[Oscillator] ERROR: dial %s: %v", httpURL, err))
			return
		}
		defer client.Close()

		tipHeader, err := retryWithBackoff(ctx, emit, "tip header", func() (*types.Header, error) {
			return client.HeaderByNumber(ctx, nil)
		})
		if err != nil {
			emit(fmt.Sprintf("[Oscillator] ERROR: fetch tip header: %v", err))
			return
		}
		tip := tipHeader.Number.Uint64()

		windowStart := uint64(0)
		if tip > windowBlocks {
			windowStart = tip - windowBlocks
		}
		emit(fmt.Sprintf("[Oscillator] checking %d basket tokens against tip block %d", len(tokens), tip))

		var scans []oscRefScan
		for _, t := range tokens {
			if ctx.Err() != nil {
				return
			}
			cached, err := s.GetOscillatorPoolRef(t.Address)
			if err != nil {
				emit(fmt.Sprintf("[Oscillator] WARN: load cached ref for %s: %v", t.Symbol, err))
				continue
			}
			if cached != nil {
				from := cached.LastScannedBlock + 1
				if from > tip {
					emit(fmt.Sprintf("[Oscillator] %s already up to date (scanned through block %d)", t.Symbol, cached.LastScannedBlock))
				} else {
					emit(fmt.Sprintf("[Oscillator] %s resuming from block %d (%d new block(s))", t.Symbol, from, tip-from+1))
				}
				scans = append(scans, oscRefScan{ref: *cached, fromBlock: from})
				continue
			}

			pool, refToken, err := helpers.ResolveBasketToken(ctx, client, addrs, t.Address)
			if err != nil {
				emit(fmt.Sprintf("[Oscillator] SKIP %s: %v", t.Symbol, err))
				continue
			}

			isToken0 := false
			switch pool.Version {
			case helpers.PoolVersionV4:
				isToken0 = pool.V4Key.Currency0 == t.Address
			default:
				token0, _, err := helpers.PoolToken0Token1(ctx, client, pool.PairAddr)
				if err != nil {
					emit(fmt.Sprintf("[Oscillator] SKIP %s: token0/token1 lookup: %v", t.Symbol, err))
					continue
				}
				isToken0 = token0 == t.Address
			}

			_ = s.EnsureERC20TokenWithClient(ctx, client, refToken)
			refDecimals, err := s.getERC20Decimals(refToken)
			if err != nil {
				refDecimals = 18 // WETH/most fallback refs are 18dp; best-effort default
			}

			ref := OscillatorPoolRef{
				TokenAddr:        t.Address,
				Version:          pool.Version,
				PoolKey:          poolKeyString(pool),
				RefToken:         refToken,
				TokenDecimals:    t.Decimals,
				RefDecimals:      refDecimals,
				TokenIsToken0:    isToken0,
				V3Fee:            pool.V3Fee,
				V4Hooks:          pool.V4Key.Hooks,
				V4Fee:            pool.V4Key.Fee,
				V4TickSpacing:    pool.V4Key.TickSpacing,
				ResolvedAtBlock:  tip,
				LastScannedBlock: 0,
			}
			if err := s.SaveOscillatorPoolRef(ref); err != nil {
				emit(fmt.Sprintf("[Oscillator] WARN: save pool ref for %s: %v", t.Symbol, err))
				continue
			}
			emit(fmt.Sprintf("[Oscillator] resolved %s → %s pool %s — backfilling %d-day window", t.Symbol, versionLabel(pool.Version), ref.PoolKey, oscillatorBackfillWindowDays))
			scans = append(scans, oscRefScan{ref: ref, fromBlock: windowStart})
		}

		if len(scans) == 0 {
			emit("[Oscillator] no basket tokens resolved — nothing to scan")
			return
		}

		v2Map := make(map[common.Address]oscRefScan)
		v3Map := make(map[common.Address]oscRefScan)
		v4Map := make(map[common.Hash]oscRefScan)
		overallFrom := tip + 1
		pending := 0
		for _, sc := range scans {
			if sc.fromBlock > tip {
				continue // already caught up — no eth_getLogs calls needed for this token this run
			}
			pending++
			if sc.fromBlock < overallFrom {
				overallFrom = sc.fromBlock
			}
			switch sc.ref.Version {
			case helpers.PoolVersionV2:
				v2Map[common.HexToAddress(sc.ref.PoolKey)] = sc
			case helpers.PoolVersionV3:
				v3Map[common.HexToAddress(sc.ref.PoolKey)] = sc
			case helpers.PoolVersionV4:
				v4Map[common.HexToHash(sc.ref.PoolKey)] = sc
			}
		}

		if pending == 0 {
			emit(fmt.Sprintf("[Oscillator] all %d basket tokens up to date at block %d", len(scans), tip))
			return
		}
		emit(fmt.Sprintf("[Oscillator] scanning blocks %d–%d for %d token(s) needing catch-up (%d already up to date)",
			overallFrom, tip, pending, len(scans)-pending))

		priceForRef := func(ref OscillatorPoolRef, token0PriceInToken1 float64) float64 {
			return tokenPriceFromToken0Price(token0PriceInToken1, ref.TokenIsToken0)
		}
		token0Decimals := func(ref OscillatorPoolRef) (uint8, uint8) {
			if ref.TokenIsToken0 {
				return ref.TokenDecimals, ref.RefDecimals
			}
			return ref.RefDecimals, ref.TokenDecimals
		}

		calibAnchorBlock := tip
		calibAnchorTime := int64(tipHeader.Time)
		lastCalibBlock := overallFrom
		blockTimeEstimate := func(block uint64) int64 {
			return calibAnchorTime - int64(calibAnchorBlock-block)*avgBlockTimeSecs
		}

		// checkpoint advances the persisted last_scanned_block for every ref
		// in keys to chunkEnd — called only after that version's fetch for
		// this chunk succeeded, so a token whose fetch failed keeps its
		// earlier checkpoint and that block range gets retried on the next
		// run instead of being silently skipped forever.
		checkpoint := func(keys []common.Address, byAddr map[common.Address]oscRefScan, chunkEnd uint64) {
			for _, a := range keys {
				sc := byAddr[a]
				if err := s.UpdateOscillatorLastScanned(sc.ref.TokenAddr, chunkEnd); err != nil {
					emit(fmt.Sprintf("[Oscillator] WARN: checkpoint %s at block %d: %v", sc.ref.TokenAddr.Hex(), chunkEnd, err))
				}
			}
		}
		checkpointV4 := func(keys []common.Hash, byID map[common.Hash]oscRefScan, chunkEnd uint64) {
			for _, id := range keys {
				sc := byID[id]
				if err := s.UpdateOscillatorLastScanned(sc.ref.TokenAddr, chunkEnd); err != nil {
					emit(fmt.Sprintf("[Oscillator] WARN: checkpoint %s at block %d: %v", sc.ref.TokenAddr.Hex(), chunkEnd, err))
				}
			}
		}

		var totalSaved int
		for chunkStart := overallFrom; chunkStart <= tip; {
			if ctx.Err() != nil {
				return
			}
			chunkEnd := chunkStart + chunkSize - 1
			if chunkEnd > tip {
				chunkEnd = tip
			}

			if chunkStart == overallFrom || chunkEnd-lastCalibBlock >= calibIntervalBlocks {
				hdr, err := retryWithBackoff(ctx, emit, "calibration header", func() (*types.Header, error) {
					return client.HeaderByNumber(ctx, new(big.Int).SetUint64(chunkEnd))
				})
				if err == nil {
					calibAnchorBlock = chunkEnd
					calibAnchorTime = int64(hdr.Time)
					lastCalibBlock = chunkEnd
				}
			}

			chunkSaved := 0

			v2Addrs := dueKeys(v2Map, chunkEnd)
			v2Events, err := retryWithBackoff(ctx, emit, "V2 fetch", func() ([]indexer.V2SyncEvent, error) {
				return indexer.FetchV2Syncs(ctx, client, v2Addrs, chunkStart, chunkEnd)
			})
			if err != nil {
				emit(fmt.Sprintf("[Oscillator] WARN: V2 fetch blocks %d–%d: %v", chunkStart, chunkEnd, err))
			} else {
				for _, ev := range v2Events {
					sc, ok := v2Map[ev.PairAddr]
					if !ok {
						continue
					}
					d0, d1 := token0Decimals(sc.ref)
					token0Price := helpers.V2ReservesToPrice(ev.Reserve0, ev.Reserve1, d0, d1)
					price := priceForRef(sc.ref, token0Price)
					if price <= 0 {
						continue
					}
					if err := s.SaveOscillatorSwap(sc.ref.TokenAddr, ev.Block, blockTimeEstimate(ev.Block), ev.TxHash, ev.LogIndex, price); err == nil {
						chunkSaved++
					}
				}
				checkpoint(v2Addrs, v2Map, chunkEnd)
			}

			v3Addrs := dueKeys(v3Map, chunkEnd)
			v3Events, err := retryWithBackoff(ctx, emit, "V3 fetch", func() ([]indexer.V3SwapEvent, error) {
				return indexer.FetchV3Swaps(ctx, client, v3Addrs, chunkStart, chunkEnd)
			})
			if err != nil {
				emit(fmt.Sprintf("[Oscillator] WARN: V3 fetch blocks %d–%d: %v", chunkStart, chunkEnd, err))
			} else {
				for _, ev := range v3Events {
					sc, ok := v3Map[ev.PoolAddr]
					if !ok {
						continue
					}
					d0, d1 := token0Decimals(sc.ref)
					token0Price := helpers.SqrtPriceX96ToPrice(ev.SqrtPriceX96, d0, d1)
					price := priceForRef(sc.ref, token0Price)
					if price <= 0 {
						continue
					}
					if err := s.SaveOscillatorSwap(sc.ref.TokenAddr, ev.Block, blockTimeEstimate(ev.Block), ev.TxHash, ev.LogIndex, price); err == nil {
						chunkSaved++
					}
				}
				checkpoint(v3Addrs, v3Map, chunkEnd)
			}

			v4IDs := dueKeys(v4Map, chunkEnd)
			v4Events, err := retryWithBackoff(ctx, emit, "V4 fetch", func() ([]indexer.V4PoolEvent, error) {
				return indexer.FetchV4SwapsForPoolIDs(ctx, client, v4IDs, chunkStart, chunkEnd)
			})
			if err != nil {
				emit(fmt.Sprintf("[Oscillator] WARN: V4 fetch blocks %d–%d: %v", chunkStart, chunkEnd, err))
			} else {
				for _, ev := range v4Events {
					sc, ok := v4Map[ev.PoolID]
					if !ok || ev.Tick == nil {
						continue
					}
					d0, d1 := token0Decimals(sc.ref)
					token0Price := helpers.V4TickToPrice(int32(ev.Tick.Int64()), d0, d1)
					price := priceForRef(sc.ref, token0Price)
					if price <= 0 {
						continue
					}
					if err := s.SaveOscillatorSwap(sc.ref.TokenAddr, ev.Block, blockTimeEstimate(ev.Block), ev.TxHash, ev.LogIndex, price); err == nil {
						chunkSaved++
					}
				}
				checkpointV4(v4IDs, v4Map, chunkEnd)
			}

			totalSaved += chunkSaved
			emit(fmt.Sprintf("[Oscillator] blocks %d–%d → %d swaps saved (running total %d)", chunkStart, chunkEnd, chunkSaved, totalSaved))

			chunkStart = chunkEnd + 1
		}

		emit(fmt.Sprintf("[Oscillator] done — %d basket tokens, %d swaps indexed", len(scans), totalSaved))
	}()
	return out
}
