package plsqlwrap

import (
	"bytes"
	"testing"
)

func TestAESRoundTrip(t *testing.T) {
	plain := []byte("orig1=a|orig2=b|")
	blob, err := aesEncrypt(plain, "passphrase")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Wire format: salt(16) + IV(16) + ciphertext, ciphertext a block multiple.
	if len(blob) < saltLen+ivLen+16 {
		t.Fatalf("blob too short: %d", len(blob))
	}
	if (len(blob)-saltLen-ivLen)%16 != 0 {
		t.Errorf("ciphertext not a multiple of block size")
	}

	got, err := aesDecrypt(blob, "passphrase")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("roundtrip mismatch: got %q want %q", got, plain)
	}
}

func TestAESWrongPassphrase(t *testing.T) {
	blob, err := aesEncrypt([]byte("secret data here"), "right")
	if err != nil {
		t.Fatal(err)
	}
	// Wrong passphrase almost always yields an invalid-padding error.
	if _, err := aesDecrypt(blob, "wrong"); err == nil {
		t.Error("expected decryption to fail with wrong passphrase")
	}
}

func TestAESRandomizedSaltIV(t *testing.T) {
	// Two encryptions of the same plaintext must differ (random salt/IV).
	a, _ := aesEncrypt([]byte("same"), "p")
	b, _ := aesEncrypt([]byte("same"), "p")
	if bytes.Equal(a, b) {
		t.Error("expected different ciphertexts due to random salt/IV")
	}
}

func TestPKCS7(t *testing.T) {
	for _, n := range []int{0, 1, 15, 16, 17, 31, 32} {
		data := bytes.Repeat([]byte{0x41}, n)
		padded := pkcs7Pad(data, 16)
		if len(padded)%16 != 0 {
			t.Fatalf("padded length %d not block-aligned for n=%d", len(padded), n)
		}
		unpadded, err := pkcs7Unpad(padded, 16)
		if err != nil {
			t.Fatalf("unpad n=%d: %v", n, err)
		}
		if !bytes.Equal(unpadded, data) {
			t.Errorf("pkcs7 roundtrip mismatch for n=%d", n)
		}
	}
}
