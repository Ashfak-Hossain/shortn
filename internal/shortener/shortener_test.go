package shortener

import (
	"context"
	"errors"
	"testing"
)

// fakeStore is an in-memory LinkStore for unit tests — no Postgres needed.
type fakeStore struct {
	byCode map[string]*Link
	nextID int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{byCode: make(map[string]*Link)}
}

func (f *fakeStore) Create(_ context.Context, link *Link) error {
	if _, taken := f.byCode[link.Code]; taken {
		return ErrCodeExists
	}
	f.nextID++
	link.ID = f.nextID
	f.byCode[link.Code] = link
	return nil
}

func (f *fakeStore) GetByCode(_ context.Context, code string) (*Link, error) {
	link, ok := f.byCode[code]
	if !ok {
		return nil, ErrNotFound
	}
	return link, nil
}

// stubGen yields scripted codes in order so a test can force a collision.
type stubGen struct {
	codes []string
	calls int
}

func (g *stubGen) Generate() (string, error) {
	code := g.codes[g.calls%len(g.codes)]
	g.calls++
	return code, nil
}

func TestService_Create_Success(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, &stubGen{codes: []string{"abc1234"}})

	link, err := svc.Create(context.Background(), "https://example.com/path")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if link.Code != "abc1234" {
		t.Errorf("Code = %q, want %q", link.Code, "abc1234")
	}
	if link.LongURL != "https://example.com/path" {
		t.Errorf("LongURL = %q, want %q", link.LongURL, "https://example.com/path")
	}
	if link.ID == 0 {
		t.Error("ID was not set by the store")
	}
}

func TestService_Create_InvalidURL(t *testing.T) {
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

func TestService_Create_RetriesOnCollision(t *testing.T) {
	store := newFakeStore()
	store.byCode["taken00"] = &Link{Code: "taken00"} // first code is already used
	gen := &stubGen{codes: []string{"taken00", "free123"}}
	svc := NewService(store, gen)

	link, err := svc.Create(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if link.Code != "free123" {
		t.Errorf("Code = %q, want %q (should skip past the collision)", link.Code, "free123")
	}
	if gen.calls != 2 {
		t.Errorf("generator called %d times, want 2", gen.calls)
	}
}

func TestService_Create_ExhaustsRetries(t *testing.T) {
	store := newFakeStore()
	store.byCode["dup0000"] = &Link{Code: "dup0000"}
	gen := &stubGen{codes: []string{"dup0000"}} // always collides
	svc := NewService(store, gen)

	_, err := svc.Create(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("expected an error after exhausting retries, got nil")
	}
	if errors.Is(err, ErrCodeExists) {
		t.Errorf("exhaustion error should not be ErrCodeExists, got %v", err)
	}
	if gen.calls != maxCreateRetries {
		t.Errorf("generator called %d times, want %d", gen.calls, maxCreateRetries)
	}
}

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

func TestService_Resolve_NotFound(t *testing.T) {
	svc := NewService(newFakeStore(), &stubGen{codes: []string{"x"}})

	_, err := svc.Resolve(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrNotFound", err)
	}
}

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
