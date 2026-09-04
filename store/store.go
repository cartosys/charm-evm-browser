package store

import (
	"database/sql"
	"math/big"

	"charm-wallet-tui/indexer"

	"github.com/ethereum/go-ethereum/common"
	_ "modernc.org/sqlite"
)

// baseSchema creates tables that exist in every schema version.
const baseSchema = `
CREATE TABLE IF NOT EXISTS erc20_tokens (
	address  TEXT    NOT NULL PRIMARY KEY,
	name     TEXT    NOT NULL DEFAULT '',
	symbol   TEXT    NOT NULL DEFAULT '',
	decimals INTEGER NOT NULL DEFAULT 0,
	seen_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_erc20_symbol ON erc20_tokens(symbol);

CREATE TABLE IF NOT EXISTS indexed_events (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	block      INTEGER NOT NULL,
	tx_hash    TEXT    NOT NULL,
	log_index  INTEGER NOT NULL,
	from_addr  TEXT    NOT NULL,
	to_addr    TEXT    NOT NULL,
	value_hex  TEXT    NOT NULL,
	token_addr TEXT    NOT NULL,
	symbol     TEXT    NOT NULL,
	decimals   INTEGER NOT NULL,
	seen_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(tx_hash, log_index)
);
CREATE INDEX IF NOT EXISTS idx_from  ON indexed_events(from_addr);
CREATE INDEX IF NOT EXISTS idx_to    ON indexed_events(to_addr);
CREATE INDEX IF NOT EXISTS idx_block ON indexed_events(block);
`

// v1Migration replaces the old catch-all V4 tables with a normalised schema:
//
//   v4_pools              — one row per pool, keyed by pool_id (Initialize events)
//   v4_swaps              — Swap events, FK → v4_pools(pool_id)
//   v4_modify_liquidity   — ModifyLiquidity events, FK → v4_pools(pool_id)
//   v4_donates            — Donate events, FK → v4_pools(pool_id)
//   v4_transfers          — ERC-6909 Transfer events, indexed by caller/from/to/token_id
//
// FKs are declared for schema clarity and JOIN use; SQLite does not enforce
// them unless PRAGMA foreign_keys = ON is set per connection.
const v1Migration = `
DROP TABLE IF EXISTS v4_swap_events;
DROP TABLE IF EXISTS v4_pool_events;

CREATE TABLE IF NOT EXISTS v4_pools (
	pool_id      TEXT    NOT NULL PRIMARY KEY,
	block        INTEGER NOT NULL,
	tx_hash      TEXT    NOT NULL,
	log_index    INTEGER NOT NULL,
	currency0    TEXT    NOT NULL,
	currency1    TEXT    NOT NULL,
	fee          INTEGER NOT NULL,
	tick_spacing INTEGER NOT NULL,
	hooks        TEXT    NOT NULL,
	sqrt_price   TEXT    NOT NULL,
	init_tick    INTEGER NOT NULL,
	seen_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_v4pools_c0    ON v4_pools(currency0);
CREATE INDEX IF NOT EXISTS idx_v4pools_c1    ON v4_pools(currency1);
CREATE INDEX IF NOT EXISTS idx_v4pools_block ON v4_pools(block);

CREATE TABLE IF NOT EXISTS v4_swaps (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	block      INTEGER NOT NULL,
	tx_hash    TEXT    NOT NULL,
	log_index  INTEGER NOT NULL,
	pool_id    TEXT    NOT NULL REFERENCES v4_pools(pool_id),
	sender     TEXT    NOT NULL,
	amount0    TEXT    NOT NULL,
	amount1    TEXT    NOT NULL,
	sqrt_price TEXT    NOT NULL,
	liquidity  TEXT    NOT NULL,
	tick       INTEGER NOT NULL,
	fee        INTEGER NOT NULL,
	seen_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(tx_hash, log_index)
);
CREATE INDEX IF NOT EXISTS idx_v4swaps_pool  ON v4_swaps(pool_id);
CREATE INDEX IF NOT EXISTS idx_v4swaps_send  ON v4_swaps(sender);
CREATE INDEX IF NOT EXISTS idx_v4swaps_block ON v4_swaps(block);

CREATE TABLE IF NOT EXISTS v4_modify_liquidity (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	block      INTEGER NOT NULL,
	tx_hash    TEXT    NOT NULL,
	log_index  INTEGER NOT NULL,
	pool_id    TEXT    NOT NULL REFERENCES v4_pools(pool_id),
	sender     TEXT    NOT NULL,
	tick_lower INTEGER NOT NULL,
	tick_upper INTEGER NOT NULL,
	liq_delta  TEXT    NOT NULL,
	salt       TEXT    NOT NULL,
	seen_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(tx_hash, log_index)
);
CREATE INDEX IF NOT EXISTS idx_v4ml_pool  ON v4_modify_liquidity(pool_id);
CREATE INDEX IF NOT EXISTS idx_v4ml_send  ON v4_modify_liquidity(sender);
CREATE INDEX IF NOT EXISTS idx_v4ml_block ON v4_modify_liquidity(block);

CREATE TABLE IF NOT EXISTS v4_donates (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	block     INTEGER NOT NULL,
	tx_hash   TEXT    NOT NULL,
	log_index INTEGER NOT NULL,
	pool_id   TEXT    NOT NULL REFERENCES v4_pools(pool_id),
	sender    TEXT    NOT NULL,
	amount0   TEXT    NOT NULL,
	amount1   TEXT    NOT NULL,
	seen_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(tx_hash, log_index)
);
CREATE INDEX IF NOT EXISTS idx_v4don_pool  ON v4_donates(pool_id);
CREATE INDEX IF NOT EXISTS idx_v4don_send  ON v4_donates(sender);
CREATE INDEX IF NOT EXISTS idx_v4don_block ON v4_donates(block);

CREATE TABLE IF NOT EXISTS v4_transfers (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	block     INTEGER NOT NULL,
	tx_hash   TEXT    NOT NULL,
	log_index INTEGER NOT NULL,
	caller    TEXT    NOT NULL,
	from_addr TEXT    NOT NULL,
	to_addr   TEXT    NOT NULL,
	token_id  TEXT    NOT NULL,
	amount    TEXT    NOT NULL,
	seen_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(tx_hash, log_index)
);
CREATE INDEX IF NOT EXISTS idx_v4xfer_from     ON v4_transfers(from_addr);
CREATE INDEX IF NOT EXISTS idx_v4xfer_to       ON v4_transfers(to_addr);
CREATE INDEX IF NOT EXISTS idx_v4xfer_caller   ON v4_transfers(caller);
CREATE INDEX IF NOT EXISTS idx_v4xfer_token_id ON v4_transfers(token_id);
CREATE INDEX IF NOT EXISTS idx_v4xfer_block    ON v4_transfers(block);
`

// v2Migration adds the McClellan Oscillator's swap-history tables. Additive
// only — no changes to the v1Migration tables above. One unified
// oscillator_swaps table (not per-version, unlike v4_swaps/v4_donates/etc.)
// because every version reduces to the same derived fact — a decimal price
// at a point in time — so the daily-close query is identical regardless of
// which Uniswap version produced the row.
const v2Migration = `
CREATE TABLE IF NOT EXISTS oscillator_pool_refs (
	token_addr        TEXT    NOT NULL PRIMARY KEY,
	version           INTEGER NOT NULL,
	pool_key          TEXT    NOT NULL,
	ref_token         TEXT    NOT NULL,
	token_decimals    INTEGER NOT NULL,
	ref_decimals      INTEGER NOT NULL,
	token_is_token0   INTEGER NOT NULL,
	v3_fee            INTEGER NOT NULL DEFAULT 0,
	v4_hooks          TEXT    NOT NULL DEFAULT '',
	v4_fee            INTEGER NOT NULL DEFAULT 0,
	v4_tick_spacing   INTEGER NOT NULL DEFAULT 0,
	resolved_at_block INTEGER NOT NULL,
	resolved_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS oscillator_swaps (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	token_addr TEXT    NOT NULL REFERENCES oscillator_pool_refs(token_addr),
	block      INTEGER NOT NULL,
	block_time INTEGER NOT NULL,
	tx_hash    TEXT    NOT NULL,
	log_index  INTEGER NOT NULL,
	price      TEXT    NOT NULL,
	seen_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(tx_hash, log_index)
);
CREATE INDEX IF NOT EXISTS idx_oscswaps_token_time ON oscillator_swaps(token_addr, block_time);
CREATE INDEX IF NOT EXISTS idx_oscswaps_block       ON oscillator_swaps(block);
`

func migrateToV2(db *sql.DB) error {
	var ver int
	if err := db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		return err
	}
	if ver >= 2 {
		return nil
	}
	if _, err := db.Exec(v2Migration); err != nil {
		return err
	}
	_, err := db.Exec("PRAGMA user_version = 2")
	return err
}

// Store wraps a SQLite database for persisting indexed events.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and runs migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer
	if _, err := db.Exec(baseSchema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateToV1(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateToV2(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrateToV1(db *sql.DB) error {
	var ver int
	if err := db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		return err
	}
	if ver >= 1 {
		return nil
	}
	if _, err := db.Exec(v1Migration); err != nil {
		return err
	}
	_, err := db.Exec("PRAGMA user_version = 1")
	return err
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ---- helpers ----------------------------------------------------------------

func bigText(x *big.Int) string {
	if x == nil {
		return "0"
	}
	return x.String()
}

func toInt64(x *big.Int) int64 {
	if x == nil {
		return 0
	}
	return x.Int64()
}

// ---- ERC-20 events ----------------------------------------------------------

// SaveEvent inserts an indexed ERC-20 Transfer event. Silently ignores duplicates.
func (s *Store) SaveEvent(ev indexer.IndexedEvent) error {
	valueHex := "0x0"
	if ev.Value != nil {
		valueHex = "0x" + ev.Value.Text(16)
	}
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO indexed_events
			(block, tx_hash, log_index, from_addr, to_addr, value_hex, token_addr, symbol, decimals)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.Block, ev.TxHash.Hex(), ev.LogIndex,
		ev.From.Hex(), ev.To.Hex(), valueHex,
		ev.Token.Hex(), ev.Symbol, ev.Decimals,
	)
	return err
}

// ---- Uniswap V4 events ------------------------------------------------------

// SaveV4PoolEvent routes the event to the appropriate typed table.
// Silently ignores duplicates (UNIQUE on tx_hash, log_index).
func (s *Store) SaveV4PoolEvent(ev indexer.V4PoolEvent) error {
	switch ev.Kind {
	case indexer.V4KindInitialize:
		return s.saveInitialize(ev)
	case indexer.V4KindSwap:
		return s.saveSwap(ev)
	case indexer.V4KindModifyLiquidity:
		return s.saveModifyLiquidity(ev)
	case indexer.V4KindDonate:
		return s.saveDonate(ev)
	case indexer.V4KindTransfer:
		return s.saveTransfer(ev)
	default:
		return nil
	}
}

func (s *Store) saveInitialize(ev indexer.V4PoolEvent) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO v4_pools
			(pool_id, block, tx_hash, log_index,
			 currency0, currency1, fee, tick_spacing, hooks, sqrt_price, init_tick)
		VALUES (?,?,?,?, ?,?,?,?,?,?,?)`,
		ev.PoolID.Hex(), ev.Block, ev.TxHash.Hex(), ev.LogIndex,
		ev.Currency0.Hex(), ev.Currency1.Hex(),
		toInt64(ev.Fee), toInt64(ev.TickSpacing), ev.Hooks.Hex(),
		bigText(ev.SqrtPriceX96), toInt64(ev.Tick),
	)
	return err
}

func (s *Store) saveSwap(ev indexer.V4PoolEvent) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO v4_swaps
			(block, tx_hash, log_index, pool_id, sender,
			 amount0, amount1, sqrt_price, liquidity, tick, fee)
		VALUES (?,?,?,?,?, ?,?,?,?,?,?)`,
		ev.Block, ev.TxHash.Hex(), ev.LogIndex,
		ev.PoolID.Hex(), ev.Sender.Hex(),
		bigText(ev.Amount0), bigText(ev.Amount1),
		bigText(ev.SqrtPriceX96), bigText(ev.Liquidity),
		toInt64(ev.Tick), toInt64(ev.Fee),
	)
	return err
}

func (s *Store) saveModifyLiquidity(ev indexer.V4PoolEvent) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO v4_modify_liquidity
			(block, tx_hash, log_index, pool_id, sender,
			 tick_lower, tick_upper, liq_delta, salt)
		VALUES (?,?,?,?,?, ?,?,?,?)`,
		ev.Block, ev.TxHash.Hex(), ev.LogIndex,
		ev.PoolID.Hex(), ev.Sender.Hex(),
		toInt64(ev.TickLower), toInt64(ev.TickUpper),
		bigText(ev.LiquidityDelta), ev.Salt.Hex(),
	)
	return err
}

func (s *Store) saveDonate(ev indexer.V4PoolEvent) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO v4_donates
			(block, tx_hash, log_index, pool_id, sender, amount0, amount1)
		VALUES (?,?,?,?,?,?,?)`,
		ev.Block, ev.TxHash.Hex(), ev.LogIndex,
		ev.PoolID.Hex(), ev.Sender.Hex(),
		bigText(ev.Amount0), bigText(ev.Amount1),
	)
	return err
}

func (s *Store) saveTransfer(ev indexer.V4PoolEvent) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO v4_transfers
			(block, tx_hash, log_index, caller, from_addr, to_addr, token_id, amount)
		VALUES (?,?,?,?,?,?,?,?)`,
		ev.Block, ev.TxHash.Hex(), ev.LogIndex,
		ev.Caller.Hex(), ev.From.Hex(), ev.To.Hex(),
		bigText(ev.TokenID), bigText(ev.Amount0),
	)
	return err
}

// ---- Queries ----------------------------------------------------------------

// RecentEvents returns up to limit ERC-20 events ordered newest block first.
func (s *Store) RecentEvents(limit int) ([]indexer.IndexedEvent, error) {
	rows, err := s.db.Query(`
		SELECT block, tx_hash, log_index, from_addr, to_addr, value_hex, token_addr, symbol, decimals
		FROM indexed_events
		ORDER BY block DESC, id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []indexer.IndexedEvent
	for rows.Next() {
		var (
			block    uint64
			txHash   string
			logIdx   uint
			from     string
			to       string
			valueHex string
			token    string
			symbol   string
			decimals uint8
		)
		if err := rows.Scan(&block, &txHash, &logIdx, &from, &to, &valueHex, &token, &symbol, &decimals); err != nil {
			continue
		}
		value := new(big.Int)
		value.SetString(valueHex, 0)
		events = append(events, indexer.IndexedEvent{
			Block:    block,
			TxHash:   common.HexToHash(txHash),
			LogIndex: logIdx,
			From:     common.HexToAddress(from),
			To:       common.HexToAddress(to),
			Value:    value,
			Token:    common.HexToAddress(token),
			Symbol:   symbol,
			Decimals: decimals,
		})
	}
	return events, rows.Err()
}

// Count returns the total number of stored ERC-20 events.
func (s *Store) Count() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM indexed_events`).Scan(&n)
	return n, err
}

// OldestBlock returns the lowest block number in indexed_events, or 0 if empty.
func (s *Store) OldestBlock() (uint64, error) {
	var b sql.NullInt64
	err := s.db.QueryRow(`SELECT MIN(block) FROM indexed_events`).Scan(&b)
	if err != nil || !b.Valid {
		return 0, err
	}
	return uint64(b.Int64), nil
}

// LatestBlock returns the highest block number in indexed_events, or 0 if empty.
func (s *Store) LatestBlock() (uint64, error) {
	var b sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(block) FROM indexed_events`).Scan(&b)
	if err != nil || !b.Valid {
		return 0, err
	}
	return uint64(b.Int64), nil
}

// PoolRow holds the aggregated result of the V4 pools stats query.
type PoolRow struct {
	PoolID      string
	Block       uint64
	TxHash      string
	Token0Sym   string
	Token0Name  string
	Currency0   string
	SwapVolume0 float64
	Token1Sym   string
	Token1Name  string
	Currency1   string
	SwapVolume1 float64
	Fee         int64
	Decimals0   int64
	Decimals1   int64
	Swaps       int64
	LiqEvents   int64
	LiqVolume   float64
	SeenAt      string
	Hooks       string
}

// V4PoolStats returns all indexed pools with aggregated swap and liquidity metrics,
// ordered by swap count descending.
func (s *Store) V4PoolStats() ([]PoolRow, error) {
	rows, err := s.db.Query(`
		SELECT
			p.pool_id,
			p.block,
			p.tx_hash,
			COALESCE(t0.symbol, '') AS token0, COALESCE(t0.name, '') AS name0,
			p.currency0 AS name0_address,
			COALESCE(SUM(ABS(s.amount0)), 0) AS swap_volume0,
			COALESCE(t1.symbol, '') AS token1, COALESCE(t1.name, '') AS name1,
			p.currency1 AS name1_address,
			COALESCE(SUM(ABS(s.amount1)), 0) AS swap_volume1,
			p.fee,
			COALESCE(t0.decimals, 18) AS dec0,
			COALESCE(t1.decimals, 18) AS dec1,
			COUNT(DISTINCT s.id)  AS swaps,
			COUNT(DISTINCT ml.id) AS liq_events,
			COALESCE(SUM(ABS(ml.liq_delta)), 0) AS liq_volume,
			p.seen_at,
			p.hooks
		FROM v4_pools p
		LEFT JOIN erc20_tokens        t0 ON t0.address = p.currency0
		LEFT JOIN erc20_tokens        t1 ON t1.address = p.currency1
		LEFT JOIN v4_swaps            s  ON s.pool_id  = p.pool_id
		LEFT JOIN v4_modify_liquidity ml ON ml.pool_id = p.pool_id
		GROUP BY p.pool_id
		ORDER BY p.seen_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PoolRow
	for rows.Next() {
		var r PoolRow
		if err := rows.Scan(
			&r.PoolID,
			&r.Block, &r.TxHash,
			&r.Token0Sym, &r.Token0Name, &r.Currency0, &r.SwapVolume0,
			&r.Token1Sym, &r.Token1Name, &r.Currency1, &r.SwapVolume1,
			&r.Fee, &r.Decimals0, &r.Decimals1, &r.Swaps, &r.LiqEvents, &r.LiqVolume,
			&r.SeenAt, &r.Hooks,
		); err != nil {
			continue
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
