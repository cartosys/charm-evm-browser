package helpers

import (
	"math"
	"math/big"
	"testing"
)

func TestV2ReservesToPrice(t *testing.T) {
	tests := []struct {
		name                   string
		reserve0, reserve1     *big.Int
		decimals0, decimals1   uint8
		want                   float64
	}{
		{
			name: "equal decimals", reserve0: big.NewInt(100), reserve1: big.NewInt(250),
			decimals0: 0, decimals1: 0, want: 2.5,
		},
		{
			name:      "18dp token0 vs 6dp token1 (1 token = 2000 USDC)",
			reserve0:  new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil), // 1 token, 18dp
			reserve1:  new(big.Int).Mul(big.NewInt(2000), big.NewInt(1_000_000)), // 2000 USDC, 6dp
			decimals0: 18, decimals1: 6, want: 2000,
		},
		{
			name: "zero reserve", reserve0: big.NewInt(0), reserve1: big.NewInt(100),
			decimals0: 18, decimals1: 18, want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := V2ReservesToPrice(tt.reserve0, tt.reserve1, tt.decimals0, tt.decimals1)
			if math.Abs(got-tt.want) > 1e-6 {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSqrtPriceX96ToPrice(t *testing.T) {
	q96 := new(big.Int).Lsh(big.NewInt(1), 96)

	tests := []struct {
		name                 string
		sqrtPriceX96         *big.Int
		decimals0, decimals1 uint8
		want                 float64
	}{
		{
			name:         "sqrtPriceX96 = 2^96 (raw price 1), equal decimals",
			sqrtPriceX96: q96,
			decimals0:    18, decimals1: 18, want: 1,
		},
		{
			name:         "sqrtPriceX96 = 2*2^96 (raw price 4), equal decimals",
			sqrtPriceX96: new(big.Int).Mul(q96, big.NewInt(2)),
			decimals0:    18, decimals1: 18, want: 4,
		},
		{
			name:         "raw price 1, 18dp token0 vs 6dp token1 -> decimal-adjusted",
			sqrtPriceX96: q96,
			decimals0:    18, decimals1: 6, want: 1e12,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SqrtPriceX96ToPrice(tt.sqrtPriceX96, tt.decimals0, tt.decimals1)
			if math.Abs(got-tt.want) > tt.want*1e-9+1e-9 {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
