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

// Uniswap V3 function selectors (verified against mainnet).
// quoteExactInputSingle((address,address,uint256,uint24,uint160))  → 0xc6a5026a
// quoteExactOutputSingle((address,address,uint256,uint24,uint160)) → 0xbd21704a
// slot0()                                                          → 0x3850c7bd
var (
	v3QuoteExactInputSelector  = []byte{0xc6, 0xa5, 0x02, 0x6a}
	v3QuoteExactOutputSelector = []byte{0xbd, 0x21, 0x70, 0x4a}
	v3Slot0Selector            = []byte{0x38, 0x50, 0xc7, 0xbd}
)

// v3AbiEncodeAddress left-pads an address to 32 bytes (ABI word).
func v3AbiEncodeAddress(addr common.Address) []byte {
	b := make([]byte, 32)
	copy(b[12:], addr[:])
	return b
}

// v3AbiEncodeUint256 right-aligns v into a 32-byte ABI word.
func v3AbiEncodeUint256(v *big.Int) []byte {
	b := make([]byte, 32)
	vb := v.Bytes()
	copy(b[32-len(vb):], vb)
	return b
}

// v3BuildQuoteCalldata ABI-encodes the QuoterV2 struct argument:
// (address tokenIn, address tokenOut, uint256 amount, uint24 fee, uint160 sqrtPriceLimitX96=0)
func v3BuildQuoteCalldata(selector []byte, tokenIn, tokenOut common.Address, amount *big.Int, fee uint32) []byte {
	var d []byte
	d = append(d, selector...)
	d = append(d, v3AbiEncodeAddress(tokenIn)...)
	d = append(d, v3AbiEncodeAddress(tokenOut)...)
	d = append(d, v3AbiEncodeUint256(amount)...)
	d = append(d, v3AbiEncodeUint256(new(big.Int).SetUint64(uint64(fee)))...)
	d = append(d, v3AbiEncodeUint256(big.NewInt(0))...) // sqrtPriceLimitX96 = 0 (no limit)
	return d
}

// v3ReadSqrtPriceX96 calls slot0() on poolAddr and returns sqrtPriceX96.
func v3ReadSqrtPriceX96(ctx context.Context, client *ethclient.Client, poolAddr common.Address) (*big.Int, error) {
	msg := ethereum.CallMsg{To: &poolAddr, Data: v3Slot0Selector}
	data, err := client.CallContract(ctx, msg, nil)
	if err != nil {
		return nil, fmt.Errorf("slot0 call failed: %w", err)
	}
	if len(data) < 32 {
		return nil, fmt.Errorf("slot0 returned short data: %d bytes", len(data))
	}
	return new(big.Int).SetBytes(data[0:32]), nil
}

// SqrtPriceX96ToPrice converts a Q64.96 sqrtPriceX96 (V3 slot0()/Swap event
// field) into token0's price in token1 units, decimal-adjusted. Computed in
// big.Float throughout — sqrtPriceX96 carries far more precision than
// float64 holds, and squaring after an early truncation to float64 would
// compound that error. Exported for the oscillator backfill (store package).
func SqrtPriceX96ToPrice(sqrtPriceX96 *big.Int, decimals0, decimals1 uint8) float64 {
	q96 := new(big.Float).SetInt(new(big.Int).Lsh(big.NewInt(1), 96))
	sqrtP := new(big.Float).Quo(new(big.Float).SetInt(sqrtPriceX96), q96)
	rawPrice := new(big.Float).Mul(sqrtP, sqrtP)
	decimalAdjust := new(big.Float).SetFloat64(math.Pow(10, float64(int(decimals0)-int(decimals1))))
	price, _ := new(big.Float).Mul(rawPrice, decimalAdjust).Float64()
	return price
}

// v3PriceImpact computes price impact from sqrtPriceX96 before and after.
// Uses the linear approximation dP/P ≈ 2·d(sqrtP)/sqrtP, valid for small moves.
func v3PriceImpact(sqrtBefore, sqrtAfter *big.Int) float64 {
	if sqrtBefore == nil || sqrtBefore.Sign() <= 0 {
		return 0
	}
	delta := new(big.Int).Sub(sqrtAfter, sqrtBefore)
	if delta.Sign() < 0 {
		delta.Neg(delta)
	}
	bf := new(big.Float)
	impact, _ := bf.Quo(
		new(big.Float).SetInt(delta),
		new(big.Float).SetInt(sqrtBefore),
	).Float64()
	return impact * 2 * 100
}

// GetV3SwapQuote fetches an exact-input quote from the Uniswap V3 QuoterV2.
// poolAddr is used to read the current sqrtPriceX96 for price-impact calculation.
func GetV3SwapQuote(
	client *ethclient.Client,
	quoterV2, poolAddr, tokenIn, tokenOut common.Address,
	fee uint32,
	amountIn *big.Int,
) (*SwapQuote, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sqrtBefore, err := v3ReadSqrtPriceX96(ctx, client, poolAddr)
	if err != nil {
		return nil, err
	}

	calldata := v3BuildQuoteCalldata(v3QuoteExactInputSelector, tokenIn, tokenOut, amountIn, fee)
	msg := ethereum.CallMsg{To: &quoterV2, Data: calldata}
	data, err := client.CallContract(ctx, msg, nil)
	if err != nil {
		return nil, fmt.Errorf("quoteExactInputSingle failed: %w", err)
	}
	if len(data) < 64 {
		return nil, fmt.Errorf("quoteExactInputSingle returned short data: %d bytes", len(data))
	}

	amountOut := new(big.Int).SetBytes(data[0:32])
	sqrtAfter := new(big.Int).SetBytes(data[32:64])

	effectivePrice := 0.0
	if amountIn.Sign() > 0 {
		ep := new(big.Float).Quo(new(big.Float).SetInt(amountOut), new(big.Float).SetInt(amountIn))
		effectivePrice, _ = ep.Float64()
	}

	return &SwapQuote{
		AmountIn:       amountIn,
		AmountOut:      amountOut,
		PriceImpact:    v3PriceImpact(sqrtBefore, sqrtAfter),
		EffectivePrice: effectivePrice,
		IsV3:           true,
	}, nil
}

// GetV3ReverseSwapQuote fetches an exact-output quote from the Uniswap V3 QuoterV2.
// Returns the required amountIn to receive exactly amountOut of tokenOut.
func GetV3ReverseSwapQuote(
	client *ethclient.Client,
	quoterV2, poolAddr, tokenIn, tokenOut common.Address,
	fee uint32,
	amountOut *big.Int,
) (*SwapQuote, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sqrtBefore, err := v3ReadSqrtPriceX96(ctx, client, poolAddr)
	if err != nil {
		return nil, err
	}

	calldata := v3BuildQuoteCalldata(v3QuoteExactOutputSelector, tokenIn, tokenOut, amountOut, fee)
	msg := ethereum.CallMsg{To: &quoterV2, Data: calldata}
	data, err := client.CallContract(ctx, msg, nil)
	if err != nil {
		return nil, fmt.Errorf("quoteExactOutputSingle failed: %w", err)
	}
	if len(data) < 64 {
		return nil, fmt.Errorf("quoteExactOutputSingle returned short data: %d bytes", len(data))
	}

	amountIn := new(big.Int).SetBytes(data[0:32])
	sqrtAfter := new(big.Int).SetBytes(data[32:64])

	effectivePrice := 0.0
	if amountIn.Sign() > 0 {
		ep := new(big.Float).Quo(new(big.Float).SetInt(amountOut), new(big.Float).SetInt(amountIn))
		effectivePrice, _ = ep.Float64()
	}

	return &SwapQuote{
		AmountIn:       amountIn,
		AmountOut:      amountOut,
		PriceImpact:    v3PriceImpact(sqrtBefore, sqrtAfter),
		EffectivePrice: effectivePrice,
		IsV3:           true,
	}, nil
}
