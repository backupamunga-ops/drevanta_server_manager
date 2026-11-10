package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// tokenPrefix identifies Drevanta-issued bearer values in logs and files
// without leaking entropy. The claim token carries it; session cookies do
// not (they live in an HttpOnly cookie and are never displayed).
const tokenPrefix = "drv1_"

// NewToken returns a 256-bit cryptographically secure random token,
// base64url-encoded (43 chars) with the "drv1_" prefix, plus its
// SHA-256 hex hash for storage. The plaintext is shown once to the
// operator; only the hash is persisted anywhere.
func NewToken() (token string, hash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, TokenHash(token), nil
}

// TokenHash derives the storage hash of a token (SHA-256 hex). A plain
// SHA-256 is appropriate here because the token is 256 bits of CSPRNG
// output — precomputation is meaningless.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// recoveryAlphabet is Crockford-style base32 without the most ambiguous
// glyphs (no 0/O/1/I/L); recovery codes are read aloud and typed from
// paper, so typo-resistance matters more than density.
const recoveryAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewRecoveryCode returns one 8-character recovery code (40 bits).
func NewRecoveryCode() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate recovery code: %w", err)
	}
	var b strings.Builder
	for _, v := range raw {
		b.WriteByte(recoveryAlphabet[int(v)%len(recoveryAlphabet)])
	}
	s := b.String()
	return s[:4] + "-" + s[4:], nil
}

// NormalizeRecoveryCode canonicalizes operator input: lowercase, hyphens
// and spaces stripped, upper-cased back into the code alphabet.
func NormalizeRecoveryCode(code string) string {
	c := strings.ToUpper(code)
	var b strings.Builder
	for _, r := range c {
		switch {
		case r == '-' || r == ' ':
			continue
		case r == 'O':
			b.WriteByte('0') // not in alphabet, but normalize defensively
		case r == 'I' || r == 'L':
			b.WriteByte('1')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// HashRecoveryCode hashes a normalized recovery code for storage. Codes
// are 40 random bits, so a keyed hash is unnecessary; SHA-256 prevents
// anyone with database access from using the codes directly.
func HashRecoveryCode(normalizedCode string) string {
	sum := sha256.Sum256([]byte(normalizedCode))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeHexCompare compares two hex digests in constant time.
func ConstantTimeHexCompare(a, b string) bool {
	return len(a) == len(b) && hmac.Equal([]byte(a), []byte(b))
}
