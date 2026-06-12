// Package idgen provides utilities for generating unique, unpredictable identifiers.
// It abstracts the underlying randomness implementation to ensure system-wide
// consistency, security, and collision resistance.
package idgen

import (
	"crypto/rand"
	"math/big"
)

// We use a 62-char alphabet (numbers, lowercase, uppercase) to maximize
// the entropy per character while remaining completely URL-safe without requiring
// additional encoding.
const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ" // 62 char

// RandomBase62 is a cryptographically secure string generator. It produces
// unpredictable, Base62-encoded identifiers of a fixed length.
type RandomBase62 struct {
	length int
}

// NewRandomBase62 initializes a generator configured to produce identifiers
// of the exact specified length.
func NewRandomBase62(length int) RandomBase62 {
	return RandomBase62{length: length}
}

// Generate produces a new random string.
// It guarantees cryptographic security by utilizing the crypto/rand package,
// minimizing the probability of collisions and preventing predictability attacks.
func (r RandomBase62) Generate() (string, error) {
	// We pre-calculate the upper limit for our random number generation based
	// on the exact size of our alphabet to ensure uniform distribution.
	limit := big.NewInt(int64(len(alphabet)))

	// We pre-allocate exactly the required byte slice length to minimize memory
	// allocations and avoid expensive slice resizing during the loop.
	code := make([]byte, r.length)

	for i := range code {
		// We explicitly use crypto/rand rather than math/rand to prevent enumeration
		// attacks, where malicious users might attempt to guess the underlying seed
		// and predict future shortened URLs.
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		// We map the secure random integer directly to our alphabet index.
		code[i] = alphabet[n.Int64()]
	}
	return string(code), nil
}
