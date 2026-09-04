package indexer

import (
	"context"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Event signatures for the McClellan Oscillator's V2/V3 swap-history
// backfill, declared the same way as this file's existing V4 signatures
// above. V2's Swap event (trade direction/amounts) is intentionally not
// decoded — Sync's post-trade reserve ratio alone is enough to derive price.
var (
	v2SyncSig = crypto.Keccak256Hash([]byte("Sync(uint112,uint112)"))
	v3SwapSig = crypto.Keccak256Hash([]byte("Swap(address,address,int256,int256,uint160,uint128,int24)"))
)

// V2SyncEvent carries a Uniswap V2 pair's reserves at the point of a Sync event.
type V2SyncEvent struct {
	Block    uint64
	TxHash   common.Hash
	LogIndex uint
	PairAddr common.Address
	Reserve0 *big.Int
	Reserve1 *big.Int
}

// V3SwapEvent carries a Uniswap V3 pool's post-swap sqrtPriceX96/tick.
type V3SwapEvent struct {
	Block        uint64
	TxHash       common.Hash
	LogIndex     uint
	PoolAddr     common.Address
	SqrtPriceX96 *big.Int
	Tick         int32
}

// decodeSignedWord interprets a 32-byte ABI word as two's-complement signed —
// used for V3 Swap's int256/int24 fields, which the ABI encoder already
// sign-extends to a full word, so this applies uniformly to both.
func decodeSignedWord(word []byte) *big.Int {
	v := new(big.Int).SetBytes(word)
	if len(word) == 32 && word[0]&0x80 != 0 {
		v.Sub(v, new(big.Int).Lsh(big.NewInt(1), 256))
	}
	return v
}

func decodeV2Sync(l types.Log) *V2SyncEvent {
	if len(l.Topics) < 1 || l.Topics[0] != v2SyncSig || len(l.Data) < 64 {
		return nil
	}
	return &V2SyncEvent{
		Block:    l.BlockNumber,
		TxHash:   l.TxHash,
		LogIndex: uint(l.Index),
		PairAddr: l.Address,
		Reserve0: new(big.Int).SetBytes(l.Data[0:32]),
		Reserve1: new(big.Int).SetBytes(l.Data[32:64]),
	}
}

func decodeV3Swap(l types.Log) *V3SwapEvent {
	if len(l.Topics) < 1 || l.Topics[0] != v3SwapSig || len(l.Data) < 160 {
		return nil
	}
	// data: amount0 int256, amount1 int256, sqrtPriceX96 uint160, liquidity uint128, tick int24
	// — each ABI-encoded as a fixed 32-byte word.
	tick := decodeSignedWord(l.Data[128:160])
	return &V3SwapEvent{
		Block:        l.BlockNumber,
		TxHash:       l.TxHash,
		LogIndex:     uint(l.Index),
		PoolAddr:     l.Address,
		SqrtPriceX96: new(big.Int).SetBytes(l.Data[64:96]),
		Tick:         int32(tick.Int64()),
	}
}

// FetchV2Syncs fetches Sync events for exactly the given V2 pair addresses in
// [from,to], one FilterLogs call regardless of how many pairAddrs there are —
// mirroring fetchV4PoolEvents' multi-address-in-one-call approach.
func FetchV2Syncs(ctx context.Context, client *ethclient.Client, pairAddrs []common.Address, from, to uint64) ([]V2SyncEvent, error) {
	if len(pairAddrs) == 0 {
		return nil, nil
	}
	fCtx, fCancel := context.WithTimeout(ctx, 15*time.Second)
	defer fCancel()
	logs, err := client.FilterLogs(fCtx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(to),
		Addresses: pairAddrs,
		Topics:    [][]common.Hash{{v2SyncSig}},
	})
	if err != nil {
		return nil, err
	}
	var events []V2SyncEvent
	for _, l := range logs {
		if ev := decodeV2Sync(l); ev != nil {
			events = append(events, *ev)
		}
	}
	return events, nil
}

// FetchV3Swaps fetches Swap events for exactly the given V3 pool addresses in [from,to].
func FetchV3Swaps(ctx context.Context, client *ethclient.Client, poolAddrs []common.Address, from, to uint64) ([]V3SwapEvent, error) {
	if len(poolAddrs) == 0 {
		return nil, nil
	}
	fCtx, fCancel := context.WithTimeout(ctx, 15*time.Second)
	defer fCancel()
	logs, err := client.FilterLogs(fCtx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(to),
		Addresses: poolAddrs,
		Topics:    [][]common.Hash{{v3SwapSig}},
	})
	if err != nil {
		return nil, err
	}
	var events []V3SwapEvent
	for _, l := range logs {
		if ev := decodeV3Swap(l); ev != nil {
			events = append(events, *ev)
		}
	}
	return events, nil
}

// FetchV4SwapsForPoolIDs fetches Swap events on the singleton PoolManager,
// filtered to exactly the given V4 pool IDs via the indexed topic[1] — one
// FilterLogs call covers every basket V4 pool at once.
func FetchV4SwapsForPoolIDs(ctx context.Context, client *ethclient.Client, poolIDs []common.Hash, from, to uint64) ([]V4PoolEvent, error) {
	if len(poolIDs) == 0 {
		return nil, nil
	}
	pmABI, err := abi.JSON(strings.NewReader(v4PoolManagerABI))
	if err != nil {
		return nil, err
	}
	poolManager := common.HexToAddress(v4PoolManagerAddress)
	fCtx, fCancel := context.WithTimeout(ctx, 15*time.Second)
	defer fCancel()
	logs, err := client.FilterLogs(fCtx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(to),
		Addresses: []common.Address{poolManager},
		Topics:    [][]common.Hash{{v4SwapSig}, poolIDs},
	})
	if err != nil {
		return nil, err
	}
	var events []V4PoolEvent
	for _, l := range logs {
		if ev := DecodeV4PoolEvent(l, &pmABI); ev != nil {
			events = append(events, *ev)
		}
	}
	return events, nil
}
