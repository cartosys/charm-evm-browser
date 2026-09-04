package helpers

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// v4NftPositionManager is the Uniswap V4 PositionManager (ERC-721) on Ethereum
// mainnet — used by mainnetUniswapAddresses (helpers/uniswap.go) and by the
// mainnet-specific live-RPC test in v4_block_test.go. GetLiquidityPositions
// itself resolves the chain-appropriate address via addressesForClient
// instead of using this directly, so it also works on Sepolia.
var v4NftPositionManager = common.HexToAddress("0xbD216513d74C8cf14cf4747E6AaA6420FF64ee9E")

const v4PositionManagerViewABI = `[
  {
    "inputs": [{"name": "poolId", "type": "bytes32"}, {"name": "positionKey", "type": "bytes32"}],
    "name": "getPositionInfo",
    "outputs": [
      {"name": "liquidity",                   "type": "uint128"},
      {"name": "feeGrowthInside0LastX128",     "type": "uint256"},
      {"name": "feeGrowthInside1LastX128",     "type": "uint256"}
    ],
    "stateMutability": "view",
    "type": "function"
  }
]`

// LiquidityPosition represents a Uniswap V4 NFT liquidity position.
type LiquidityPosition struct {
	TokenID        *big.Int
	Stub           bool           // true when positions() call failed; only TokenID is populated
	Token0         common.Address // currency0
	Token1         common.Address // currency1
	Token0Symbol   string
	Token1Symbol   string
	Token0Decimals uint8
	Token1Decimals uint8
	Fee            uint32  // fee tier in hundredths of a bip (3000 = 0.3%)
	TickSpacing    int32   // V4 tick spacing
	Hooks          common.Address
	TickLower      int32
	TickUpper      int32
	Liquidity      *big.Int
	TokensOwed0    *big.Int // nil for V4 (different fee model)
	TokensOwed1    *big.Int // nil for V4 (different fee model)
	MinPrice       float64  // human-readable price of token0 in token1 at tickLower
	MaxPrice       float64  // human-readable price of token0 in token1 at tickUpper
}

// GetLiquidityPositions fetches all Uniswap V4 NFT positions held by ownerAddr.
// Caps at 50 to avoid excessive RPC calls.
// Returns positions, total NFT balance, diagnostic log lines, and any error.
func GetLiquidityPositions(rpcURL string, ownerAddr common.Address) ([]LiquidityPosition, uint64, []string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("dial: %w", err)
	}
	defer client.Close()

	addrs := addressesForClient(ctx, client)
	posManager := addrs.V4PositionManager
	stateView := addrs.V4StateView

	balance, err := v4BalanceOf(ctx, client, posManager, ownerAddr)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("balanceOf: %w", err)
	}
	if balance == 0 {
		return nil, 0, nil, nil
	}

	stateViewABI, err := abi.JSON(strings.NewReader(v4PositionManagerViewABI))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("parse StateView ABI: %w", err)
	}

	diags := []string{fmt.Sprintf("balanceOf: %d NFT(s)", balance)}
	tokenIds, enumDiags, enumErr := v4EnumerateOwnedTokenIds(ctx, client, posManager, ownerAddr, balance)
	diags = append(diags, enumDiags...)
	if enumErr != nil {
		return nil, balance, diags, fmt.Errorf("enumerate token IDs: %w", enumErr)
	}

	syms := newV4SymbolCache()
	positions := make([]LiquidityPosition, 0, len(tokenIds))

	for _, tokenId := range tokenIds {
		pos, err := v4FetchPosition(ctx, client, posManager, tokenId)
		if err != nil {
			diags = append(diags, fmt.Sprintf("tokenId %s: positions() error: %v", tokenId, err))
			// Still include a stub so the token ID is visible in the panel.
			positions = append(positions, LiquidityPosition{TokenID: tokenId, Stub: true})
			continue
		}

		// Fetch per-position liquidity from StateView.
		poolId := v4ComputePoolId(pos.Token0, pos.Token1, pos.Hooks, pos.Fee, pos.TickSpacing)
		posKey := v4ComputePositionKey(posManager, tokenId, pos.TickLower, pos.TickUpper)
		pos.Liquidity = v4FetchPositionLiquidity(ctx, client, stateView, &stateViewABI, poolId, posKey)

		if pos.Liquidity == nil || pos.Liquidity.Sign() == 0 {
			diags = append(diags, fmt.Sprintf("tokenId %s: liquidity=0", tokenId))
		}

		// Resolve symbols via the shared V4 cache.
		pos.Token0Symbol = syms.getOrFetch(ctx, client, pos.Token0)
		if pos.Token0Symbol == "" {
			pos.Token0Symbol = pos.Token0.Hex()[:10]
		}
		pos.Token1Symbol = syms.getOrFetch(ctx, client, pos.Token1)
		if pos.Token1Symbol == "" {
			pos.Token1Symbol = pos.Token1.Hex()[:10]
		}

		// Handle native ETH (zero address = ETH in V4).
		if (pos.Token0 == common.Address{}) {
			pos.Token0Symbol = "ETH"
		}
		if (pos.Token1 == common.Address{}) {
			pos.Token1Symbol = "ETH"
		}

		pos.Token0Decimals = v4ERC20Decimals(ctx, client, pos.Token0)
		pos.Token1Decimals = v4ERC20Decimals(ctx, client, pos.Token1)

		pos.MinPrice = V4TickToPrice(pos.TickLower, pos.Token0Decimals, pos.Token1Decimals)
		pos.MaxPrice = V4TickToPrice(pos.TickUpper, pos.Token0Decimals, pos.Token1Decimals)

		positions = append(positions, pos)
	}

	return positions, balance, diags, nil
}

// ---- ERC-721 token ID enumeration ----

// v4EnumerateOwnedTokenIds returns up to 50 token IDs owned by owner.
// It uses ERC-721 Enumerable (tokenOfOwnerByIndex). If that reverts it falls
// back to Transfer event log scanning + ownerOf verification.
// Returns token IDs, per-step diagnostic lines, and any fatal error.
func v4EnumerateOwnedTokenIds(ctx context.Context, client *ethclient.Client, posManager, owner common.Address, expectedCount uint64) ([]*big.Int, []string, error) {
	var diags []string
	count := min(expectedCount, 50)

	// Try ERC-721 Enumerable path.
	first, err := v4TokenOfOwnerByIndex(ctx, client, posManager, owner, 0)
	if err != nil {
		diags = append(diags, fmt.Sprintf("tokenOfOwnerByIndex(0): error: %v", err))
		diags = append(diags, "falling back to Transfer event log scan")
	} else {
		diags = append(diags, fmt.Sprintf("tokenOfOwnerByIndex(0): tokenId=%s", first))
		ids := make([]*big.Int, 0, count)
		ids = append(ids, first)
		for i := uint64(1); i < count; i++ {
			id, e := v4TokenOfOwnerByIndex(ctx, client, posManager, owner, i)
			if e != nil {
				diags = append(diags, fmt.Sprintf("tokenOfOwnerByIndex(%d): error: %v", i, e))
				break
			}
			diags = append(diags, fmt.Sprintf("tokenOfOwnerByIndex(%d): tokenId=%s", i, id))
			ids = append(ids, id)
		}
		return ids, diags, nil
	}

	// Transfer event log fallback.
	transferSig := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	ownerTopic := common.BytesToHash(owner.Bytes())

	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, diags, fmt.Errorf("get block header: %w", err)
	}
	fromBlock := new(big.Int).Sub(header.Number, big.NewInt(2_000_000))
	if fromBlock.Sign() < 0 {
		fromBlock = big.NewInt(0)
	}
	diags = append(diags, fmt.Sprintf("Transfer-log scan from block %s", fromBlock))

	query := ethereum.FilterQuery{
		FromBlock: fromBlock,
		Addresses: []common.Address{posManager},
		Topics: [][]common.Hash{
			{transferSig},
			nil,          // from: any
			{ownerTopic}, // to: owner
		},
	}
	logs, err := client.FilterLogs(ctx, query)
	if err != nil {
		return nil, diags, fmt.Errorf("FilterLogs: %w", err)
	}
	diags = append(diags, fmt.Sprintf("Transfer-log: %d inbound transfer event(s) found", len(logs)))

	seen := make(map[string]bool)
	ids := make([]*big.Int, 0, expectedCount)
	for _, l := range logs {
		if len(l.Topics) < 4 {
			continue
		}
		tokenId := new(big.Int).SetBytes(l.Topics[3].Bytes())
		key := tokenId.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		currentOwner := v4OwnerOf(ctx, client, posManager, tokenId)
		if currentOwner == owner {
			diags = append(diags, fmt.Sprintf("Transfer-log: tokenId=%s confirmed owned", tokenId))
			ids = append(ids, tokenId)
		} else {
			diags = append(diags, fmt.Sprintf("Transfer-log: tokenId=%s no longer owned (ownerOf=%s)", tokenId, currentOwner.Hex()[:10]))
		}
		if uint64(len(ids)) >= 50 {
			break
		}
	}
	return ids, diags, nil
}

// ---- ERC-721 calls ----

// v4BalanceOf calls balanceOf(address) on the V4 NFT Position Manager.
func v4BalanceOf(ctx context.Context, client *ethclient.Client, posManager, owner common.Address) (uint64, error) {
	// selector: balanceOf(address) = 0x70a08231
	data := make([]byte, 36)
	copy(data[:4], []byte{0x70, 0xa0, 0x82, 0x31})
	copy(data[16:36], owner.Bytes()) // address right-aligned in 32-byte slot
	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &posManager, Data: data}, nil)
	if err != nil {
		return 0, err
	}
	if len(result) < 32 {
		return 0, fmt.Errorf("short response: %d bytes", len(result))
	}
	return new(big.Int).SetBytes(result[:32]).Uint64(), nil
}

// v4TokenOfOwnerByIndex calls tokenOfOwnerByIndex(address, uint256) on the V4 Position Manager.
func v4TokenOfOwnerByIndex(ctx context.Context, client *ethclient.Client, posManager, owner common.Address, index uint64) (*big.Int, error) {
	// selector: tokenOfOwnerByIndex(address,uint256) = 0x2f745c59
	data := make([]byte, 68)
	copy(data[:4], []byte{0x2f, 0x74, 0x5c, 0x59})
	copy(data[16:36], owner.Bytes())
	idxBytes := new(big.Int).SetUint64(index).Bytes()
	copy(data[68-len(idxBytes):68], idxBytes)
	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &posManager, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	if len(result) < 32 {
		return nil, fmt.Errorf("short response: %d bytes", len(result))
	}
	return new(big.Int).SetBytes(result[:32]), nil
}

// v4OwnerOf calls ownerOf(uint256) on the V4 Position Manager.
// Returns zero address on any error.
func v4OwnerOf(ctx context.Context, client *ethclient.Client, posManager common.Address, tokenId *big.Int) common.Address {
	// selector: ownerOf(uint256) = 0x6352211e
	data := make([]byte, 36)
	copy(data[:4], []byte{0x63, 0x52, 0x21, 0x1e})
	idBytes := tokenId.Bytes()
	copy(data[36-len(idBytes):36], idBytes)
	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &posManager, Data: data}, nil)
	if err != nil || len(result) < 32 {
		return common.Address{}
	}
	return common.BytesToAddress(result[12:32])
}

// ---- V4 positions(uint256) decode ----
//
// V4 positions(uint256 tokenId) ABI return layout (8 × 32-byte slots = 256 bytes):
//
//   Slot 0 (bytes   0– 31): PositionInfo bytes32
//                            bits[255:232] = tickLower (int24, big-endian, top 3 bytes)
//                            bits[231:208] = tickUpper (int24, big-endian, bytes 3–5)
//   Slot 1 (bytes  32– 63): PoolKey.currency0 (address)
//   Slot 2 (bytes  64– 95): PoolKey.currency1 (address)
//   Slot 3 (bytes  96–127): PoolKey.fee (uint24)
//   Slot 4 (bytes 128–159): PoolKey.tickSpacing (int24)
//   Slot 5 (bytes 160–191): PoolKey.hooks (address)
//   Slot 6 (bytes 192–223): feeGrowthInside0LastX128 (uint256, skipped)
//   Slot 7 (bytes 224–255): feeGrowthInside1LastX128 (uint256, skipped)

func v4FetchPosition(ctx context.Context, client *ethclient.Client, posManager common.Address, tokenId *big.Int, blockNumber ...*big.Int) (LiquidityPosition, error) {
	var pos LiquidityPosition
	pos.TokenID = tokenId

	// selector: positions(uint256) = 0x99fbab88
	data := make([]byte, 36)
	copy(data[:4], []byte{0x99, 0xfb, 0xab, 0x88})
	idBytes := tokenId.Bytes()
	copy(data[36-len(idBytes):36], idBytes)

	var atBlock *big.Int
	if len(blockNumber) > 0 {
		atBlock = blockNumber[0]
	}
	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &posManager, Data: data}, atBlock)
	if err != nil {
		return pos, err
	}
	if len(result) < 192 {
		return pos, fmt.Errorf("short positions response: %d bytes", len(result))
	}

	// PositionInfo bytes32: tickLower in first 3 bytes, tickUpper in bytes 3–5.
	posInfo := result[0:32]
	pos.TickLower = v4DecodeInt24Packed(posInfo[0:3])
	pos.TickUpper = v4DecodeInt24Packed(posInfo[3:6])

	pos.Token0 = common.BytesToAddress(result[32:64])   // PoolKey.currency0
	pos.Token1 = common.BytesToAddress(result[64:96])   // PoolKey.currency1
	pos.Fee = uint32(new(big.Int).SetBytes(result[96:128]).Uint64())  // PoolKey.fee
	pos.TickSpacing = v4DecodeInt24Slot(result[128:160])              // PoolKey.tickSpacing
	pos.Hooks = common.BytesToAddress(result[160:192])                // PoolKey.hooks

	return pos, nil
}

// v4DecodeInt24Packed decodes an int24 from 3 packed big-endian bytes (V4 PositionInfo format).
func v4DecodeInt24Packed(b []byte) int32 {
	raw := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
	if raw&0x800000 != 0 {
		return int32(raw | 0xFF000000)
	}
	return int32(raw)
}

// v4DecodeInt24Slot decodes a sign-extended int24 from a 32-byte ABI slot (right-aligned).
func v4DecodeInt24Slot(slot []byte) int32 {
	raw := uint32(slot[29])<<16 | uint32(slot[30])<<8 | uint32(slot[31])
	if raw&0x800000 != 0 {
		return int32(raw | 0xFF000000)
	}
	return int32(raw)
}

// ComputePoolId is the exported form of v4ComputePoolId, for use by external packages (e.g. indexer).
func ComputePoolId(currency0, currency1, hooks common.Address, fee uint32, tickSpacing int32) common.Hash {
	return v4ComputePoolId(currency0, currency1, hooks, fee, tickSpacing)
}

// ---- StateView: per-position liquidity ----

// v4ComputePoolId computes the V4 PoolId = keccak256(abi.encode(currency0, currency1, fee, tickSpacing, hooks)).
func v4ComputePoolId(currency0, currency1, hooks common.Address, fee uint32, tickSpacing int32) common.Hash {
	// ABI encode: 5 slots × 32 bytes = 160 bytes (all static types).
	data := make([]byte, 160)

	copy(data[12:32], currency0.Bytes())  // slot 0: currency0
	copy(data[44:64], currency1.Bytes())  // slot 1: currency1

	// slot 2: fee (uint24, right-aligned)
	data[93] = byte(fee >> 16)
	data[94] = byte(fee >> 8)
	data[95] = byte(fee)

	// slot 3: tickSpacing (int24, right-aligned, two's complement for negatives)
	if tickSpacing < 0 {
		for i := 96; i < 125; i++ {
			data[i] = 0xFF // sign extension
		}
	}
	raw := uint32(tickSpacing) & 0xFFFFFF
	data[125] = byte(raw >> 16)
	data[126] = byte(raw >> 8)
	data[127] = byte(raw)

	copy(data[140:160], hooks.Bytes()) // slot 4: hooks

	return crypto.Keccak256Hash(data)
}

// v4ComputePositionKey computes the StateView position key:
// keccak256(abi.encodePacked(positionManager, tickLower, tickUpper, salt))
// where salt = bytes32(tokenId).
func v4ComputePositionKey(posManager common.Address, tokenId *big.Int, tickLower, tickUpper int32) common.Hash {
	// packed: 20 + 3 + 3 + 32 = 58 bytes
	data := make([]byte, 58)
	copy(data[0:20], posManager.Bytes())

	rawLower := uint32(tickLower) & 0xFFFFFF
	data[20] = byte(rawLower >> 16)
	data[21] = byte(rawLower >> 8)
	data[22] = byte(rawLower)

	rawUpper := uint32(tickUpper) & 0xFFFFFF
	data[23] = byte(rawUpper >> 16)
	data[24] = byte(rawUpper >> 8)
	data[25] = byte(rawUpper)

	// salt = bytes32(tokenId), right-aligned
	idBytes := tokenId.Bytes()
	copy(data[58-len(idBytes):58], idBytes)

	return crypto.Keccak256Hash(data)
}

// v4FetchPositionLiquidity calls StateView.getPositionInfo(poolId, positionKey) and returns the liquidity.
// Returns nil on any error so the caller can gracefully skip.
func v4FetchPositionLiquidity(ctx context.Context, client *ethclient.Client, stateView common.Address, stateViewABI *abi.ABI, poolId, posKey common.Hash) *big.Int {
	calldata, err := stateViewABI.Pack("getPositionInfo", poolId, posKey)
	if err != nil {
		return nil
	}
	raw, err := client.CallContract(ctx, ethereum.CallMsg{To: &stateView, Data: calldata}, nil)
	if err != nil || len(raw) < 32 {
		return nil
	}
	vals, err := stateViewABI.Unpack("getPositionInfo", raw)
	if err != nil || len(vals) == 0 {
		return nil
	}
	liq, _ := vals[0].(*big.Int)
	return liq
}

// ---- Price helpers ----

// v4RawSpotPrice returns 1.0001^tick — the V4 tick-to-price formula in raw
// (smallest-unit) terms, i.e. token1_raw per token0_raw, with no decimal
// adjustment. Shared by V4TickToPrice (human-readable display) and the V4
// quote code's price-impact calc (helpers/uniswap_v4_quote.go), which needs
// the unadjusted raw ratio to stay consistent with how amountIn/amountOut
// are already raw big.Int values throughout this codebase's V2/V3 quote math.
func v4RawSpotPrice(tick int32) float64 {
	return math.Pow(1.0001, float64(tick))
}

// V4TickToPrice converts a V4 tick to a human-readable price (token1 per
// token0). Exported for the oscillator backfill (store package), which
// derives V4 basket-swap prices from the same tick the live quote code uses.
func V4TickToPrice(tick int32, decimals0, decimals1 uint8) float64 {
	decimalAdjust := math.Pow(10, float64(int(decimals0)-int(decimals1)))
	return v4RawSpotPrice(tick) * decimalAdjust
}

// v4ERC20Decimals fetches the decimals() of an ERC-20 token, returning 18 on failure.
// Returns 18 for the zero address (native ETH).
func v4ERC20Decimals(ctx context.Context, client *ethclient.Client, addr common.Address) uint8 {
	if (addr == common.Address{}) {
		return 18
	}
	callCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	result, err := client.CallContract(callCtx, ethereum.CallMsg{
		To:   &addr,
		Data: []byte{0x31, 0x3c, 0xe5, 0x67}, // decimals()
	}, nil)
	if err != nil || len(result) < 32 {
		return 18
	}
	return result[31]
}
