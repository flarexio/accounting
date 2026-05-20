// Package postgres provides Postgres-backed repository adapters via
// sqlc-generated queries on top of pgx/v5.
//
//	migrations/  -- golang-migrate up/down SQL applied out of band
//	sqlc/        -- queries.sql, the sqlc input
//	pgstore/     -- sqlc-generated Go (DO NOT EDIT by hand)
//
// Regenerate pgstore with: cd persistence/postgres && sqlc generate
//
// One file per domain (e.g. accounting.go).
package postgres

import (
	"context"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"
)

// connectPool opens a pgxpool.Pool from dsn, pings it, and returns the pool
// alongside an io.Closer the caller defers to release it.
func connectPool(ctx context.Context, dsn string) (*pgxpool.Pool, io.Closer, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return pool, poolCloser{pool: pool}, nil
}

type poolCloser struct {
	pool *pgxpool.Pool
}

func (c poolCloser) Close() error {
	c.pool.Close()
	return nil
}
