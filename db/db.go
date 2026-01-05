package db

import (
	"context"
	"database/sql"
	"errors"
	"exchange/shm"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

// Order status constants
const (
	OrderStatusPending  int16 = 0
	OrderStatusAccepted int16 = 1
	OrderStatusPartial  int16 = 2
	OrderStatusFilled   int16 = 3
	OrderStatusRejected int16 = 4
	OrderStatusCanceled int16 = 5
)

type User struct {
	ID        int64     `json:"id"`
	Email     *string   `json:"email,omitempty"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

func mapEventKindToStatus(eventKind uint32) (int16, string) {
	switch eventKind {
	case 0: // accepted
		return OrderStatusAccepted, ""
	case 1: // partial
		return OrderStatusPartial, ""
	case 2: // full fill
		return OrderStatusFilled, ""
	case 3: // rejected
		return OrderStatusRejected, "rejected" // or decode ErrorCode → string
	case 4: // canceled
		return OrderStatusCanceled, "canceled"
	default:
		return OrderStatusPending, "unknown_event"
	}
}

// InitDB creates connection pool + tables (called ONCE at startup)
func InitDB(dsn string) error {
	// Convert pgxpool DSN → sql.DB compatible
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}

	// Production pool settings
	config.MaxConns = 20
	config.MinConns = 5
	config.MaxConnLifetime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second

	// Create pool
	pool, err = pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return fmt.Errorf("create pool: %w", err)
	}

	// Test connection
	if err := pool.Ping(context.Background()); err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}

	log.Println("✅ pgxpool connected:", dsn)

	// Create schema (idempotent)
	// Replace the single Exec with these sequential calls
	ctx := context.Background()

	// 1. Create tables first (no inline indexes)
	// 1. Create tables first
	_, err = pool.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS users (
            id BIGSERIAL PRIMARY KEY,
            email VARCHAR(255),
            username VARCHAR(64),
            created_at TIMESTAMPTZ DEFAULT now()
        );

        CREATE TABLE IF NOT EXISTS oauth_accounts (
            id BIGSERIAL PRIMARY KEY,
            user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            provider TEXT NOT NULL,
            provider_user_id TEXT NOT NULL,
            created_at TIMESTAMPTZ DEFAULT now(),
            UNIQUE(provider, provider_user_id)
        );

        CREATE TABLE IF NOT EXISTS user_balances (
            user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
            balance NUMERIC(18,8) DEFAULT 0,
            updated_at TIMESTAMPTZ DEFAULT now()
        );

        CREATE TABLE IF NOT EXISTS user_holdings (
            id BIGSERIAL PRIMARY KEY,
            user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            symbol INTEGER NOT NULL,
            quantity NUMERIC(18,8) DEFAULT 0,
            updated_at TIMESTAMPTZ DEFAULT now(),
            UNIQUE(user_id, symbol)
        );

        DROP TABLE IF EXISTS user_orders;

        CREATE TABLE IF NOT EXISTS orders (
            id BIGSERIAL PRIMARY KEY,
            order_id BIGINT UNIQUE NOT NULL,
            user_id BIGINT NOT NULL,
            symbol INTEGER NOT NULL,
            side SMALLINT NOT NULL,
            order_type SMALLINT NOT NULL,
            status SMALLINT NOT NULL DEFAULT 0,
            price NUMERIC(18,8),
            quantity NUMERIC(18,8) NOT NULL,
            filled_qty NUMERIC(18,8) DEFAULT 0,
            reject_reason VARCHAR(128),
            timestamp BIGINT NOT NULL,
            created_at TIMESTAMPTZ DEFAULT now(),
            updated_at TIMESTAMPTZ DEFAULT now()
        );
    `)
	if err != nil {
		return fmt.Errorf("create tables: %w", err)
	}

	// 2. Create indexes WITHOUT CONCURRENTLY (fast for empty tables at startup)
	_, err = pool.Exec(ctx, `
        CREATE INDEX IF NOT EXISTS idx_orders_user_status ON orders(user_id, status);
        CREATE INDEX IF NOT EXISTS idx_orders_symbol_status ON orders(symbol, status);
        CREATE INDEX IF NOT EXISTS idx_orders_user_time ON orders(user_id, created_at DESC);
    `)
	if err != nil {
		return fmt.Errorf("create indexes: %w", err)
	}

	log.Println(" Database schema ready")
	return nil
}

// PRODUCTION FindOrCreateOAuthUser (transactional, handles races)
func FindOrCreateOAuthUser(ctx context.Context, provider, providerUserID, email, username string, shm_manager_ptr *shm.ShmManager) (*User, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var user User
	err = tx.QueryRow(ctx, `
        SELECT u.id, u.email, u.username, u.created_at
        FROM users u 
        JOIN oauth_accounts oa ON oa.user_id = u.id 
        WHERE oa.provider = $1 AND oa.provider_user_id = $2
        FOR UPDATE OF u
    `, provider, providerUserID).Scan(&user.ID, &user.Email, &user.Username, &user.CreatedAt)

	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		// Create new user atomically
		err = tx.QueryRow(ctx, `
            INSERT INTO users (email, username)
            VALUES ($1, $2)
            RETURNING id, email, username, created_at
        `, email, username).Scan(&user.ID, &user.Email, &user.Username, &user.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("create user: %w", err)
		}

		if shm_manager_ptr != nil && shm_manager_ptr.Query_queue != nil {
			var query shm.Query
			query.QueryId = 0
			query.QueryType = 2
			query.UserId = uint64(user.ID)
			shm_manager_ptr.Query_queue.Enqueue(query) // Safe - nil check
		}
		// Link OAuth account
		_, err = tx.Exec(ctx, `
            INSERT INTO oauth_accounts (user_id, provider, provider_user_id)
            VALUES ($1, $2, $3)
        `, user.ID, provider, providerUserID)
		if err != nil {
			return nil, fmt.Errorf("link oauth: %w", err)
		}

		// Auto-create balance
		_, err = tx.Exec(ctx, `
            INSERT INTO user_balances (user_id, balance) 
            VALUES ($1, 0) ON CONFLICT DO NOTHING
        `, user.ID)
		if err != nil {
			log.Printf("balance init failed (non-fatal): %v", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("find user: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &user, nil
}

// 1. Record incoming order (status=0, pending)
func RecordPendingOrder(ctx context.Context, order shm.Order) error {
	_, err := pool.Exec(ctx, `
        INSERT INTO orders (
            order_id, user_id, symbol, side, order_type, status, 
            price, quantity, timestamp
        ) VALUES ($1, $2, $3, $4, $5, 0, $6, $7, $8)
        ON CONFLICT (order_id) DO NOTHING
    `,
		order.OrderID,
		order.UserId,
		int32(order.Symbol),
		int16(order.Side),
		int16(order.Order_type),
		int64(order.Price),
		int32(order.Quantity),
		order.Timestamp,
	)
	return err
}

// 2. Record cancel request (status=3, cancel_requested)
func RecordCancelRequest(ctx context.Context, userID uint64, orderID uint64, symbol uint32) error {
	_, err := pool.Exec(ctx, `
        UPDATE orders 
        SET status = 3, reject_reason = 'cancel_requested', updated_at = now()
        WHERE order_id = $1 AND user_id = $2 AND symbol = $3 AND status = 0
    `, orderID, userID, int32(symbol))
	return err
}

type Order struct {
	ID           int64     `json:"id"`
	OrderID      uint64    `json:"order_id"`
	Symbol       int32     `json:"symbol"`
	Side         int16     `json:"side"`
	OrderType    int16     `json:"order_type"`
	Status       int16     `json:"status"`
	Price        float64   `json:"price"`
	Quantity     float64   `json:"quantity"`
	FilledQty    float64   `json:"filled_qty"`
	RejectReason *string   `json:"reject_reason,omitempty"`
	Timestamp    uint64    `json:"timestamp"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func GetUserOrders(ctx context.Context, userID uint64, limit int32, offset int32) ([]Order, error) {
	rows, err := pool.Query(ctx, `
        SELECT id, order_id, symbol, side, order_type, status, 
               price, quantity, filled_qty, reject_reason, 
               timestamp, created_at, updated_at
        FROM orders 
        WHERE user_id = $1 
        ORDER BY created_at DESC 
        LIMIT $2 OFFSET $3
    `, userID, limit, offset)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		var order Order
		err := rows.Scan(
			&order.ID, &order.OrderID, &order.Symbol, &order.Side,
			&order.OrderType, &order.Status, &order.Price, &order.Quantity,
			&order.FilledQty, &order.RejectReason, &order.Timestamp,
			&order.CreatedAt, &order.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}

	return orders, rows.Err()
}

// Update order row based on engine event.
func ApplyOrderEvent(ctx context.Context, ev shm.OrderEvent) error {
	if pool == nil {
		return fmt.Errorf("db pool not initialized")
	}

	status, reason := mapEventKindToStatus(ev.EventKind)

	filledQty := ev.FilledQty

	_, err := pool.Exec(ctx, `
		UPDATE orders
		SET status = $1,
		    filled_qty = $2,
		    reject_reason = CASE WHEN $3 = '' THEN reject_reason ELSE $3 END,
		    updated_at = now()
		WHERE order_id = $4
		  AND user_id = $5
		  AND symbol  = $6
	`, status, float64(filledQty), reason,
		ev.OrderId, ev.UserId, int32(ev.Symbol),
	)
	return err
}

// Close pool on shutdown
func Close() {
	if pool != nil {
		pool.Close()
	}
}
