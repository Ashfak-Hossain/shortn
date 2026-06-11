// Package store is the Postgres-backed persistence layer — the only package
// that contains SQL. It translates database errors into the domain's sentinel
// errors so pgx types never leak upward.
package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
)

// Postgres implements shortener.LinkStore.
type Postgres struct {
	pool *pgxpool.Pool
}

var _ shortener.LinkStore = (*Postgres)(nil)

// New returns a store backed by the given pool.
func New(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

// Create inserts a link, filling its ID and CreatedAt from the database.
func (p *Postgres) Create(ctx context.Context, link *shortener.Link) error {
	const query = `INSERT INTO links (code, long_url) VALUES ($1, $2) RETURNING id, created_at`

	// Used QueryRow instead of Exec so we can get the ID and CreatedAt values back from the database.
	err := p.pool.QueryRow(ctx, query, link.Code, link.LongURL).
		Scan(&link.ID, &link.CreatedAt) // If the code already exists, we want to return ErrCodeExists instead of a pgx error.
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return shortener.ErrCodeExists
		}
		return err
	}
	return nil
}

// GetByCode returns the link for a code, or shortener.ErrNotFound.
func (p *Postgres) GetByCode(ctx context.Context, code string) (*shortener.Link, error) {
	const query = `SELECT id, code, long_url, created_at, expires_at, click_count FROM links WHERE code = $1`

	var link shortener.Link
	err := p.pool.QueryRow(ctx, query, code).Scan(
		&link.ID,
		&link.Code,
		&link.LongURL,
		&link.CreatedAt,
		&link.ExpiresAt,
		&link.ClickCount,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shortener.ErrNotFound
		}
		return nil, err
	}
	return &link, nil
}
