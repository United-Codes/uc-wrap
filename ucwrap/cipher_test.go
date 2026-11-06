package ucwrap

import "testing"

// cipherFrom must be a permutation of all 256 byte values.
func TestCipherFromIsPermutation(t *testing.T) {
	var seen [256]bool
	for i := 0; i < 256; i++ {
		v := cipherFrom[i]
		if seen[v] {
			t.Fatalf("cipherFrom is not a permutation: value 0x%02X appears twice", v)
		}
		seen[v] = true
	}
	for i, ok := range seen {
		if !ok {
			t.Fatalf("cipherFrom is not a permutation: value 0x%02X never appears", i)
		}
	}
}

// cipherTo must be the exact inverse of cipherFrom for every byte.
func TestCipherToIsInverse(t *testing.T) {
	for i := 0; i < 256; i++ {
		b := byte(i)
		if got := cipherTo[cipherFrom[b]]; got != b {
			t.Errorf("cipherTo[cipherFrom[%d]] = %d, want %d", i, got, i)
		}
		if got := cipherFrom[cipherTo[b]]; got != b {
			t.Errorf("cipherFrom[cipherTo[%d]] = %d, want %d", i, got, i)
		}
	}
}
