package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	internalredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/gin-gonic/gin"
)

type cloudflareCredentialsRequest struct {
	TTL int `json:"ttl"`
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
		sessionID := c.Query("session_id")
		sessionToken := c.Query("session_token")
		if sessionID == "" || sessionToken == "" || len(sessionID) > 100 || len(sessionToken) > 128 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id or session_token format"})
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if !verifySessionToken(ctx, redisClient, sessionID, sessionToken) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
			return
		}

		keyID := os.Getenv("CLOUDFLARE_TURN_KEY_ID")
		apiToken := os.Getenv("CLOUDFLARE_TURN_API_TOKEN")

		if keyID == "" || apiToken == "" {
			log.Println("CLOUDFLARE_TURN_KEY_ID or CLOUDFLARE_TURN_API_TOKEN not configured")
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

		httpClient := &http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequest("POST", cfURL, strings.NewReader(string(reqBody)))
		if err != nil {
			log.Printf("Failed to build Cloudflare TURN request: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build TURN request"})
			return
		}
		req.Header.Set("Authorization", "Bearer "+apiToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			log.Printf("Failed to reach Cloudflare TURN API: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to contact Cloudflare TURN API"})
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
		if err != nil {
			log.Printf("Failed to read Cloudflare TURN response: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read TURN response"})
			return
		}

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			log.Printf("Cloudflare TURN API error %d: %s", resp.StatusCode, string(body))
			c.JSON(http.StatusBadGateway, gin.H{"error": "Cloudflare TURN API returned an error"})
			return
		}

		var cfResp cloudflareCredentialsResponse
		if err := json.Unmarshal(body, &cfResp); err != nil {
			log.Printf("Failed to parse Cloudflare TURN response: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse TURN credentials"})
			return
		}

		// Return structured iceServers array that RTCPeerConnection expects directly
		c.JSON(http.StatusOK, gin.H{
			"iceServers": []gin.H{
				{"urls": []string{"stun:stun.cloudflare.com:3478"}},
				{
					"urls":       cfResp.IceServers.URLs,
					"username":   cfResp.IceServers.Username,
					"credential": cfResp.IceServers.Credential,
				},
			},
		})
	}
}
