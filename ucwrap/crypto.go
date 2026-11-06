package ucwrap

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"

	"golang.org/x/crypto/pbkdf2"
)

const (
	pbkdf2Iterations = 100000
	saltLen          = 16
	ivLen            = 16
	keyLen           = 32 // AES-256
)

// aesEncrypt encrypts plaintext with AES-256-CBC. The key is derived from the
// passphrase via PBKDF2-HMAC-SHA256. The returned blob is:
//
//	salt(16) || IV(16) || ciphertext(PKCS7-padded)
func aesEncrypt(plaintext []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, saltLen)
	iv := make([]byte, ivLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}

	key := pbkdf2.Key([]byte(passphrase), salt, pbkdf2Iterations, keyLen, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	padded := pkcs7Pad(plaintext, block.BlockSize())
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	out := make([]byte, 0, saltLen+ivLen+len(ciphertext))
	out = append(out, salt...)
	out = append(out, iv...)
	out = append(out, ciphertext...)
	return out, nil
}

// aesDecrypt reverses aesEncrypt. A wrong passphrase typically surfaces as an
// invalid-padding error.
func aesDecrypt(blob []byte, passphrase string) ([]byte, error) {
	if len(blob) < saltLen+ivLen+aes.BlockSize {
		return nil, errors.New("ucwrap: encrypted blob too short")
	}
	salt := blob[:saltLen]
	iv := blob[saltLen : saltLen+ivLen]
	ciphertext := blob[saltLen+ivLen:]

	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("ucwrap: ciphertext is not a multiple of the block size")
	}

	key := pbkdf2.Key([]byte(passphrase), salt, pbkdf2Iterations, keyLen, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	return pkcs7Unpad(plain, block.BlockSize())
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("ucwrap: invalid PKCS7 padding (length)")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, errors.New("ucwrap: invalid PKCS7 padding (size)")
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, errors.New("ucwrap: invalid PKCS7 padding (bytes)")
		}
	}
	return data[:len(data)-pad], nil
}
