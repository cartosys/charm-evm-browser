package store

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"charm-wallet-tui/helpers"
	"charm-wallet-tui/indexer"
	"charm-wallet-tui/rpc"

	"github.com/ethereum/go-ethereum/common"
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
)

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

		tipHeader, err := client.HeaderByNumber(ctx, nil)
		if err != nil {
			emit(fmt.Sprintf("[Oscillator] ERROR: fetch tip header: %v", err))
			return
		}
		tip := tipHeader.Number.Uint64()

		fromBlock := uint64(0)
		if window := windowBlocks; tip > window {
			fromBlock = tip - window
		}
		emit(fmt.Sprintf("[Oscillator] resolving %d basket tokens, scanning blocks %d–%d", len(tokens), fromBlock, tip))

		var refs []OscillatorPoolRef
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
				refs = append(refs, *cached)
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
				TokenAddr:       t.Address,
				Version:         pool.Version,
				PoolKey:         poolKeyString(pool),
				RefToken:        refToken,
				TokenDecimals:   t.Decimals,
				RefDecimals:     refDecimals,
				TokenIsToken0:   isToken0,
				V3Fee:           pool.V3Fee,
				V4Hooks:         pool.V4Key.Hooks,
				V4Fee:           pool.V4Key.Fee,
				V4TickSpacing:   pool.V4Key.TickSpacing,
				ResolvedAtBlock: tip,
			}
			if err := s.SaveOscillatorPoolRef(ref); err != nil {
				emit(fmt.Sprintf("[Oscillator] WARN: save pool ref for %s: %v", t.Symbol, err))
				continue
			}
			emit(fmt.Sprintf("[Oscillator] resolved %s → %s pool %s", t.Symbol, versionLabel(pool.Version), ref.PoolKey))
			refs = append(refs, ref)
		}

		if len(refs) == 0 {
			emit("[Oscillator] no basket tokens resolved — nothing to scan")
			return
		}

		v2Map := make(map[common.Address]OscillatorPoolRef)
		v3Map := make(map[common.Address]OscillatorPoolRef)
		v4Map := make(map[common.Hash]OscillatorPoolRef)
		for _, ref := range refs {
			switch ref.Version {
			case helpers.PoolVersionV2:
				v2Map[common.HexToAddress(ref.PoolKey)] = ref
			case helpers.PoolVersionV3:
				v3Map[common.HexToAddress(ref.PoolKey)] = ref
			case helpers.PoolVersionV4:
				v4Map[common.HexToHash(ref.PoolKey)] = ref
			}
		}
		v2Addrs := make([]common.Address, 0, len(v2Map))
		for a := range v2Map {
			v2Addrs = append(v2Addrs, a)
		}
		v3Addrs := make([]common.Address, 0, len(v3Map))
		for a := range v3Map {
			v3Addrs = append(v3Addrs, a)
		}
		v4IDs := make([]common.Hash, 0, len(v4Map))
		for id := range v4Map {
			v4IDs = append(v4IDs, id)
		}

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
		lastCalibBlock := fromBlock
		blockTimeEstimate := func(block uint64) int64 {
			return calibAnchorTime - int64(calibAnchorBlock-block)*avgBlockTimeSecs
		}

		var totalSaved int
		for chunkStart := fromBlock; chunkStart <= tip; {
			if ctx.Err() != nil {
				return
			}
			chunkEnd := chunkStart + chunkSize - 1
			if chunkEnd > tip {
				chunkEnd = tip
			}

			if chunkStart == fromBlock || chunkEnd-lastCalibBlock >= calibIntervalBlocks {
				if hdr, err := client.HeaderByNumber(ctx, new(big.Int).SetUint64(chunkEnd)); err == nil {
					calibAnchorBlock = chunkEnd
					calibAnchorTime = int64(hdr.Time)
					lastCalibBlock = chunkEnd
				}
			}

			chunkSaved := 0

			if v2Events, err := indexer.FetchV2Syncs(ctx, client, v2Addrs, chunkStart, chunkEnd); err != nil {
				emit(fmt.Sprintf("[Oscillator] WARN: V2 fetch blocks %d–%d: %v", chunkStart, chunkEnd, err))
			} else {
				for _, ev := range v2Events {
					ref, ok := v2Map[ev.PairAddr]
					if !ok {
						continue
					}
					d0, d1 := token0Decimals(ref)
					token0Price := helpers.V2ReservesToPrice(ev.Reserve0, ev.Reserve1, d0, d1)
					price := priceForRef(ref, token0Price)
					if price <= 0 {
						continue
					}
					if err := s.SaveOscillatorSwap(ref.TokenAddr, ev.Block, blockTimeEstimate(ev.Block), ev.TxHash, ev.LogIndex, price); err == nil {
						chunkSaved++
					}
				}
			}

			if v3Events, err := indexer.FetchV3Swaps(ctx, client, v3Addrs, chunkStart, chunkEnd); err != nil {
				emit(fmt.Sprintf("[Oscillator] WARN: V3 fetch blocks %d–%d: %v", chunkStart, chunkEnd, err))
			} else {
				for _, ev := range v3Events {
					ref, ok := v3Map[ev.PoolAddr]
					if !ok {
						continue
					}
					d0, d1 := token0Decimals(ref)
					token0Price := helpers.SqrtPriceX96ToPrice(ev.SqrtPriceX96, d0, d1)
					price := priceForRef(ref, token0Price)
					if price <= 0 {
						continue
					}
					if err := s.SaveOscillatorSwap(ref.TokenAddr, ev.Block, blockTimeEstimate(ev.Block), ev.TxHash, ev.LogIndex, price); err == nil {
						chunkSaved++
					}
				}
			}

			if v4Events, err := indexer.FetchV4SwapsForPoolIDs(ctx, client, v4IDs, chunkStart, chunkEnd); err != nil {
				emit(fmt.Sprintf("[Oscillator] WARN: V4 fetch blocks %d–%d: %v", chunkStart, chunkEnd, err))
			} else {
				for _, ev := range v4Events {
					ref, ok := v4Map[ev.PoolID]
					if !ok || ev.Tick == nil {
						continue
					}
					d0, d1 := token0Decimals(ref)
					token0Price := helpers.V4TickToPrice(int32(ev.Tick.Int64()), d0, d1)
					price := priceForRef(ref, token0Price)
					if price <= 0 {
						continue
					}
					if err := s.SaveOscillatorSwap(ref.TokenAddr, ev.Block, blockTimeEstimate(ev.Block), ev.TxHash, ev.LogIndex, price); err == nil {
						chunkSaved++
					}
				}
			}

			totalSaved += chunkSaved
			emit(fmt.Sprintf("[Oscillator] blocks %d–%d → %d swaps saved (running total %d)", chunkStart, chunkEnd, chunkSaved, totalSaved))

			chunkStart = chunkEnd + 1
		}

		emit(fmt.Sprintf("[Oscillator] done — %d basket tokens, %d swaps indexed", len(refs), totalSaved))
	}()
	return out
}
