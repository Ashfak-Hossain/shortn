package idgen

import (
	"strings"
	"testing"
)

// TestRandomBase62_Generate verifies that the generator produces strings of the
// exact requested length and strictly adheres to the defined Base62 alphabet.
func TestRandomBase62_Generate(t *testing.T) {
	// We use a table-driven approach to cleanly test standard, extended, and
	// boundary-case lengths without duplicating assertion logic.
	cases := []struct {
		name   string
		length int
	}{
		{name: "length 7 (the production default)", length: 7},
		{name: "length 10", length: 10},
		{name: "length 1 (lower boundary edge case)", length: 1},
	}

	for _, tc := range cases {
		// We bind the loop variable to a local variable (handled automatically in Go 1.22+)
		// and use t.Run to execute each case as an isolated subtest. This ensures that if
		// one length fails, the others will still execute and report their status.
		t.Run(tc.name, func(t *testing.T) {
			gen := NewRandomBase62(tc.length)

			code, err := gen.Generate()
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}

			// We assert the exact byte length matches our configuration.
			if len(code) != tc.length {
				t.Fatalf("len(code) = %d, want %d", len(code), tc.length)
			}

			// We iterate through the generated string rune-by-rune to guarantee
			// no special characters or invalid bytes leaked into the output.
			for _, r := range code {
				if !strings.ContainsRune(alphabet, r) {
					t.Errorf("code %q contains %q, not in the base62 alphabet", code, r)
				}
			}
		})
	}
}

// TestRandomBase62_Uniqueness performs a statistical sanity check to ensure
// the generator produces varying outputs over a large batch without immediate collisions.
func TestRandomBase62_Uniqueness(t *testing.T) {
	const n = 1000
	gen := NewRandomBase62(7)

	// We use a map with an empty struct as the value. This creates a true "Set" in Go,
	// requiring zero bytes of memory allocation for the values, which is ideal
	// for tracking large batches of strings in tests.
	seen := make(map[string]struct{}, n)

	for i := 0; i < n; i++ {
		code, err := gen.Generate()
		if err != nil {
			t.Fatalf("Generate() error = %v", err)
		}

		// If a collision occurs within a batch this small (1000 items), it indicates
		// a catastrophic failure or predictability in the underlying entropy source.
		if _, dup := seen[code]; dup {
			t.Fatalf("duplicate code %q after %d generations", code, i)
		}

		seen[code] = struct{}{}
	}
}
