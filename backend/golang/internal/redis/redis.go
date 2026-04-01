package redis

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	BanActionChannel = "admin:action"
)

type Client struct {
	client *redis.Client
}

func NewClient() *Client {
	addr := os.Getenv("REDIS_HOST")
	if addr == "" {
		addr = "localhost"
	}

	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}

	password := os.Getenv("REDIS_PASSWORD")

	rdb := redis.NewClient(&redis.Options{
		Addr:     addr + ":" + port,
		Password: password,
		DB:       0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Printf("Warning: Failed to connect to Redis: %v", err)
	} else {
		log.Println("Connected to Redis successfully")
	}

	return &Client{client: rdb}
}

func (r *Client) PublishBanAction(ctx context.Context, sessionID, ipAddress, reason string) error {
	data := map[string]interface{}{
		"action":     "emergency_ban",
		"session_id": sessionID,
		"ip":         ipAddress,
		"reason":     reason,
		"timestamp":  time.Now().UnixMilli(),
	}

	return r.publishJSON(ctx, data)
}

func (r *Client) PublishBanIPAction(ctx context.Context, ipAddress, reason string) error {
	data := map[string]interface{}{
		"action":    "emergency_ban_ip",
		"ip":        ipAddress,
		"reason":    reason,
		"timestamp": time.Now().UnixMilli(),
	}

	return r.publishJSON(ctx, data)
}

func (r *Client) PublishUnbanAction(ctx context.Context, sessionID, ipAddress string) error {
	data := map[string]interface{}{
		"action":     "emergency_unban",
		"session_id": sessionID,
		"ip":         ipAddress,
		"timestamp":  time.Now().UnixMilli(),
	}

	return r.publishJSON(ctx, data)
}

func (r *Client) PublishUnbanIPAction(ctx context.Context, ipAddress string) error {
	data := map[string]interface{}{
		"action":    "emergency_unban_ip",
		"ip":        ipAddress,
		"timestamp": time.Now().UnixMilli(),
	}

	return r.publishJSON(ctx, data)
}

func (r *Client) PublishDisconnectAction(ctx context.Context, sessionID string) error {
	data := map[string]interface{}{
		"action":     "emergency_disconnect",
		"session_id": sessionID,
		"timestamp":  time.Now().UnixMilli(),
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return r.client.Publish(ctx, BanActionChannel, payload).Err()
}

func (r *Client) publishJSON(ctx context.Context, data map[string]interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}

	return r.client.Publish(ctx, BanActionChannel, payload).Err()
}

func (r *Client) Close() error {
	return r.client.Close()
}

// GetClient returns the underlying redis client
func (r *Client) GetClient() *redis.Client {
	return r.client
}
