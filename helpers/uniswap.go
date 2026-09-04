package helpers

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// UniswapV2Pair represents a Uniswap V2 pair contract
type UniswapV2Pair struct {
	Address common.Address
	Token0  common.Address
	Token1  common.Address
}

// SwapQuote represents the result of a swap price query
type SwapQuote struct {
	AmountIn       *big.Int // Input amount
	AmountOut      *big.Int // Expected output amount
	Token0Reserve  *big.Int // Reserve of token0 (V2 only; zero for V3)
	Token1Reserve  *big.Int // Reserve of token1 (V2 only; zero for V3)
	PriceImpact    float64  // Price impact percentage
	EffectivePrice float64  // Effective price (output/input)
	IsV3           bool     // true when quote came from a Uniswap V3 pool
	IsV4           bool     // true when quote came from a Uniswap V4 pool
	HookAddr       common.Address // V4 pool's hook contract; zero if none or not V4
}

// Uniswap V2 function selectors
var (
	// getReserves() returns (uint112 reserve0, uint112 reserve1, uint32 blockTimestampLast)
	getReservesSelector = []byte{0x09, 0x02, 0xf1, 0xac}
	// token0() returns (address)
	token0Selector = []byte{0x0d, 0xfe, 0x16, 0x81}
	// token1() returns (address)
	token1Selector = []byte{0xd2, 0x12, 0x20, 0xa7}
)

// Well-known Uniswap V2 pair addresses on Ethereum mainnet
var (
	// USDC/WETH pair on Uniswap V2 (this is the actual mainnet pair)
	// Verified at: https://v2.info.uniswap.org/pair/0xb4e16d0168e52d35cacd2c6185b44281ec28c9dc
	USDCWETHPairAddress = common.HexToAddress("0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc")
	// WETH address
	WETHAddress = common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")
	// USDC address
	USDCAddress = common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	// DAI/WETH pair (for testing alternative)
	DAIWETHPairAddress = common.HexToAddress("0xA478c2975Ab1Ea89e8196811F51A7B7Ade33eB11")
	// DAI address
	DAIAddress = common.HexToAddress("0x6B175474E89094C44Da98b954EedeAC495271d0F")
)

// SepoliaChainID is the chain ID of the Sepolia testnet (EIP-155).
const SepoliaChainID = 11155111

// UniswapNetworkAddresses bundles the Uniswap V2 router/factory contract
// addresses plus the watched-token and reference-pair addresses to use on a
// given network. UniswapAddressesForChain is the single place that decides
// which set applies.
type UniswapNetworkAddresses struct {
	Router  common.Address
	Factory common.Address

	WETH   common.Address
	USDC   common.Address
	USDT   common.Address
	DAI    common.Address
	SPCXon common.Address // SpaceX (Ondo Tokenized) — mainnet only

	// Uniswap V3
	FactoryV3    common.Address // V3 factory, for on-chain getPool() lookups
	QuoterV2     common.Address // QuoterV2 for off-chain quote simulation
	SwapRouterV3 common.Address // SwapRouter02

	// Uniswap V4. Zero on networks where V4 isn't deployed/supported — callers
	// gate on V4PoolManager != (common.Address{}) before trying the V4 tier.
	V4PoolManager     common.Address // singleton pool ledger; also holds Initialize/Swap/etc. events
	V4StateView       common.Address // read-only slot0/liquidity peripheral (PoolManager storage isn't eth_call-readable)
	V4Quoter          common.Address // V4Quoter, for off-chain quote simulation
	V4PositionManager common.Address // NFT position manager (liquidity positions, not swaps)
	UniversalRouter   common.Address // swap execution entrypoint (Permit2-based allowance model)
}

// mainnetUniswapAddresses mirrors the package-level mainnet vars above so the
// live-RPC tests in uniswap_test.go (which run against mainnet) keep working
// against the same addresses. Router/Factory/USDT were previously inlined at
// their call sites (commands.go, model.go) — named here for symmetry with Sepolia.
var mainnetUniswapAddresses = UniswapNetworkAddresses{
	Router:  common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D"),
	Factory: common.HexToAddress("0x5C69bEe701ef814a2B6a3EDD4B1652CB9cc5aA6f"),

	WETH:   WETHAddress,
	USDC:   USDCAddress,
	USDT:   common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7"),
	DAI:    DAIAddress,
	SPCXon: common.HexToAddress("0xc9eef266834730340A55B6CC24621B31BAF55581"),

	FactoryV3:    common.HexToAddress("0x1F98431c8aD98523631AE4a59f267346ea31F984"),
	QuoterV2:     common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e"),
	SwapRouterV3: common.HexToAddress("0x68b3465833fb72A70ecDF485E0e4C7bD8665Fc45"),

	// V4 addresses reuse the constants/vars already defined for the existing
	// V4 read-side code (event listener, NFT positions) rather than being
	// retyped here. V4Quoter and UniversalRouter are new, cited from
	// Uniswap's official deployments doc (developers.uniswap.org/contracts/v4/deployments).
	V4PoolManager:     common.HexToAddress(V4PoolManagerAddress),
	V4StateView:       common.HexToAddress(V4StateViewAddress),
	V4Quoter:          common.HexToAddress("0x52f0e24d1c21c8a0cb1e5a5dd6198556bd9e1203"),
	V4PositionManager: v4NftPositionManager,
	UniversalRouter:   common.HexToAddress("0x66a9893cc07d91d95644aedd05d03f95e1dba8af"),
}

// sepoliaUniswapAddresses holds the Uniswap V2/V3/V4 deployment and token
// addresses on Sepolia (chain ID 11155111). V2 addresses verified directly
// on-chain against a public Sepolia RPC: router.factory()/router.WETH()
// match the listed Factory/WETH, each token's symbol()/decimals() match, and
// each pair has live non-zero reserves on the Factory's pair list. V3/V4
// addresses cross-checked against two independent official Uniswap sources
// (developers.uniswap.org's deployments pages and the Uniswap/sdks monorepo's
// sdk-core/src/addresses.ts) and confirmed to hold live contract bytecode via
// eth_getCode against a public Sepolia RPC.
var sepoliaUniswapAddresses = UniswapNetworkAddresses{
	Router:  common.HexToAddress("0xeE567Fe1712Faf6149d80dA1E6934E354124CfE3"),
	Factory: common.HexToAddress("0xF62c03E08ada871A0bEb309762E260a7a6a880E6"),

	WETH: common.HexToAddress("0xfFf9976782d46CC05630D1f6eBAB18b2324d6B14"),
	USDC: common.HexToAddress("0x94a9D9AC8a22534E3FaCa9F4e7F2E2cf85d5E4C8"),
	USDT: common.HexToAddress("0xaa8E23Fb1079EA71e0a56F48a2aa51851D8433D0"),
	DAI:  common.HexToAddress("0xB4F1737Af37711e9A5890D9510c9bB60e170CB0D"),

	FactoryV3:    common.HexToAddress("0x0227628f3F023bb0B980b67D528571c95c6DaC1c"),
	QuoterV2:     common.HexToAddress("0xEd1f6473345F45b75F8179591dd5bA1888cf2FB3"),
	SwapRouterV3: common.HexToAddress("0x3bFA4769FB09eefC5a80d6E87c3B9C650f7Ae48E"),

	V4PoolManager:     common.HexToAddress("0xE03A1074c86CFeDd5C142C4F04F1a1536e203543"),
	V4StateView:       common.HexToAddress("0xe1dd9c3fa50edb962e442f60dfbc432e24537e4c"),
	V4Quoter:          common.HexToAddress("0x61b3f2011a92d183c7dbadbda940a7555ccf9227"),
	V4PositionManager: common.HexToAddress("0x429ba70129df741B2Ca2a85BC3A2a3328e5c09b4"),
	UniversalRouter:   common.HexToAddress("0x3a9d48ab9751398bbfa63ad67599bb04e4bdf98b"),
}

// UniswapAddressesForChain returns the Uniswap V2 router/factory/token/pair
// addresses to use for chainID. Nil or unrecognized chain IDs default to
// Ethereum mainnet, matching the app's existing default RPC/network.
func UniswapAddressesForChain(chainID *big.Int) UniswapNetworkAddresses {
	if chainID != nil && chainID.Cmp(big.NewInt(SepoliaChainID)) == 0 {
		return sepoliaUniswapAddresses
	}
	return mainnetUniswapAddresses
}

// addressesForClient detects client's chain and resolves the matching
// UniswapNetworkAddresses, mirroring rpc.ConnectWithTimeout's best-effort
// chain detection (a failed lookup is treated as mainnet, same as nil).
func addressesForClient(ctx context.Context, client *ethclient.Client) UniswapNetworkAddresses {
	chainID, _ := client.ChainID(ctx)
	return UniswapAddressesForChain(chainID)
}

// isMainnet reports whether chainID (nil-safe, nil = mainnet) refers to
// Ethereum mainnet, for the handful of call sites that need a mainnet-only
// bound (e.g. FetchPoolKey's block-range floor) rather than a full address set.
func isMainnet(chainID *big.Int) bool {
	return chainID == nil || chainID.Cmp(big.NewInt(1)) == 0
}

// GetUniswapV2Pair fetches the token addresses for a Uniswap V2 pair
func GetUniswapV2Pair(client *ethclient.Client, pairAddress common.Address) (*UniswapV2Pair, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get token0
	token0Msg := ethereum.CallMsg{
		To:   &pairAddress,
		Data: token0Selector,
	}
	token0Data, err := client.CallContract(ctx, token0Msg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get token0: %w", err)
	}
	if len(token0Data) != 32 {
		return nil, fmt.Errorf("token0 call returned unexpected data length: %d", len(token0Data))
	}

	// Get token1
	token1Msg := ethereum.CallMsg{
		To:   &pairAddress,
		Data: token1Selector,
	}
	token1Data, err := client.CallContract(ctx, token1Msg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get token1: %w", err)
	}
	if len(token1Data) != 32 {
		return nil, fmt.Errorf("token1 call returned unexpected data length: %d", len(token1Data))
	}

	return &UniswapV2Pair{
		Address: pairAddress,
		Token0:  common.BytesToAddress(token0Data),
		Token1:  common.BytesToAddress(token1Data),
	}, nil
}

// fetchReserves returns (reserve0, reserve1) for pairAddress.
func fetchReserves(ctx context.Context, client *ethclient.Client, pairAddress common.Address) (reserve0, reserve1 *big.Int, err error) {
	msg := ethereum.CallMsg{To: &pairAddress, Data: getReservesSelector}
	data, err := client.CallContract(ctx, msg, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get reserves: %w", err)
	}
	if len(data) < 32 {
		return nil, nil, fmt.Errorf("invalid reserves data length: %d", len(data))
	}
	reserve0 = new(big.Int).SetBytes(data[0:32])
	reserve1 = big.NewInt(0)
	if len(data) >= 64 {
		reserve1 = new(big.Int).SetBytes(data[32:64])
	}
	return reserve0, reserve1, nil
}

// V2ReservesToPrice returns token0's price in token1 units from a V2 pair's
// reserves, decimal-adjusted. Exported for the oscillator backfill (store
// package), which reads V2 Sync event reserves directly rather than
// simulating a swap.
func V2ReservesToPrice(reserve0, reserve1 *big.Int, decimals0, decimals1 uint8) float64 {
	if reserve0.Sign() <= 0 || reserve1.Sign() <= 0 {
		return 0
	}
	ratio := new(big.Float).Quo(new(big.Float).SetInt(reserve1), new(big.Float).SetInt(reserve0))
	decimalAdjust := new(big.Float).SetFloat64(math.Pow(10, float64(int(decimals0)-int(decimals1))))
	price, _ := new(big.Float).Mul(ratio, decimalAdjust).Float64()
	return price
}

// orderReserves returns (reserveIn, reserveOut) given which token is the input.
func orderReserves(pair *UniswapV2Pair, tokenIn common.Address, r0, r1 *big.Int) (reserveIn, reserveOut *big.Int, err error) {
	switch tokenIn {
	case pair.Token0:
		return r0, r1, nil
	case pair.Token1:
		return r1, r0, nil
	default:
		return nil, nil, fmt.Errorf("tokenIn %s not in pair (token0: %s, token1: %s)",
			tokenIn.Hex(), pair.Token0.Hex(), pair.Token1.Hex())
	}
}

// priceImpactFromSpot returns the price impact percentage given a spot price
// and the effective price actually received, both expressed as the same
// raw-unit (not decimal-adjusted) ratio. Shared by computePriceImpact (V2,
// spot derived from pool reserves) and the V4 quote code (spot derived from
// the pool's current tick via v4RawSpotPrice).
func priceImpactFromSpot(spot, effectivePrice float64) float64 {
	if spot <= 0 {
		return 0
	}
	return ((spot - effectivePrice) / spot) * 100
}

// computePriceImpact returns the price impact percentage given spot and effective prices.
func computePriceImpact(reserveIn, reserveOut *big.Int, effectivePrice float64) float64 {
	if reserveIn.Sign() <= 0 || reserveOut.Sign() <= 0 {
		return 0
	}
	spotPrice := new(big.Float).Quo(new(big.Float).SetInt(reserveOut), new(big.Float).SetInt(reserveIn))
	spot, _ := spotPrice.Float64()
	return priceImpactFromSpot(spot, effectivePrice)
}

// GetSwapQuote calculates the expected output amount for a swap using the Uniswap V2 formula.
// tokenIn is the token being sold; amountIn is the amount to sell.
func GetSwapQuote(client *ethclient.Client, pairAddress common.Address, tokenIn common.Address, amountIn *big.Int) (*SwapQuote, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pair, err := GetUniswapV2Pair(client, pairAddress)
	if err != nil {
		return nil, err
	}

	reserve0, reserve1, err := fetchReserves(ctx, client, pairAddress)
	if err != nil {
		return nil, err
	}

	reserveIn, reserveOut, err := orderReserves(pair, tokenIn, reserve0, reserve1)
	if err != nil {
		return nil, err
	}

	// amountOut = (amountIn * 997 * reserveOut) / (reserveIn * 1000 + amountIn * 997)
	amountInWithFee := new(big.Int).Mul(amountIn, big.NewInt(997))
	numerator := new(big.Int).Mul(amountInWithFee, reserveOut)
	denominator := new(big.Int).Add(new(big.Int).Mul(reserveIn, big.NewInt(1000)), amountInWithFee)
	amountOut := new(big.Int).Div(numerator, denominator)

	effectivePrice := 0.0
	if amountIn.Sign() > 0 {
		ef := new(big.Float).Quo(new(big.Float).SetInt(amountOut), new(big.Float).SetInt(amountIn))
		effectivePrice, _ = ef.Float64()
	}

	return &SwapQuote{
		AmountIn:       amountIn,
		AmountOut:      amountOut,
		Token0Reserve:  reserve0,
		Token1Reserve:  reserve1,
		PriceImpact:    computePriceImpact(reserveIn, reserveOut, effectivePrice),
		EffectivePrice: effectivePrice,
	}, nil
}

// GetReverseSwapQuote calculates the required input amount to receive a desired amountOut.
func GetReverseSwapQuote(client *ethclient.Client, pairAddress, tokenIn common.Address, amountOut *big.Int) (*SwapQuote, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pair, err := GetUniswapV2Pair(client, pairAddress)
	if err != nil {
		return nil, err
	}

	reserve0, reserve1, err := fetchReserves(ctx, client, pairAddress)
	if err != nil {
		return nil, err
	}

	reserveIn, reserveOut, err := orderReserves(pair, tokenIn, reserve0, reserve1)
	if err != nil {
		return nil, err
	}

	// amountIn = (reserveIn * amountOut * 1000) / ((reserveOut - amountOut) * 997) + 1
	denominator := new(big.Int).Sub(reserveOut, amountOut)
	denominator.Mul(denominator, big.NewInt(997))
	if denominator.Sign() <= 0 {
		return nil, fmt.Errorf("insufficient liquidity for desired output amount")
	}
	numerator := new(big.Int).Mul(new(big.Int).Mul(reserveIn, amountOut), big.NewInt(1000))
	amountIn := new(big.Int).Add(new(big.Int).Div(numerator, denominator), big.NewInt(1))

	effectivePrice := 0.0
	if amountIn.Sign() > 0 {
		ef := new(big.Float).Quo(new(big.Float).SetInt(amountOut), new(big.Float).SetInt(amountIn))
		effectivePrice, _ = ef.Float64()
	}

	return &SwapQuote{
		AmountIn:       amountIn,
		AmountOut:      amountOut,
		Token0Reserve:  reserve0,
		Token1Reserve:  reserve1,
		PriceImpact:    computePriceImpact(reserveIn, reserveOut, effectivePrice),
		EffectivePrice: effectivePrice,
	}, nil
}

// V3 function selectors used by ResolvePairOnChain.
var (
	// getPair(address,address) returns (address)
	v2GetPairSelector = []byte{0xe6, 0xa4, 0x39, 0x05}
	// getPool(address,address,uint24) returns (address)
	v3GetPoolSelector = []byte{0x16, 0x98, 0xee, 0x82}
	// liquidity() returns (uint128)
	v3LiquiditySelector = []byte{0x1a, 0x68, 0x65, 0x02}
)

// v3FeeTiers are the standard Uniswap V3 fee tiers, in the order
// ResolvePairOnChain probes them (most-commonly-used first).
var v3FeeTiers = []uint32{3000, 500, 10000, 100}

// v2FactoryGetPair calls the V2 factory's getPair(tokenA, tokenB) and returns
// the pair address, or the zero address if no pair exists.
func v2FactoryGetPair(ctx context.Context, client *ethclient.Client, factory, tokenA, tokenB common.Address) (common.Address, error) {
	data := append(append([]byte{}, v2GetPairSelector...), common.LeftPadBytes(tokenA.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(tokenB.Bytes(), 32)...)
	out, err := client.CallContract(ctx, ethereum.CallMsg{To: &factory, Data: data}, nil)
	if err != nil {
		return common.Address{}, err
	}
	if len(out) < 32 {
		return common.Address{}, nil
	}
	return common.BytesToAddress(out[12:32]), nil
}

// v3FactoryGetPool calls the V3 factory's getPool(tokenA, tokenB, fee) and
// returns the pool address, or the zero address if no pool exists at that fee tier.
func v3FactoryGetPool(ctx context.Context, client *ethclient.Client, factoryV3, tokenA, tokenB common.Address, fee uint32) (common.Address, error) {
	data := append(append([]byte{}, v3GetPoolSelector...), common.LeftPadBytes(tokenA.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(tokenB.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(big.NewInt(int64(fee)).Bytes(), 32)...)
	out, err := client.CallContract(ctx, ethereum.CallMsg{To: &factoryV3, Data: data}, nil)
	if err != nil {
		return common.Address{}, err
	}
	if len(out) < 32 {
		return common.Address{}, nil
	}
	return common.BytesToAddress(out[12:32]), nil
}

// v3PoolLiquidity calls a V3 pool's liquidity() and returns the raw value.
func v3PoolLiquidity(ctx context.Context, client *ethclient.Client, pool common.Address) (*big.Int, error) {
	out, err := client.CallContract(ctx, ethereum.CallMsg{To: &pool, Data: v3LiquiditySelector}, nil)
	if err != nil {
		return nil, err
	}
	if len(out) < 32 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(out), nil
}

// ResolvePairOnChain finds the best Uniswap pool for tokenA/tokenB by
// querying the V2 and V3 factories directly, instead of relying on a
// hardcoded pair-address table. It only considers a candidate "real" if it
// has actual liquidity — a pool can exist on-chain but be empty (just
// deployed, never seeded), and naively taking the first non-zero address
// risks picking a dead pool over the one that's actually tradable.
//
// V3 is preferred over V2 when both have liquidity (matching this app's
// prior hand-picked behavior for SPCXon, whose deepest liquidity is on V3).
// Among V3 fee tiers, the one with the highest liquidity() wins. V4 is tried
// last, only when neither V2 nor V3 has liquidity — V4 has no factory to
// query live, so this tier costs more (see resolveV4Pool) and is only worth
// paying when the established versions come up empty.
func ResolvePairOnChain(ctx context.Context, client *ethclient.Client, addrs UniswapNetworkAddresses, tokenA, tokenB common.Address) (ResolvedPool, error) {
	var bestV3Addr common.Address
	var bestV3Fee uint32
	var bestV3Liquidity *big.Int

	if addrs.FactoryV3 != (common.Address{}) {
		for _, f := range v3FeeTiers {
			pool, perr := v3FactoryGetPool(ctx, client, addrs.FactoryV3, tokenA, tokenB, f)
			if perr != nil || pool == (common.Address{}) {
				continue
			}
			liq, lerr := v3PoolLiquidity(ctx, client, pool)
			if lerr != nil || liq.Sign() <= 0 {
				continue
			}
			if bestV3Liquidity == nil || liq.Cmp(bestV3Liquidity) > 0 {
				bestV3Addr = pool
				bestV3Fee = f
				bestV3Liquidity = liq
			}
		}
	}
	if bestV3Liquidity != nil {
		return ResolvedPool{Version: PoolVersionV3, PairAddr: bestV3Addr, V3Fee: bestV3Fee}, nil
	}

	if addrs.Factory != (common.Address{}) {
		pair, perr := v2FactoryGetPair(ctx, client, addrs.Factory, tokenA, tokenB)
		if perr == nil && pair != (common.Address{}) {
			r0, r1, rerr := fetchReserves(ctx, client, pair)
			if rerr == nil && r0.Sign() > 0 && r1.Sign() > 0 {
				return ResolvedPool{Version: PoolVersionV2, PairAddr: pair}, nil
			}
		}
	}

	if addrs.V4PoolManager != (common.Address{}) {
		if pool, ok := resolveV4Pool(ctx, client, addrs, tokenA, tokenB); ok {
			return pool, nil
		}
	}

	return ResolvedPool{}, fmt.Errorf("no Uniswap V2, V3, or V4 pool found")
}

// FormatSwapQuote returns a human-readable string for a swap quote
func FormatSwapQuote(quote *SwapQuote, tokenInSymbol, tokenOutSymbol string, tokenInDecimals, tokenOutDecimals uint8) string {
	if quote == nil {
		return "No quote available"
	}

	// Format amounts with proper decimals
	divisorIn := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(tokenInDecimals)), nil))
	amountInFormatted := new(big.Float).Quo(new(big.Float).SetInt(quote.AmountIn), divisorIn)

	divisorOut := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(tokenOutDecimals)), nil))
	amountOutFormatted := new(big.Float).Quo(new(big.Float).SetInt(quote.AmountOut), divisorOut)

	return fmt.Sprintf("%s %s → %s %s (impact: %.2f%%)",
		amountInFormatted.Text('f', 4), tokenInSymbol,
		amountOutFormatted.Text('f', 4), tokenOutSymbol,
		quote.PriceImpact)
}
