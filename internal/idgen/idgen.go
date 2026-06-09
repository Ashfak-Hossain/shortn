// Package idgen provides utilities for generating unique ids.
package idgen

import (
	"crypto/rand"
	"math/big"
)

const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ" // 62 char

// RandomBase62 generates random strings of a specified length using the alphabet.
type RandomBase62 struct {
	length int
}

// NewRandomBase62 returns a new RandomBase62 generator with the specified length.
func NewRandomBase62(length int) RandomBase62 {
	return RandomBase62{length: length}
}

// Generate a random string. The randomness is crypto secure, so the probab of a collision is less
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
