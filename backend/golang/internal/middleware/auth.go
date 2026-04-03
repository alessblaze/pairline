package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/storage"
	"github.com/gorilla/mux"
)

type contextKey string

const (
	UserKey     contextKey = "user"
	RoleKey     contextKey = "role"
	DatabaseKey contextKey = "database"
	RedisKey    contextKey = "redis"
)

type User struct {
	Username string
	Role     string
}

func JWTAuth(jwtSecret string, db *storage.Database) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("Authorization")
			if token == "" {
				sendError(w, "Authorization header required", http.StatusUnauthorized)
				return
			}

			if len(token) > 7 && strings.HasPrefix(token, "Bearer ") {
				token = token[7:]
			}

			username, role, err := verifyJWT(token, jwtSecret)
			if err != nil {
				sendError(w, "Invalid token", http.StatusUnauthorized)
				return
			}

			var admin storage.AdminAccount
			if err := db.GetDB().Where("username = ? AND is_active = ?", username, true).First(&admin).Error; err != nil {
				sendError(w, "Invalid token", http.StatusUnauthorized)
				return
			}

			_ = role
			ctx := context.WithValue(r.Context(), UserKey, &User{Username: admin.Username, Role: admin.Role})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireDatabase(db *storage.Database) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), DatabaseKey, db)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireRedis(redisClient *redis.Client) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), RedisKey, redisClient)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RoleAuth(allowedRoles []string) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := r.Context().Value(UserKey).(*User)
			if !ok {
				sendError(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			if !contains(allowedRoles, user.Role) {
				sendError(w, "Insufficient permissions", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func verifyJWT(token string, jwtSecret string) (string, string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", fmt.Errorf("invalid token format")
	}

	signatureInput := parts[0] + "." + parts[1]
	h := hmac.New(sha256.New, []byte(jwtSecret))
	h.Write([]byte(signatureInput))
	expectedSignature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	if !hmac.Equal([]byte(parts[2]), []byte(expectedSignature)) {
		return "", "", fmt.Errorf("invalid token signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", err
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", "", err
	}

	expires, ok := claims["exp"].(float64)
	if !ok {
		return "", "", fmt.Errorf("missing expiration")
	}

	if float64(time.Now().Unix()) > expires {
		return "", "", fmt.Errorf("token expired")
	}

	username, ok := claims["username"].(string)
	if !ok {
		return "", "", fmt.Errorf("missing username")
	}

	role, ok := claims["role"].(string)
	if !ok {
		return "", "", fmt.Errorf("missing role")
	}

	return username, role, nil
}

// VerifyJWT is exported for use in server middleware
func VerifyJWT(token string, jwtSecret string) (string, string, error) {
	return verifyJWT(token, jwtSecret)
}

func GenerateJWT(username, role, jwtSecret string, expiresHours int) (string, error) {
	expiration := time.Now().Add(time.Duration(expiresHours) * time.Hour).Unix()

	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}

	headerJSON, _ := json.Marshal(header)
	headerEncoded := base64.RawURLEncoding.EncodeToString(headerJSON)

	payload := map[string]interface{}{
		"username": username,
		"role":     role,
		"iat":      time.Now().Unix(),
		"exp":      expiration,
	}

	payloadJSON, _ := json.Marshal(payload)
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signatureInput := headerEncoded + "." + payloadEncoded

	h := hmac.New(sha256.New, []byte(jwtSecret))
	h.Write([]byte(signatureInput))
	signature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	return signatureInput + "." + signature, nil
}

func sendError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": message,
		"code":  status,
	})
}
