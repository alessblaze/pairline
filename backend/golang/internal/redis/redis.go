package redis

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const AdminActionStream = "admin:action:stream"

type Client struct {
	client redis.UniversalClient
}

func NewClient() *Client {
	addrs := redisAddrsFromEnv()
	password := os.Getenv("REDIS_PASSWORD")
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("REDIS_MODE")))

	rdb := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:    addrs,
		Password: password,
		DB:       0,
		MasterName: func() string {
			if mode == "sentinel" {
				return os.Getenv("REDIS_MASTER_NAME")
			}
			return ""
		}(),
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

	return r.publishJSON(ctx, data)
}

func (r *Client) publishJSON(ctx context.Context, data map[string]interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}

	err = r.client.XAdd(ctx, &redis.XAddArgs{
		Stream: AdminActionStream,
		MaxLen: 10_000,
		Approx: true,
		Values: map[string]interface{}{
			"payload": string(payload),
		},
	}).Err()
	return err
}

func (r *Client) Close() error {
	return r.client.Close()
}

func SetIndexedValue(
	ctx context.Context,
	client redis.UniversalClient,
	indexKey,
	key,
	value string,
	ttl time.Duration,
) error {
	if err := client.Set(ctx, key, value, ttl).Err(); err != nil {
		return err
	}

	return client.SAdd(ctx, indexKey, key).Err()
}

func DeleteIndexedKey(ctx context.Context, client redis.UniversalClient, indexKey, key string) error {
	if err := client.Del(ctx, key).Err(); err != nil {
		return err
	}

	return client.SRem(ctx, indexKey, key).Err()
}

// GetClient returns the underlying redis client
func (r *Client) GetClient() redis.UniversalClient {
	return r.client
}

func redisAddrsFromEnv() []string {
	clusterNodes := strings.TrimSpace(os.Getenv("REDIS_CLUSTER_NODES"))
	if clusterNodes != "" {
		parts := strings.Split(clusterNodes, ",")
		addrs := make([]string, 0, len(parts))

		for _, part := range parts {
			addr := strings.TrimSpace(part)
			if addr != "" {
				addrs = append(addrs, addr)
			}
		}

		if len(addrs) > 0 {
			return addrs
		}
	}

	addr := os.Getenv("REDIS_HOST")
	if addr == "" {
		addr = "localhost"
	}

	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}

	return []string{addr + ":" + port}
}
