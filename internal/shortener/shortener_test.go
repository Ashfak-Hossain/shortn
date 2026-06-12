package shortener

import (
	"context"
	"errors"
	"testing"
)

// fakeStore is an in-memory implementation of the LinkStore interface.
// We use a "Fake" here instead of a real database or a mocking framework to provide
// fast, completely isolated unit tests for the domain logic without any I/O overhead.
type fakeStore struct {
	byCode map[string]*Link
	nextID int64
}

// newFakeStore initializes a fakeStore with an empty, ready-to-use map.
func newFakeStore() *fakeStore {
	return &fakeStore{byCode: make(map[string]*Link)}
}

// Create mimics the database insert behavior, including enforcing unique constraints
// on the 'Code' field and auto-incrementing the ID.
func (f *fakeStore) Create(_ context.Context, link *Link) error {
	if _, taken := f.byCode[link.Code]; taken {
		return ErrCodeExists
	}
	f.nextID++
	link.ID = f.nextID
	f.byCode[link.Code] = link
	return nil
}

// GetByCode mimics a database select, returning ErrNotFound if the key is missing.
func (f *fakeStore) GetByCode(_ context.Context, code string) (*Link, error) {
	link, ok := f.byCode[code]
	if !ok {
		return nil, ErrNotFound
	}
	return link, nil
}

// stubGen is a deterministic, mock implementation of the IDGenerator interface.
// By yielding scripted codes in a specific order, we can reliably simulate
// non-deterministic events (like random code collisions) in our test suite.
type stubGen struct {
	codes []string
	calls int
}

// Generate returns the next pre-programmed code. If it reaches the end of the
// slice, it loops back to the beginning to prevent out-of-bounds panics.
func (g *stubGen) Generate() (string, error) {
	code := g.codes[g.calls%len(g.codes)]
	g.calls++
	return code, nil
}

// TestService_Create_Success verifies the happy path: a valid URL is processed,
// assigned a unique code, and successfully persisted.
func TestService_Create_Success(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, &stubGen{codes: []string{"abc1234"}})

	link, err := svc.Create(context.Background(), "https://example.com/path")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// We assert that the domain correctly wired the output of the generator
	// into the persisted Link entity.
	if link.Code != "abc1234" {
		t.Errorf("Code = %q, want %q", link.Code, "abc1234")
	}
	if link.LongURL != "https://example.com/path" {
		t.Errorf("LongURL = %q, want %q", link.LongURL, "https://example.com/path")
	}

	// We ensure the fakeStore correctly mutated the pointer to set the auto-generated ID.
	if link.ID == 0 {
		t.Error("ID was not set by the store")
	}
}

// TestService_Create_InvalidURL ensures the domain correctly rejects malformed
// or malicious input before any database interaction occurs.
func TestService_Create_InvalidURL(t *testing.T) {
	// We use a table-driven test to cover various security and validation boundaries,
	// specifically checking for XSS vectors (javascript:) and missing protocols.
	cases := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"no scheme", "example.com"},
		{"ftp scheme", "ftp://example.com"},
		{"javascript scheme", "javascript:alert(1)"},
		{"no host", "http://"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(newFakeStore(), &stubGen{codes: []string{"abc1234"}})

			_, err := svc.Create(context.Background(), tc.url)
			if !errors.Is(err, ErrInvalidURL) {
				t.Fatalf("Create(%q) error = %v, want ErrInvalidURL", tc.url, err)
			}
		})
	}
}

// TestService_Create_RetriesOnCollision proves that the domain service can recover
// seamlessly if the random generator happens to produce a code that already exists.
func TestService_Create_RetriesOnCollision(t *testing.T) {
	store := newFakeStore()

	// We pre-seed the store with "taken00" to guarantee a collision on the first attempt.
	store.byCode["taken00"] = &Link{Code: "taken00"}

	// The stub generator will yield "taken00" (collision), then "free123" (success).
	gen := &stubGen{codes: []string{"taken00", "free123"}}
	svc := NewService(store, gen)

	link, err := svc.Create(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// We assert that the service successfully skipped the colliding code and used the next one.
	if link.Code != "free123" {
		t.Errorf("Code = %q, want %q (should skip past the collision)", link.Code, "free123")
	}

	// We assert the generator was called exactly twice (once failing, once succeeding).
	if gen.calls != 2 {
		t.Errorf("generator called %d times, want 2", gen.calls)
	}
}

// TestService_Create_ExhaustsRetries verifies the boundary condition of the retry loop
// to prevent infinite loops during catastrophic collision scenarios.
func TestService_Create_ExhaustsRetries(t *testing.T) {
	store := newFakeStore()
	store.byCode["dup0000"] = &Link{Code: "dup0000"}

	// The stub generator will strictly yield the colliding code over and over.
	gen := &stubGen{codes: []string{"dup0000"}}
	svc := NewService(store, gen)

	_, err := svc.Create(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("expected an error after exhausting retries, got nil")
	}

	// We assert that the exact error returned is NOT the database collision error,
	// but rather the domain's aggregate failure error indicating retry exhaustion.
	if errors.Is(err, ErrCodeExists) {
		t.Errorf("exhaustion error should not be ErrCodeExists, got %v", err)
	}

	if gen.calls != maxCreateRetries {
		t.Errorf("generator called %d times, want %d", gen.calls, maxCreateRetries)
	}
}

// TestService_Resolve_Found verifies the happy path for retrieving an existing link.
func TestService_Resolve_Found(t *testing.T) {
	store := newFakeStore()
	store.byCode["abc1234"] = &Link{Code: "abc1234", LongURL: "https://example.com"}
	svc := NewService(store, &stubGen{codes: []string{"x"}})

	link, err := svc.Resolve(context.Background(), "abc1234")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if link.LongURL != "https://example.com" {
		t.Errorf("LongURL = %q, want %q", link.LongURL, "https://example.com")
	}
}

// TestService_Resolve_NotFound ensures querying missing codes properly returns
// the domain sentinel error.
func TestService_Resolve_NotFound(t *testing.T) {
	svc := NewService(newFakeStore(), &stubGen{codes: []string{"x"}})

	_, err := svc.Resolve(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrNotFound", err)
	}
}

// TestNormalizeURL verifies the pure logic of the URL parser and sanitizer.
func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lowercases scheme and host", "HTTPS://Example.COM/Path", "https://example.com/Path"},
		{"preserves path case, query, fragment", "https://example.com/A/b?Q=1#Frag", "https://example.com/A/b?Q=1#Frag"},
		{"trims surrounding space", "  https://example.com  ", "https://example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeURL(tc.in)
			if err != nil {
				t.Fatalf("normalizeURL(%q) error = %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("normalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
