package store

import (
	"database/sql"
	"strconv"

	"charm-wallet-tui/helpers"

	"github.com/ethereum/go-ethereum/common"
)

// OscillatorPoolRef is the persisted pool-resolution cache for one basket
// token — resolved once via helpers.ResolveBasketToken, reused on later app
// restarts instead of re-running ResolvePairOnChain every time.
type OscillatorPoolRef struct {
	TokenAddr        common.Address
	Version          helpers.PoolVersion
	PoolKey          string // V2/V3 pool contract address hex, or V4 pool_id hex
	RefToken         common.Address
	TokenDecimals    uint8
	RefDecimals      uint8
	TokenIsToken0    bool
	V3Fee            uint32
	V4Hooks          common.Address
	V4Fee            uint32
	V4TickSpacing    int32
	ResolvedAtBlock  uint64
	LastScannedBlock uint64
}

// SaveOscillatorPoolRef persists (or replaces) the resolved pool for a basket token.
func (s *Store) SaveOscillatorPoolRef(ref OscillatorPoolRef) error {
	isToken0 := 0
	if ref.TokenIsToken0 {
		isToken0 = 1
	}
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO oscillator_pool_refs
			(token_addr, version, pool_key, ref_token, token_decimals, ref_decimals,
			 token_is_token0, v3_fee, v4_hooks, v4_fee, v4_tick_spacing, resolved_at_block,
			 last_scanned_block)
		VALUES (?,?,?,?,?,?, ?,?,?,?,?,?, ?)`,
		ref.TokenAddr.Hex(), int(ref.Version), ref.PoolKey, ref.RefToken.Hex(),
		ref.TokenDecimals, ref.RefDecimals, isToken0, ref.V3Fee,
		ref.V4Hooks.Hex(), ref.V4Fee, ref.V4TickSpacing, ref.ResolvedAtBlock,
		ref.LastScannedBlock,
	)
	return err
}

// UpdateOscillatorLastScanned advances the persisted scan checkpoint for
// token after a chunk covering up to block has been fetched and saved.
func (s *Store) UpdateOscillatorLastScanned(token common.Address, block uint64) error {
	_, err := s.db.Exec(`UPDATE oscillator_pool_refs SET last_scanned_block = ? WHERE token_addr = ?`,
		block, token.Hex())
	return err
}

func scanOscillatorPoolRef(row interface {
	Scan(dest ...any) error
}) (*OscillatorPoolRef, error) {
	var (
		tokenAddr, poolKey, refToken, v4Hooks string
		version, tokenDecimals, refDecimals   int
		isToken0, v3Fee, v4Fee                int
		v4TickSpacing                         int
		resolvedAtBlock, lastScannedBlock     uint64
	)
	if err := row.Scan(&tokenAddr, &version, &poolKey, &refToken, &tokenDecimals, &refDecimals,
		&isToken0, &v3Fee, &v4Hooks, &v4Fee, &v4TickSpacing, &resolvedAtBlock, &lastScannedBlock); err != nil {
		return nil, err
	}
	return &OscillatorPoolRef{
		TokenAddr:        common.HexToAddress(tokenAddr),
		Version:          helpers.PoolVersion(version),
		PoolKey:          poolKey,
		RefToken:         common.HexToAddress(refToken),
		TokenDecimals:    uint8(tokenDecimals),
		RefDecimals:      uint8(refDecimals),
		TokenIsToken0:    isToken0 != 0,
		V3Fee:            uint32(v3Fee),
		V4Hooks:          common.HexToAddress(v4Hooks),
		V4Fee:            uint32(v4Fee),
		V4TickSpacing:    int32(v4TickSpacing),
		ResolvedAtBlock:  resolvedAtBlock,
		LastScannedBlock: lastScannedBlock,
	}, nil
}

const oscillatorPoolRefColumns = `token_addr, version, pool_key, ref_token, token_decimals, ref_decimals,
			 token_is_token0, v3_fee, v4_hooks, v4_fee, v4_tick_spacing, resolved_at_block,
			 last_scanned_block`

// GetOscillatorPoolRef returns the cached pool ref for token, or nil, nil if not yet resolved.
func (s *Store) GetOscillatorPoolRef(token common.Address) (*OscillatorPoolRef, error) {
	row := s.db.QueryRow(`SELECT `+oscillatorPoolRefColumns+`
		FROM oscillator_pool_refs WHERE token_addr = ?`, token.Hex())
	ref, err := scanOscillatorPoolRef(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return ref, err
}

// ListOscillatorPoolRefs returns every cached basket pool resolution.
func (s *Store) ListOscillatorPoolRefs() ([]OscillatorPoolRef, error) {
	rows, err := s.db.Query(`SELECT ` + oscillatorPoolRefColumns + ` FROM oscillator_pool_refs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OscillatorPoolRef
	for rows.Next() {
		ref, err := scanOscillatorPoolRef(rows)
		if err != nil {
			continue
		}
		out = append(out, *ref)
	}
	return out, rows.Err()
}

// DeleteOscillatorPoolRef clears a cached resolution, forcing re-resolution
// on the next backscan — used by the page's manual "re-resolve" action.
func (s *Store) DeleteOscillatorPoolRef(token common.Address) error {
	_, err := s.db.Exec(`DELETE FROM oscillator_pool_refs WHERE token_addr = ?`, token.Hex())
	return err
}

// SaveOscillatorSwap persists one decoded basket-token price point. Silently
// ignores duplicates (UNIQUE on tx_hash, log_index).
func (s *Store) SaveOscillatorSwap(tokenAddr common.Address, block uint64, blockTime int64, txHash common.Hash, logIndex uint, price float64) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO oscillator_swaps
			(token_addr, block, block_time, tx_hash, log_index, price)
		VALUES (?,?,?,?,?,?)`,
		tokenAddr.Hex(), block, blockTime, txHash.Hex(), logIndex,
		strconv.FormatFloat(price, 'f', -1, 64),
	)
	return err
}

// DailyPrice is one UTC calendar day's closing price for a basket token.
type DailyPrice struct {
	Day   string // "YYYY-MM-DD", UTC
	Close float64
}

// OscillatorDailyCloses groups token's oscillator_swaps by UTC calendar day
// (via block_time, not insertion time) and returns one entry per day that
// has at least one swap, using the last swap of each day as that day's
// close, ordered oldest-first.
func (s *Store) OscillatorDailyCloses(token common.Address) ([]DailyPrice, error) {
	rows, err := s.db.Query(`
		SELECT strftime('%Y-%m-%d', block_time, 'unixepoch') AS day, price
		FROM oscillator_swaps
		WHERE token_addr = ?
		ORDER BY block_time ASC`, token.Hex())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DailyPrice
	for rows.Next() {
		var day, priceText string
		if err := rows.Scan(&day, &priceText); err != nil {
			continue
		}
		price, err := strconv.ParseFloat(priceText, 64)
		if err != nil {
			continue
		}
		if len(out) > 0 && out[len(out)-1].Day == day {
			out[len(out)-1].Close = price
			continue
		}
		out = append(out, DailyPrice{Day: day, Close: price})
	}
	return out, rows.Err()
}
