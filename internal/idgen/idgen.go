// Package idgen provides utilities for generating unique identifiers,-
// such as the short codes for URLs. The current implementation uses a -
// cryptographically secure random generator to produce base62 strings, which are compact and URL-friendly.
// This approach minimizes the risk of collisions while keeping the generated codes short and easy to use in URLs.
package idgen

import (
	"crypto/rand"
	"math/big"
)

const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// RandomBase62 generates random strings of a specified length using the  alphabet.
type RandomBase62 struct {
	length int
}

// NewRandomBase62 returns a new RandomBase62 generator with the specified length.
func NewRandomBase62(length int) RandomBase62 {
	return RandomBase62{length: length}
}

// Generate produces a random string of the configured length. The randomness is cryptographically secure, so the
// probability of a collision is negligible for reasonable lengths.
func (g RandomBase62) Generate() (string, error) {
	limit := big.NewInt(int64(len(alphabet)))
	code := make([]byte, g.length)

	for i := range code {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		code[i] = alphabet[n.Int64()]
	}
	return string(code), nil
}
