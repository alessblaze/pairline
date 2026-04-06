package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestGenerateJWTWithTypeVerifiesExpectedTypes(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"

	accessToken, err := GenerateJWTWithType("alice", "moderator", TokenTypeAccess, secret, 15*time.Minute)
	if err != nil {
		t.Fatalf("GenerateJWTWithType(access) returned error: %v", err)
	}

	refreshToken, err := GenerateJWTWithType("alice", "moderator", TokenTypeRefresh, secret, time.Hour)
	if err != nil {
		t.Fatalf("GenerateJWTWithType(refresh) returned error: %v", err)
	}

	if _, _, err := VerifyJWT(accessToken, secret); err != nil {
		t.Fatalf("VerifyJWT(access) returned error: %v", err)
	}

	if _, _, err := VerifyJWT(refreshToken, secret); err == nil {
		t.Fatal("VerifyJWT should reject refresh tokens on access-only paths")
	}

	if _, _, err := VerifyJWTWithType(refreshToken, secret, TokenTypeRefresh); err != nil {
		t.Fatalf("VerifyJWTWithType(refresh) returned error: %v", err)
	}
}

func TestVerifyJWTTreatsLegacyTokensAsAccessTokens(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	token := legacyToken(t, "alice", "admin", secret, time.Now().Add(time.Hour))

	username, role, err := VerifyJWT(token, secret)
	if err != nil {
		t.Fatalf("VerifyJWT returned error for legacy token: %v", err)
	}

	if username != "alice" || role != "admin" {
		t.Fatalf("VerifyJWT() = (%q, %q), want (%q, %q)", username, role, "alice", "admin")
	}
}

func legacyToken(t *testing.T, username, role, secret string, expiresAt time.Time) string {
	t.Helper()

	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}
	payload := map[string]interface{}{
		"username": username,
		"role":     role,
		"iat":      time.Now().Unix(),
		"exp":      expiresAt.Unix(),
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("json.Marshal(header) returned error: %v", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal(payload) returned error: %v", err)
	}

	headerEncoded := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signatureInput := headerEncoded + "." + payloadEncoded

	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(signatureInput))
	signature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	return signatureInput + "." + signature
}
