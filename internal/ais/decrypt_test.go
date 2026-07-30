package ais

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"golang.org/x/crypto/pbkdf2"
)

const testPAN = "ABCDE1234F"
const testDOB = "15061990"

// encryptForTest mirrors the AIS portal's encryption so tests can produce
// encrypted fixtures from known plaintext.
func encryptForTest(t *testing.T, plaintext string, pan, dob string) string {
	t.Helper()
	iv := []byte("0123456789abcdef")   // 16 bytes = AES block size
	salt := []byte("abcdefghijklmnop") // 16 bytes (32 hex chars)

	password := strings.ToLower(pan) + passwordMiddle + dob
	key := pbkdf2.Key([]byte(password), salt, 1000, 32, sha256.New)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}

	pt := []byte(plaintext)
	padLen := aes.BlockSize - len(pt)%aes.BlockSize
	padded := append(pt, repeat(byte(padLen), padLen)...)

	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	return hex.EncodeToString(iv) + hex.EncodeToString(salt) + base64.StdEncoding.EncodeToString(ciphertext)
}

func repeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestDecrypt_CorrectPassword(t *testing.T) {
	want := `{"metadata":{"jsonVersion":"15.0.0"},"header":{"columnData":["2025-26"]}}`
	enc := encryptForTest(t, want, testPAN, testDOB)

	got, err := Decrypt(enc, testPAN, testDOB)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != want {
		t.Errorf("Decrypt = %q, want %q", got, want)
	}
}

func TestDecrypt_WrongPassword(t *testing.T) {
	enc := encryptForTest(t, `{"hello":"world"}`, testPAN, testDOB)
	_, err := Decrypt(enc, "WRONG1234P", testDOB)
	if err == nil {
		t.Fatal("Decrypt with wrong PAN: want error, got nil")
	}
}

func TestDecrypt_WrongDOB(t *testing.T) {
	enc := encryptForTest(t, `{"hello":"world"}`, testPAN, testDOB)
	_, err := Decrypt(enc, testPAN, "99999999")
	if err == nil {
		t.Fatal("Decrypt with wrong DOB: want error, got nil")
	}
}

func TestDecrypt_PANCaseInsensitive(t *testing.T) {
	want := `{"test":true}`
	enc := encryptForTest(t, want, testPAN, testDOB)

	got, err := Decrypt(enc, "abcde1234f", testDOB)
	if err != nil {
		t.Fatalf("Decrypt with lowercase PAN: %v", err)
	}
	if string(got) != want {
		t.Errorf("Decrypt = %q, want %q", got, want)
	}
}

func TestDecrypt_AlreadyDecryptedJSON(t *testing.T) {
	want := `{"metadata":{"jsonVersion":"15.0.0"}}`
	got, err := Decrypt(want, testPAN, testDOB)
	if err != nil {
		t.Fatalf("Decrypt plain JSON: %v", err)
	}
	if string(got) != want {
		t.Errorf("Decrypt = %q, want %q", got, want)
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	_, err := Decrypt("short", testPAN, testDOB)
	if err == nil {
		t.Fatal("Decrypt too-short input: want error, got nil")
	}
}
