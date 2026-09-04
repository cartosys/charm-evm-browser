package helpers

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// StablecoinFallbackChain returns the ordered reference-token candidates
// (USDC, USDT, DAI, WETH) to price token against, skipping whichever equals
// token itself (e.g. resolving USDC prices it against USDT next).
func StablecoinFallbackChain(addrs UniswapNetworkAddresses, token common.Address) []common.Address {
	candidates := []common.Address{addrs.USDC, addrs.USDT, addrs.DAI, addrs.WETH}
	out := make([]common.Address, 0, len(candidates))
	for _, c := range candidates {
		if c == (common.Address{}) || c == token {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ResolveBasketToken finds a priced pool for token by trying each ref token
// in StablecoinFallbackChain in order via the existing ResolvePairOnChain
// (which already requires real liquidity and prefers V3 > V2 > V4), stopping
// at the first one with a real pool.
func ResolveBasketToken(ctx context.Context, client *ethclient.Client, addrs UniswapNetworkAddresses, token common.Address) (ResolvedPool, common.Address, error) {
	for _, ref := range StablecoinFallbackChain(addrs, token) {
		pool, err := ResolvePairOnChain(ctx, client, addrs, token, ref)
		if err == nil {
			return pool, ref, nil
		}
	}
	return ResolvedPool{}, common.Address{}, fmt.Errorf("no pool found for %s against any stablecoin fallback", token.Hex())
}

// PoolToken0Token1 queries token0()/token1() on a V2 pair or V3 pool
// contract — both use the identical ABI — generalizing the inline calls
// GetUniswapV2Pair already makes for reuse against V3 pool addresses too.
func PoolToken0Token1(ctx context.Context, client *ethclient.Client, poolAddr common.Address) (token0, token1 common.Address, err error) {
	pair, err := GetUniswapV2Pair(client, poolAddr)
	if err != nil {
		return common.Address{}, common.Address{}, err
	}
	return pair.Token0, pair.Token1, nil
}
