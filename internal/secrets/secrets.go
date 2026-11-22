// Package secrets provides at-rest encryption (AES-GCM), random password and
// token generation and token hashing.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

// Box encrypts and decrypts small secrets (passwords, license keys).
type Box struct{ aead cipher.AEAD }

// ParseKey accepts a 32-byte key as base64 (std or url) or 64 hex characters.
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errors.New("secret key must be 32 bytes, encoded as 64 hex characters or base64")
}

// LoadOrCreateKey returns the configured key, or (if empty) reads/creates a
// persistent random key file in dataDir. created is true if a new file was written.
func LoadOrCreateKey(configured, dataDir string) (key []byte, created bool, err error) {
	if configured != "" {
		k, err := ParseKey(configured)
		return k, false, err
	}
	path := filepath.Join(dataDir, "secret.key")
	if b, err := os.ReadFile(path); err == nil {
		k, err := ParseKey(string(b))
		return k, false, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(k)+"\n"), 0o600); err != nil {
		return nil, false, err
	}
	return k, true, nil
}

// NewBox creates a Box from a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("secrets: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypt returns base64(nonce || ciphertext). Empty input yields "".
func (b *Box) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt reverses Encrypt. Empty input yields "".
func (b *Box) Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secrets: ciphertext too short")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("secrets: decrypt: %w", err)
	}
	return string(plain), nil
}

const (
	lower   = "abcdefghijkmnopqrstuvwxyz" // no l
	upper   = "ABCDEFGHJKLMNPQRSTUVWXYZ"  // no I, O
	digits  = "23456789"                  // no 0, 1
	symbols = "-_."                       // safe in XML, PowerShell, shells, URLs
)

// Password generates a random password of length n (min 20) that contains at
// least one lowercase, uppercase, digit and symbol character, so it satisfies
// the Windows complexity policy. The alphabet avoids ambiguous characters and
// anything that needs quoting in unattend files, PowerShell or shells.
func Password(n int) (string, error) {
	if n < 20 {
		n = 20
	}
	all := lower + upper + digits + symbols
	classes := []string{lower, upper, digits, symbols}
	out := make([]byte, n)
	for i := range out {
		set := all
		if i < len(classes) {
			set = classes[i]
		}
		c, err := pick(set)
		if err != nil {
			return "", err
		}
		out[i] = c
	}
	// Shuffle so the guaranteed characters are not always at the start; keep
	// the first character a letter (some tools dislike a leading symbol).
	for i := len(out) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}
		out[i], out[j.Int64()] = out[j.Int64()], out[i]
	}
	for i, c := range out {
		if strings.IndexByte(lower+upper, c) >= 0 {
			out[0], out[i] = out[i], out[0]
			break
		}
	}
	return string(out), nil
}

func pick(set string) (byte, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
	if err != nil {
		return 0, err
	}
	return set[n.Int64()], nil
}

// Token returns prefix + 32 random bytes, base64url encoded.
func Token(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// Hash returns the hex sha256 of a token (tokens are high-entropy, so a fast
// hash is appropriate).
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
