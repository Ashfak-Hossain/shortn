//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

func TestNoDuplicateCodes(t *testing.T) {
	const (
		N        = 1000
		endpoint = "http://localhost/api/links"
	)

	codes := make([]string, N)
	errs := make([]error, N)
	var wg sync.WaitGroup

	for i := range N {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			body := bytes.NewBufferString(`{"url":"https://example.com"}`)
			resp, err := http.Post(endpoint, "application/json", body)
			if err != nil {
				errs[i] = fmt.Errorf("request failed: %w", err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusCreated {
				errs[i] = fmt.Errorf("unexpected status %d", resp.StatusCode)
				return
			}

			var result struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				errs[i] = fmt.Errorf("decode failed: %w", err)
				return
			}
			if result.Code == "" {
				errs[i] = fmt.Errorf("empty code in response")
				return
			}

			codes[i] = result.Code
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	seen := make(map[string]struct{}, N)
	for _, code := range codes {
		if _, dup := seen[code]; dup {
			t.Fatalf("duplicate code: %q", code)
		}
		seen[code] = struct{}{}
	}

	t.Logf("verified %d unique codes with zero duplicates", len(seen))
}

// go test ./test/... -v -tags integration -run TestNoDuplicateCodes -count=1
// zsh: correct './test/...' to './test/..' [nyae]? n
// === RUN   TestNoDuplicateCodes
//     uniqueness_test.go:77: verified 1000 unique codes with zero duplicates
// --- PASS: TestNoDuplicateCodes (0.52s)
// PASS
// ok  	github.com/Ashfak-Hossain/shortn/test	1.409s
