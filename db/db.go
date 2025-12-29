package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

type User struct {
	ID        int64     `json:"id"`
	Email     *string   `json:"email,omitempty"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
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
	ctx := context.Background()
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

        CREATE TABLE IF NOT EXISTS user_orders (
            user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            order_id BIGINT NOT NULL,
            created_at TIMESTAMPTZ DEFAULT now(),
            PRIMARY KEY(user_id, order_id)
        );

        CREATE TABLE IF NOT EXISTS orders (
            id BIGSERIAL PRIMARY KEY,
            order_id BIGINT UNIQUE NOT NULL,
            symbol INTEGER NOT NULL,
            side SMALLINT NOT NULL,
            order_type SMALLINT NOT NULL,
            price NUMERIC(18,8),
            quantity NUMERIC(18,8) NOT NULL,
            filled_qty NUMERIC(18,8) DEFAULT 0,
            status SMALLINT NOT NULL,
            reject_reason VARCHAR(128),
            created_at TIMESTAMPTZ DEFAULT now(),
            updated_at TIMESTAMPTZ DEFAULT now()
        );

        CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_orders_user_status 
            ON orders(user_id, status) WHERE status = 0;
        
        CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_user_orders_user 
            ON user_orders(user_id);
    `)
	if err != nil {
		return fmt.Errorf("schema migration: %w", err)
	}

	log.Println("✅ Database schema ready")
	return nil
}

// PRODUCTION FindOrCreateOAuthUser (transactional, handles races)
func FindOrCreateOAuthUser(ctx context.Context, provider, providerUserID, email, username string) (*User, error) {
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

	if err == sql.ErrNoRows {
		// Create new user atomically
		err = tx.QueryRow(ctx, `
            INSERT INTO users (email, username)
            VALUES ($1, $2)
            RETURNING id, email, username, created_at
        `, email, username).Scan(&user.ID, &user.Email, &user.Username, &user.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("create user: %w", err)
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

// Close pool on shutdown
func Close() {
	if pool != nil {
		pool.Close()
	}
}
