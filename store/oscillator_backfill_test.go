package store

import (
	"context"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm-wallet-tui/helpers"
	"charm-wallet-tui/rpc"

	"github.com/ethereum/go-ethereum/common"
)

func TestIndexOscillatorBackfillLive(t *testing.T) {
	rpcURL := os.Getenv("ETH_RPC_URL")
	if rpcURL == "" {
		t.Skip("ETH_RPC_URL not set, skipping integration test")
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	addrs := helpers.UniswapAddressesForChain(big.NewInt(1))
	tokens := []rpc.WatchedToken{
		{Symbol: "WETH", Decimals: 18, Address: addrs.WETH, ChainID: big.NewInt(1)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A small, fast window (~2,000 blocks, one chunk) instead of the full
	// 90-day default — enough to smoke-test resolution + decode + persist
	// without a multi-hour live scan.
	ch := s.indexOscillatorBackfillWindow(ctx, rpcURL, addrs, tokens, 2_000, 2_000)
	for line := range ch {
		t.Log(line)
	}

	ref, err := s.GetOscillatorPoolRef(addrs.WETH)
	if err != nil {
		t.Fatalf("GetOscillatorPoolRef failed: %v", err)
	}
	if ref == nil {
		t.Fatal("expected a resolved pool ref for WETH, got none")
	}
	t.Logf("resolved WETH -> version=%d pool=%s ref=%s", ref.Version, ref.PoolKey, ref.RefToken.Hex())
	if ref.LastScannedBlock == 0 {
		t.Fatal("expected LastScannedBlock to be checkpointed after the first backscan, got 0")
	}
	firstCheckpoint := ref.LastScannedBlock

	closes, err := s.OscillatorDailyCloses(addrs.WETH)
	if err != nil {
		t.Fatalf("OscillatorDailyCloses failed: %v", err)
	}
	// A 2,000-block (~6.7 hour) window on a liquid WETH pool should produce
	// at least one swap, though this isn't guaranteed on every possible
	// window — log rather than hard-fail if it comes back empty.
	if len(closes) == 0 {
		t.Log("no daily closes found in this window — window may have missed a swap; not necessarily a bug")
	} else {
		t.Logf("got %d daily close(s), most recent: %+v", len(closes), closes[len(closes)-1])
	}

	// Re-running the backscan for the same, already-resolved token should
	// resume from the checkpoint instead of rescanning the full window —
	// confirm the progress log never mentions a fresh resolution/backfill.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel2()
	ch2 := s.indexOscillatorBackfillWindow(ctx2, rpcURL, addrs, tokens, 2_000, 2_000)
	for line := range ch2 {
		t.Log(line)
		if strings.Contains(line, "backfilling") {
			t.Fatalf("second run re-backfilled an already-resolved token: %q", line)
		}
	}

	ref2, err := s.GetOscillatorPoolRef(addrs.WETH)
	if err != nil {
		t.Fatalf("GetOscillatorPoolRef (second run) failed: %v", err)
	}
	if ref2.LastScannedBlock < firstCheckpoint {
		t.Fatalf("checkpoint went backwards: first=%d second=%d", firstCheckpoint, ref2.LastScannedBlock)
	}
	t.Logf("checkpoint advanced %d -> %d", firstCheckpoint, ref2.LastScannedBlock)
}

func TestDueKeys(t *testing.T) {
	addrA := common.HexToAddress("0x1111111111111111111111111111111111111111")
	addrB := common.HexToAddress("0x2222222222222222222222222222222222222222")
	m := map[common.Address]oscRefScan{
		addrA: {fromBlock: 100}, // already due at chunkEnd=150
		addrB: {fromBlock: 200}, // not due yet
	}

	due := dueKeys(m, 150)
	if len(due) != 1 || due[0] != addrA {
		t.Fatalf("expected only addrA due at chunkEnd=150, got %v", due)
	}

	due = dueKeys(m, 200)
	if len(due) != 2 {
		t.Fatalf("expected both refs due once chunkEnd reaches fromBlock=200, got %v", due)
	}
}
