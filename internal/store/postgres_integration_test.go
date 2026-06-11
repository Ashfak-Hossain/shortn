//go:build integration

package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
)

// startPostgres boots a throwaway Postgres with the links schema already applied
// and returns a connected pool. Container and pool are torn down when the test ends.
func startPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	schema := filepath.Join("..", "..", "migrations", "000001_create_links.up.sql")
	container, err := postgres.Run(ctx, "postgres:16",
		postgres.WithInitScripts(schema),
		postgres.WithDatabase("shortn"),
		postgres.WithUsername("dev"),
		postgres.WithPassword("dev"),
		// Postgres restarts once during init, so wait for the ready log twice.
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			t.Logf("terminate postgres: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

func TestIntegration_Store(t *testing.T) {
	ctx := context.Background()
	st := New(startPostgres(t))

	t.Run("create then get round-trips the link", func(t *testing.T) {
		link := &shortener.Link{Code: "abc1234", LongURL: "https://example.com"}
		if err := st.Create(ctx, link); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if link.ID == 0 || link.CreatedAt.IsZero() {
			t.Fatalf("Create did not fill DB-generated fields: id=%d createdAt=%v", link.ID, link.CreatedAt)
		}

		got, err := st.GetByCode(ctx, "abc1234")
		if err != nil {
			t.Fatalf("GetByCode: %v", err)
		}
		if got.LongURL != "https://example.com" {
			t.Errorf("LongURL = %q, want %q", got.LongURL, "https://example.com")
		}
		if got.ExpiresAt != nil {
			t.Errorf("ExpiresAt = %v, want nil (NULL column)", got.ExpiresAt)
		}
	})

	t.Run("duplicate code returns ErrCodeExists", func(t *testing.T) {
		first := &shortener.Link{Code: "dup0000", LongURL: "https://a.com"}
		if err := st.Create(ctx, first); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		second := &shortener.Link{Code: "dup0000", LongURL: "https://b.com"}
		if err := st.Create(ctx, second); !errors.Is(err, shortener.ErrCodeExists) {
			t.Fatalf("duplicate Create error = %v, want ErrCodeExists", err)
		}
	})

	t.Run("missing code returns ErrNotFound", func(t *testing.T) {
		if _, err := st.GetByCode(ctx, "missing0"); !errors.Is(err, shortener.ErrNotFound) {
			t.Fatalf("GetByCode error = %v, want ErrNotFound", err)
		}
	})
}
