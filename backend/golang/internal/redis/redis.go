package redis

import (
	"context"
	"encoding/json"
	"fmt"
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
	addrs, err := redisClusterAddrsFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	password := os.Getenv("REDIS_PASSWORD")

	rdb := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:    addrs,
		Password: password,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		log.Fatalf("Failed to connect to Redis Cluster: %v", err)
	}

	log.Println("Connected to Redis Cluster successfully")
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

	if err := client.SAdd(ctx, indexKey, key).Err(); err != nil {
		if rollbackErr := client.Del(ctx, key).Err(); rollbackErr != nil {
			return fmt.Errorf("add index entry: %w (rollback delete failed: %v)", err, rollbackErr)
		}

		return fmt.Errorf("add index entry: %w", err)
	}

	return nil
}

func DeleteIndexedKey(ctx context.Context, client redis.UniversalClient, indexKey, key string) error {
	if err := client.Del(ctx, key).Err(); err != nil {
		return err
	}

	if err := client.SRem(ctx, indexKey, key).Err(); err != nil {
		return fmt.Errorf("remove index entry: %w", err)
	}

	return nil
}

// GetClient returns the underlying redis client
func (r *Client) GetClient() redis.UniversalClient {
	return r.client
}

func redisClusterAddrsFromEnv() ([]string, error) {
	clusterNodes := strings.TrimSpace(os.Getenv("REDIS_CLUSTER_NODES"))
	if clusterNodes == "" {
		return nil, fmt.Errorf("REDIS_CLUSTER_NODES is required for Go services")
	}

	parts := strings.Split(clusterNodes, ",")
	addrs := make([]string, 0, len(parts))

	for _, part := range parts {
		addr := strings.TrimSpace(part)
		if addr != "" {
			addrs = append(addrs, addr)
		}
	}

	if len(addrs) == 0 {
		return nil, fmt.Errorf("REDIS_CLUSTER_NODES must contain at least one host:port entry")
	}

	return addrs, nil
}
