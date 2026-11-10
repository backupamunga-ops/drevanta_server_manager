package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP parameters (RFC 6238 default profile, compatible with Google
// Authenticator, Aegis, 1Password, FreeOTP, ...).
const (
	totpDigits  = 6
	totpStep    = 30 * time.Second
	totpSkew    = 1  // accept ±1 step around the current one
	totpSecretN = 20 // bytes -> 160-bit secret, base32 = 32 chars
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a fresh 160-bit TOTP secret, base32-encoded.
func NewTOTPSecret() (string, error) {
	raw := make([]byte, totpSecretN)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate totp secret: %w", err)
	}
	return b32.EncodeToString(raw), nil
}

// TOTPURI renders the otpauth:// provisioning URI for authenticator apps.
func TOTPURI(secret, issuer, account string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// totpAt computes the HOTP value of secret at unix time t (RFC 4226 over
// the time counter, SHA-1, 6 digits).
func totpAt(secret string, t time.Time) (int64, int64, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil || len(key) == 0 {
		return 0, 0, fmt.Errorf("invalid totp secret")
	}
	step := int64(t.Unix() / int64(totpStep/time.Second))
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	bin := (int64(sum[offset]&0x7f) << 24) |
		(int64(sum[offset+1]) << 16) |
		(int64(sum[offset+2]) << 8) |
		int64(sum[offset+3])
	mod := int64(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return bin % mod, step, nil
}

// ComputeTOTP returns the current 6-digit code for a secret. Exposed for
// tooling and tests that act as the authenticator app.
func ComputeTOTP(secret string, at time.Time) (string, error) {
	v, _, err := totpAt(secret, at)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", v), nil
}

// VerifyTOTP checks a 6-digit code against the secret, allowing ±1 step
// of clock drift. It returns the matched step counter and whether the
// code is valid. Comparison is constant-time per candidate.
//
// Design note: the same code may be presented repeatedly within its
// 30-second window (mainstream TOTP behaviour — GitHub, Google, ...).
// A stricter single-use-per-step guard was tried and rejected: it locks
// an operator out of quick re-logins for up to 30 s while offering no
// practical protection (an attacker racing a phished code needs only
// one use).
func VerifyTOTP(secret, code string, at time.Time) (step int64, ok bool, err error) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false, nil
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return 0, false, nil
		}
	}
	var want int64
	if _, err := fmt.Sscanf(code, "%06d", &want); err != nil {
		return 0, false, nil
	}
	for delta := -int64(totpSkew); delta <= int64(totpSkew); delta++ {
		got, step, err := totpAt(secret, at.Add(time.Duration(delta)*totpStep))
		if err != nil {
			return 0, false, err
		}
		if hmac.Equal([]byte(fmt.Sprintf("%06d", got)), []byte(fmt.Sprintf("%06d", want))) {
			return step, true, nil
		}
	}
	return 0, false, nil
}
