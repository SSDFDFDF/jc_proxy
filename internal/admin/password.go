package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	defaultIter    = 120000
	saltLen        = 16
	keyLen         = 32
	bootstrapPwLen = 18
)

// HashPassword returns format: pbkdf2$<iter>$<salt_b64>$<hash_hex>
func HashPassword(password string) (string, error) {
	if strings.TrimSpace(password) == "" {
		return "", fmt.Errorf("empty password")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := pbkdf2.Key([]byte(password), salt, defaultIter, keyLen, sha256.New)
	return fmt.Sprintf("pbkdf2$%d$%s$%s", defaultIter, base64.RawStdEncoding.EncodeToString(salt), hex.EncodeToString(key)), nil
}

func GenerateRandomPassword() (string, error) {
	raw := make([]byte, bootstrapPwLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func VerifyPassword(password, encoded string) bool {
	if strings.TrimSpace(encoded) == "" {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got := pbkdf2.Key([]byte(password), salt, iter, len(expected), sha256.New)
	return subtle.ConstantTimeCompare(got, expected) == 1
}
