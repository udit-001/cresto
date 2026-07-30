package ais

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const passwordMiddle = "GQ39%*g"

// Decrypt reverses the AIS portal's AES-256-CBC encryption.
//
// The file format is: first 32 hex chars = IV, next 32 hex chars = salt,
// remaining = base64-encoded ciphertext. Key derivation is PBKDF2 with
// SHA-256 HMAC, 1000 iterations, 32-byte key. Password = pan.lower() + "GQ39%*g" + dob.
//
// If the input is already valid JSON (plain-text, not encrypted), it is
// returned as-is — this lets the handler accept both encrypted and
// pre-decrypted files.
func Decrypt(encrypted string, pan, dob string) ([]byte, error) {
	trimmed := strings.TrimSpace(encrypted)

	// Already-decrypted JSON: return as-is.
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return []byte(trimmed), nil
	}

	if len(trimmed) < 64 {
		return nil, fmt.Errorf("ais: encrypted data too short (%d chars)", len(trimmed))
	}

	iv, err := hex.DecodeString(trimmed[:32])
	if err != nil {
		return nil, fmt.Errorf("ais: invalid IV hex: %w", err)
	}
	salt, err := hex.DecodeString(trimmed[32:64])
	if err != nil {
		return nil, fmt.Errorf("ais: invalid salt hex: %w", err)
	}

	ciphertext, err := base64.StdEncoding.DecodeString(trimmed[64:])
	if err != nil {
		return nil, fmt.Errorf("ais: invalid base64 ciphertext: %w", err)
	}

	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ais: ciphertext length %d not block-aligned", len(ciphertext))
	}

	password := strings.ToLower(pan) + passwordMiddle + dob
	key := pbkdf2.Key([]byte(password), salt, 1000, 32, sha256.New)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("ais: create cipher: %w", err)
	}

	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)

	// PKCS7 unpad.
	padLen := int(plaintext[len(plaintext)-1])
	if padLen == 0 || padLen > aes.BlockSize || padLen > len(plaintext) {
		return nil, fmt.Errorf("ais: invalid PKCS7 padding (padLen=%d)", padLen)
	}
	for i := len(plaintext) - padLen; i < len(plaintext); i++ {
		if int(plaintext[i]) != padLen {
			return nil, fmt.Errorf("ais: invalid PKCS7 padding byte")
		}
	}
	return plaintext[:len(plaintext)-padLen], nil
}
