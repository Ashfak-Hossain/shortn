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

// startPostgres provisions an ephemeral Postgres container with the application's
// database schema pre-applied. It returns a fully connected database pool and
// automatically registers teardown hooks to destroy the container when the test ends.
func startPostgres(t *testing.T) *pgxpool.Pool {
	// We mark this as a test helper so that if a setup failure occurs, the test runner
	// attributes the failure to the exact line in the test that called this function,
	// rather than burying it inside the setup logic.
	t.Helper()
	ctx := context.Background()

	// We use Testcontainers to spin up a real Postgres database via Docker.
	// Testing against a real database (rather than mocking) guarantees that our SQL
	// syntax, constraints, and pgx driver behaviors exactly match production.
	schema := filepath.Join("..", "..", "migrations", "000001_create_links.up.sql")
	container, err := postgres.Run(ctx, "postgres:16",
		postgres.WithInitScripts(schema),
		postgres.WithDatabase("shortn"),
		postgres.WithUsername("dev"),
		postgres.WithPassword("dev"),

		// Postgres restarts itself once during its initialization process.
		// We explicitly wait for the "ready to accept connections" log to appear exactly
		// twice. If we only wait for it once, our tests will try to connect during the
		// restart phase and fail randomly (a classic flaky test trap).
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}

	// We register the container termination immediately. t.Cleanup ensures this runs
	// at the end of the test, even if a later setup step panics or fails, preventing
	// orphaned Docker containers from eating up system memory.
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

	// Ensure the database connections are gracefully closed before the container dies.
	t.Cleanup(pool.Close)

	return pool
}

// TestIntegration_Store executes the persistence layer integration test suite.
func TestIntegration_Store(t *testing.T) {
	ctx := context.Background()
	st := New(startPostgres(t))

	t.Run("create then get round-trips the link", func(t *testing.T) {
		link := &shortener.Link{Code: "abc1234", LongURL: "https://example.com"}
		if err := st.Create(ctx, link); err != nil {
			t.Fatalf("Create: %v", err)
		}

		// We assert that the database successfully auto-generated the ID and CreatedAt
		// timestamp, and properly populated them back into our struct via the SQL RETURNING clause.
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

		// We explicitly verify that a NULL database column (ExpiresAt) correctly
		// translates into a nil pointer in our Go struct.
		if got.ExpiresAt != nil {
			t.Errorf("ExpiresAt = %v, want nil (NULL column)", got.ExpiresAt)
		}
	})

	t.Run("duplicate code returns ErrCodeExists", func(t *testing.T) {
		first := &shortener.Link{Code: "dup0000", LongURL: "https://a.com"}
		if err := st.Create(ctx, first); err != nil {
			t.Fatalf("first Create: %v", err)
		}

		// We intentionally violate the unique constraint on the 'code' column to ensure
		// our store correctly traps the raw pgx error (code 23505) and successfully
		// translates it into our safe domain sentinel error.
		second := &shortener.Link{Code: "dup0000", LongURL: "https://b.com"}
		if err := st.Create(ctx, second); !errors.Is(err, shortener.ErrCodeExists) {
			t.Fatalf("duplicate Create error = %v, want ErrCodeExists", err)
		}
	})

	t.Run("missing code returns ErrNotFound", func(t *testing.T) {
		// We query a non-existent code to verify that pgx.ErrNoRows is successfully
		// trapped and translated into our domain's ErrNotFound.
		if _, err := st.GetByCode(ctx, "missing0"); !errors.Is(err, shortener.ErrNotFound) {
			t.Fatalf("GetByCode error = %v, want ErrNotFound", err)
		}
	})
}
