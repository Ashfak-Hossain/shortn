package idgen

import (
	"strings"
	"testing"
)

// TestRandomBase62_Generate is table-driven over the code length: each case
// asserts the output is exactly the requested length and uses only base62
// characters. The "table" is the set of lengths we want to hold true.
func TestRandomBase62_Generate(t *testing.T) {
	cases := []struct {
		name   string
		length int
	}{
		{name: "length 7 (the production default)", length: 7},
		{name: "length 10", length: 10},
		{name: "length 1", length: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gen := NewRandomBase62(tc.length)

			code, err := gen.Generate()
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			if len(code) != tc.length {
				t.Fatalf("len(code) = %d, want %d", len(code), tc.length)
			}
			for _, r := range code {
				if !strings.ContainsRune(alphabet, r) {
					t.Errorf("code %q contains %q, not in the base62 alphabet", code, r)
				}
			}
		})
	}
}

// TestRandomBase62_Uniqueness is a sanity check that the generator varies its
// output rather than returning a constant. It generates a batch of codes and fails if any duplicates are seen.
func TestRandomBase62_Uniqueness(t *testing.T) {
	const n = 1000
	gen := NewRandomBase62(7)
	seen := make(map[string]struct{}, n)

	for i := 0; i < n; i++ {
		code, err := gen.Generate()
		if err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
		if _, dup := seen[code]; dup {
			t.Fatalf("duplicate code %q after %d generations", code, i)
		}
		seen[code] = struct{}{}
	}
}
