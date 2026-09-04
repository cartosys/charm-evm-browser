package indexer

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func word32(v *big.Int) []byte {
	return common.LeftPadBytes(v.Bytes(), 32)
}

// signedWord32 encodes v (possibly negative) as a 32-byte two's-complement word.
func signedWord32(v int64) []byte {
	if v >= 0 {
		return word32(big.NewInt(v))
	}
	twos := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(v))
	b := twos.Bytes()
	out := make([]byte, 32)
	for i := range out {
		out[i] = 0xff
	}
	copy(out[32-len(b):], b)
	return out
}

func TestDecodeV2Sync(t *testing.T) {
	pair := common.HexToAddress("0x1111111111111111111111111111111111111a")
	var data []byte
	data = append(data, word32(big.NewInt(1000))...)
	data = append(data, word32(big.NewInt(2000))...)

	l := types.Log{
		Address:     pair,
		Topics:      []common.Hash{v2SyncSig},
		Data:        data,
		BlockNumber: 100,
		Index:       3,
	}

	ev := decodeV2Sync(l)
	if ev == nil {
		t.Fatal("decodeV2Sync returned nil")
	}
	if ev.Reserve0.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Reserve0 = %v, want 1000", ev.Reserve0)
	}
	if ev.Reserve1.Cmp(big.NewInt(2000)) != 0 {
		t.Errorf("Reserve1 = %v, want 2000", ev.Reserve1)
	}
	if ev.PairAddr != pair {
		t.Errorf("PairAddr = %v, want %v", ev.PairAddr, pair)
	}
	if ev.Block != 100 || ev.LogIndex != 3 {
		t.Errorf("Block/LogIndex = %d/%d, want 100/3", ev.Block, ev.LogIndex)
	}
}

func TestDecodeV2SyncWrongTopic(t *testing.T) {
	l := types.Log{Topics: []common.Hash{v3SwapSig}, Data: make([]byte, 64)}
	if ev := decodeV2Sync(l); ev != nil {
		t.Errorf("expected nil for wrong topic, got %+v", ev)
	}
}

func TestDecodeV3Swap(t *testing.T) {
	pool := common.HexToAddress("0x2222222222222222222222222222222222222b")
	sender := common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000aaa")
	recipient := common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000bbb")

	var data []byte
	data = append(data, signedWord32(-500)...)          // amount0
	data = append(data, signedWord32(500)...)           // amount1
	sqrtPriceX96 := new(big.Int).Lsh(big.NewInt(1), 96) // raw price 1
	data = append(data, word32(sqrtPriceX96)...)
	data = append(data, word32(big.NewInt(123456))...) // liquidity
	data = append(data, signedWord32(-100)...)         // tick

	l := types.Log{
		Address:     pool,
		Topics:      []common.Hash{v3SwapSig, sender, recipient},
		Data:        data,
		BlockNumber: 200,
		Index:       7,
	}

	ev := decodeV3Swap(l)
	if ev == nil {
		t.Fatal("decodeV3Swap returned nil")
	}
	if ev.SqrtPriceX96.Cmp(sqrtPriceX96) != 0 {
		t.Errorf("SqrtPriceX96 = %v, want %v", ev.SqrtPriceX96, sqrtPriceX96)
	}
	if ev.Tick != -100 {
		t.Errorf("Tick = %d, want -100", ev.Tick)
	}
	if ev.PoolAddr != pool {
		t.Errorf("PoolAddr = %v, want %v", ev.PoolAddr, pool)
	}
	if ev.Block != 200 || ev.LogIndex != 7 {
		t.Errorf("Block/LogIndex = %d/%d, want 200/7", ev.Block, ev.LogIndex)
	}
}
