package questdb


import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var qpool *pgxpool.Pool

type TradeLog struct {
	Timestamp     int64  `json:"timestamp"`
	Symbol        string `json:"symbol"`
	BuyerOrderID  uint64 `json:"buyer_order_id"`
	SellerOrderID uint64 `json:"seller_order_id"`
	Price         uint64 `json:"price"`
	Quantity      uint32 `json:"quantity"`
	IsBuyerMaker  bool   `json:"is_buyer_maker"`
}

type OrderBookSnapshot struct {
	Timestamp int64       `json:"timestamp"`
	Symbol    string      `json:"symbol"`
	EventID   uint64      `json:"event_id"`
	Bids      [][2]uint64 `json:"bids"`
	Asks      [][2]uint64 `json:"asks"`
}

func InitQuestDB(dsn string) error {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse questdb dsn: %w", err)
	}

	// Pool tuning: reads only, keep it modest
	cfg.MaxConns = 20
	cfg.MinConns = 5
	cfg.MaxConnLifetime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	qpool, err = pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("create questdb pool: %w", err)
	}

	if err := qpool.Ping(context.Background()); err != nil {
		return fmt.Errorf("ping questdb: %w", err)
	}

	return nil
}

func CloseQuestDB() {
	if qpool != nil {
		qpool.Close()
	}
}

// GetRecentTrades: newest -> oldest
func GetRecentTrades(ctx context.Context, symbol string, limit int) ([]TradeLog, error) {
	if limit <= 0 {
		limit = 1000
	}

	rows, err := qpool.Query(ctx, `
		SELECT timestamp, symbol, price, quantity, buyer_order_id, seller_order_id, is_buyer_maker
		FROM trade_logs
		WHERE symbol = $1
		ORDER BY timestamp DESC
		LIMIT $2
	`, symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("query trades: %w", err)
	}
	defer rows.Close()

	out := make([]TradeLog, 0, limit)
	for rows.Next() {
		var t TradeLog

		// Many QuestDB integer columns will scan as int64 cleanly.
		var price int64
		var qty int64
		var buyerOID int64
		var sellerOID int64

		if err := rows.Scan(
			&t.Timestamp,
			&t.Symbol,
			&price,
			&qty,
			&buyerOID,
			&sellerOID,
			&t.IsBuyerMaker,
		); err != nil {
			return nil, fmt.Errorf("scan trade: %w", err)
		}

		t.Price = uint64(price)
		t.Quantity = uint32(qty)
		t.BuyerOrderID = uint64(buyerOID)
		t.SellerOrderID = uint64(sellerOID)

		out = append(out, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows trades: %w", err)
	}
	return out, nil
}

// GetRecentOrderBookSnapshots: newest -> oldest
func GetRecentOrderBookSnapshots(ctx context.Context, symbol string, limit int) ([]OrderBookSnapshot, error) {
	if limit <= 0 {
		limit = 200
	}

	rows, err := qpool.Query(ctx, `
		SELECT timestamp, symbol, snapshot_id, bids, asks
		FROM orderbook_snapshots
		WHERE symbol = $1
		ORDER BY timestamp DESC
		LIMIT $2
	`, symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("query snapshots: %w", err)
	}
	defer rows.Close()

	out := make([]OrderBookSnapshot, 0, limit)
	for rows.Next() {
		var s OrderBookSnapshot
		var snapshotID int64
		var bidsJSON, asksJSON string

		if err := rows.Scan(
			&s.Timestamp,
			&s.Symbol,
			&snapshotID,
			&bidsJSON,
			&asksJSON,
		); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		s.EventID = uint64(snapshotID)

		if err := json.Unmarshal([]byte(bidsJSON), &s.Bids); err != nil {
			return nil, fmt.Errorf("unmarshal bids: %w", err)
		}
		if err := json.Unmarshal([]byte(asksJSON), &s.Asks); err != nil {
			return nil, fmt.Errorf("unmarshal asks: %w", err)
		}

		out = append(out, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows snapshots: %w", err)
	}
	return out, nil
}
