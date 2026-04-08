// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/anish/omegle/backend/golang/internal/observability"
	internalredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var turnHTTPClient = &http.Client{Timeout: 5 * time.Second}

type cloudflareCredentialsRequest struct {
	TTL int `json:"ttl"`
}

type turnCredentialsClientRequest struct {
	SessionID    string `json:"session_id"`
	SessionToken string `json:"session_token"`
}

type cloudflareIceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type cloudflareCredentialsResponse struct {
	IceServers cloudflareIceServersWrapper `json:"iceServers"`
}

type cloudflareIceServersWrapper struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
}

// GetTURNCredentials proxies to Cloudflare Calls TURN API to generate ephemeral credentials.
// This keeps the Cloudflare API token server-side and hidden from clients.
func GetTURNCredentials(redisClient *internalredis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()
		cacheHit := false
		span := startHandlerSpan(c, "webrtc.turn.credentials")
		defer span.End()

		if c.Request.ContentLength > 4096 {
			span.SetStatus(codes.Error, "request too large")
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Request too large"})
			return
		}

		var authReq turnCredentialsClientRequest
		if err := c.ShouldBindJSON(&authReq); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "invalid request body")
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		sessionID := authReq.SessionID
		sessionToken := authReq.SessionToken
		span.SetAttributes(hashedAttribute("webrtc.session.ref", sessionID))
		if sessionID == "" || sessionToken == "" || len(sessionID) > 100 || len(sessionToken) > 128 || !uuidRe.MatchString(sessionID) {
			span.SetStatus(codes.Error, "invalid session format")
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id or session_token format"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if !verifySessionToken(ctx, redisClient.GetClient(), sessionID, sessionToken) {
			span.SetStatus(codes.Error, "invalid session")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
			return
		}

		// Cache TURN credentials briefly to reduce Cloudflare API QPS during reconnect storms.
		cacheKey := "webrtc:turn:cache:" + sessionID
		if cached, err := redisClient.GetClient().Get(ctx, cacheKey).Result(); err == nil && cached != "" {
			cacheHit = true
			span.SetAttributes(attribute.Bool("webrtc.turn.cache_hit", true))
			observability.RecordTURNRequest(c.Request.Context(), time.Since(startedAt), "cache_hit", true)
			c.Data(http.StatusOK, "application/json", []byte(cached))
			return
		}
		span.SetAttributes(attribute.Bool("webrtc.turn.cache_hit", false))

		keyID := os.Getenv("CLOUDFLARE_TURN_KEY_ID")
		apiToken := os.Getenv("CLOUDFLARE_TURN_API_TOKEN")

		if keyID == "" || apiToken == "" {
			log.Println("CLOUDFLARE_TURN_KEY_ID or CLOUDFLARE_TURN_API_TOKEN not configured")
			span.SetAttributes(attribute.Bool("webrtc.turn.fallback_stun_only", true))
			observability.RecordTURNRequest(c.Request.Context(), time.Since(startedAt), "stun_fallback", cacheHit)
			// Fallback: return public STUN only so WebRTC can still attempt direct P2P
			c.JSON(http.StatusOK, gin.H{
				"iceServers": []gin.H{
					{"urls": []string{"stun:stun.cloudflare.com:3478"}},
					{"urls": []string{"stun:stun.l.google.com:19302"}},
				},
			})
			return
		}

		cfURL := fmt.Sprintf("https://rtc.live.cloudflare.com/v1/turn/keys/%s/credentials/generate", keyID)

		// Request short-lived credentials to reduce abuse impact.
		reqBody, _ := json.Marshal(cloudflareCredentialsRequest{TTL: 3600})

		cfReq, err := http.NewRequestWithContext(c.Request.Context(), "POST", cfURL, bytes.NewReader(reqBody))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to build turn request")
			log.Printf("Failed to build Cloudflare TURN request: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build TURN request"})
			return
		}
		cfReq.Header.Set("Authorization", "Bearer "+apiToken)
		cfReq.Header.Set("Content-Type", "application/json")

		resp, err := turnHTTPClient.Do(cfReq)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to contact turn provider")
			observability.RecordTURNRequest(c.Request.Context(), time.Since(startedAt), "provider_error", cacheHit)
			log.Printf("Failed to reach Cloudflare TURN API: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to contact Cloudflare TURN API"})
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to read turn response")
			log.Printf("Failed to read Cloudflare TURN response: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read TURN response"})
			return
		}

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			span.SetAttributes(attribute.Int("webrtc.turn.status_code", resp.StatusCode))
			span.SetStatus(codes.Error, "turn provider error")
			observability.RecordTURNRequest(c.Request.Context(), time.Since(startedAt), "provider_error", cacheHit)
			log.Printf("Cloudflare TURN API error %d: %s", resp.StatusCode, string(body))
			c.JSON(http.StatusBadGateway, gin.H{"error": "Cloudflare TURN API returned an error"})
			return
		}

		var cfResp cloudflareCredentialsResponse
		if err := json.Unmarshal(body, &cfResp); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to parse turn response")
			log.Printf("Failed to parse Cloudflare TURN response: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse TURN credentials"})
			return
		}

		// Return structured iceServers array that RTCPeerConnection expects directly.
		responseBody, _ := json.Marshal(gin.H{
			"iceServers": []gin.H{
				{"urls": []string{"stun:stun.cloudflare.com:3478"}},
				{
					"urls":       cfResp.IceServers.URLs,
					"username":   cfResp.IceServers.Username,
					"credential": cfResp.IceServers.Credential,
				},
			},
		})

		if len(responseBody) > 0 {
			// Cache for 10 minutes (shorter than CF TTL) to reduce load while limiting reuse window.
			_ = redisClient.GetClient().Set(ctx, cacheKey, string(responseBody), 10*time.Minute).Err()
		}
		observability.RecordTURNRequest(c.Request.Context(), time.Since(startedAt), "success", cacheHit)

		c.Data(http.StatusOK, "application/json", responseBody)
	}
}
