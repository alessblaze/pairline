package moderation

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/storage"
	"gorm.io/gorm"
)

type CreateBanParams struct {
	SessionID        string
	IPAddress        string
	Reason           string
	BannedByUsername string
	ExpiresAt        *time.Time
}

type CreateBanResult struct {
	Ban           storage.Ban
	AlreadyBanned bool
	IPAddressUsed string
}

type DeleteBanResult struct {
	Ban                 storage.Ban
	RemainingSessionBan *storage.Ban
	RemainingIPBan      *storage.Ban
}

var (
	ErrMissingBanTarget = errors.New("missing ban target")
	ErrPrivateIPAddress = errors.New("cannot ban internal/local IP address")
)

func CreateOrRefreshBan(ctx context.Context, db *gorm.DB, redisClient *appredis.Client, params CreateBanParams) (CreateBanResult, error) {
	sessionID := strings.TrimSpace(params.SessionID)
	ipAddress, ipAllowed := sanitizeIPAddress(params.IPAddress)
	if sessionID == "" && ipAddress == "" {
		if strings.TrimSpace(params.IPAddress) != "" && !ipAllowed {
			return CreateBanResult{}, ErrPrivateIPAddress
		}
		return CreateBanResult{}, ErrMissingBanTarget
	}

	now := time.Now()
	var ban storage.Ban
	alreadyBanned := false

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockBanTargets(tx, sessionID, ipAddress); err != nil {
			return err
		}

		lookup := activeBanLookup(tx, sessionID, ipAddress, now)
		if err := lookup.Order("created_at DESC").First(&ban).Error; err == nil {
			alreadyBanned = true
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		ban = storage.Ban{
			SessionID:        sessionID,
			IPAddress:        ipAddress,
			Reason:           strings.TrimSpace(params.Reason),
			BannedByUsername: strings.TrimSpace(params.BannedByUsername),
			CreatedAt:        now,
			ExpiresAt:        params.ExpiresAt,
			IsActive:         true,
		}

		return tx.Create(&ban).Error
	})
	if err != nil {
		return CreateBanResult{}, err
	}

	if redisClient != nil {
		if err := propagateBan(ctx, redisClient, ban); err != nil {
			return CreateBanResult{
				Ban:           ban,
				AlreadyBanned: alreadyBanned,
				IPAddressUsed: ipAddress,
			}, err
		}
	}

	return CreateBanResult{
		Ban:           ban,
		AlreadyBanned: alreadyBanned,
		IPAddressUsed: ipAddress,
	}, nil
}

func DeleteBan(ctx context.Context, db *gorm.DB, redisClient *appredis.Client, banIdentifier, username string) (DeleteBanResult, error) {
	now := time.Now()
	result := DeleteBanResult{}

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		banIdentifier = strings.TrimSpace(banIdentifier)

		err := tx.Where("id = ? AND is_active = ?", banIdentifier, true).First(&result.Ban).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = tx.Where("session_id = ? AND is_active = ?", banIdentifier, true).
				Order("created_at DESC").
				First(&result.Ban).Error
		}
		if err != nil {
			return err
		}

		if err := lockBanTargets(tx, result.Ban.SessionID, result.Ban.IPAddress); err != nil {
			return err
		}

		if err := tx.Model(&storage.Ban{}).
			Where("id = ? AND is_active = ?", result.Ban.ID, true).
			Updates(map[string]any{
				"is_active":            false,
				"unbanned_at":          now,
				"unbanned_by_username": username,
			}).Error; err != nil {
			return err
		}

		if result.Ban.SessionID != "" {
			activeBan, err := latestActiveBan(tx, "session_id", result.Ban.SessionID, now)
			if err != nil {
				return err
			}
			result.RemainingSessionBan = activeBan
		}

		if result.Ban.IPAddress != "" {
			activeBan, err := latestActiveBan(tx, "ip_address", result.Ban.IPAddress, now)
			if err != nil {
				return err
			}
			result.RemainingIPBan = activeBan
		}

		return nil
	})
	if err != nil {
		return DeleteBanResult{}, err
	}

	if redisClient != nil {
		if err := propagateUnban(ctx, redisClient, result); err != nil {
			return result, err
		}
	}

	return result, nil
}

func sanitizeIPAddress(raw string) (string, bool) {
	ipAddress := normalizeIP(raw)
	if ipAddress == "" {
		return "", true
	}
	if isPrivateOrLocalIP(ipAddress) {
		return "", false
	}
	return ipAddress, true
}

func isPrivateOrLocalIP(raw string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return true
	}

	addr = addr.Unmap()

	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return true
	}

	cgnatPrefix := netip.MustParsePrefix("100.64.0.0/10")
	return cgnatPrefix.Contains(addr)
}

func normalizeIP(raw string) string {
	if raw == "" {
		return ""
	}

	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}

	return addr.Unmap().String()
}

func lockBanTargets(tx *gorm.DB, sessionID, ipAddress string) error {
	keys := make([]string, 0, 2)
	if sessionID != "" {
		keys = append(keys, appredis.BanSessionKey(sessionID))
	}
	if ipAddress != "" {
		keys = append(keys, appredis.BanIPKey(ipAddress))
	}
	sort.Strings(keys)

	for _, key := range keys {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", key).Error; err != nil {
			return err
		}
	}

	return nil
}

func activeBanLookup(tx *gorm.DB, sessionID, ipAddress string, now time.Time) *gorm.DB {
	lookup := tx.Where("is_active = ? AND (expires_at IS NULL OR expires_at > ?)", true, now)

	switch {
	case sessionID != "" && ipAddress != "":
		return lookup.Where("(session_id = ? OR ip_address = ?)", sessionID, ipAddress)
	case sessionID != "":
		return lookup.Where("session_id = ?", sessionID)
	default:
		return lookup.Where("ip_address = ?", ipAddress)
	}
}

func latestActiveBan(tx *gorm.DB, column string, value string, now time.Time) (*storage.Ban, error) {
	switch column {
	case "session_id", "ip_address":
	default:
		return nil, errors.New("invalid ban lookup column")
	}

	var ban storage.Ban
	err := tx.Where(column+" = ? AND is_active = ? AND (expires_at IS NULL OR expires_at > ?)", value, true, now).
		Order("created_at DESC").
		First(&ban).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ban, nil
}

func propagateBan(ctx context.Context, redisClient *appredis.Client, ban storage.Ban) error {
	if redisClient == nil {
		return nil
	}

	var propagationErr error
	ttl := redisBanTTL(ban)

	if ban.SessionID != "" {
		if err := appredis.SetIndexedValue(
			ctx,
			redisClient.GetClient(),
			appredis.BanIndexKey(),
			appredis.BanSessionKey(ban.SessionID),
			ban.Reason,
			ttl,
		); err != nil {
			propagationErr = errors.Join(propagationErr, fmt.Errorf("store session ban in redis: %w", err))
		}

		if err := redisClient.PublishBanAction(ctx, ban.SessionID, ban.IPAddress, ban.Reason); err != nil {
			propagationErr = errors.Join(propagationErr, fmt.Errorf("publish session ban action: %w", err))
		}
	}

	if ban.IPAddress != "" {
		if err := appredis.SetIndexedValue(
			ctx,
			redisClient.GetClient(),
			appredis.BanIndexKey(),
			appredis.BanIPKey(ban.IPAddress),
			ban.Reason,
			ttl,
		); err != nil {
			propagationErr = errors.Join(propagationErr, fmt.Errorf("store ip ban in redis: %w", err))
		}

		if err := redisClient.PublishBanIPAction(ctx, ban.IPAddress, ban.Reason); err != nil {
			propagationErr = errors.Join(propagationErr, fmt.Errorf("publish ip ban action: %w", err))
		}
	}

	return propagationErr
}

func propagateUnban(ctx context.Context, redisClient *appredis.Client, result DeleteBanResult) error {
	if redisClient == nil {
		return nil
	}

	var propagationErr error

	if result.Ban.SessionID != "" {
		if result.RemainingSessionBan != nil {
			if err := appredis.SetIndexedValue(
				ctx,
				redisClient.GetClient(),
				appredis.BanIndexKey(),
				appredis.BanSessionKey(result.Ban.SessionID),
				result.RemainingSessionBan.Reason,
				redisBanTTL(*result.RemainingSessionBan),
			); err != nil {
				propagationErr = errors.Join(propagationErr, fmt.Errorf("refresh session ban in redis: %w", err))
			}
		} else {
			if err := appredis.DeleteIndexedKey(
				ctx,
				redisClient.GetClient(),
				appredis.BanIndexKey(),
				appredis.BanSessionKey(result.Ban.SessionID),
			); err != nil {
				propagationErr = errors.Join(propagationErr, fmt.Errorf("delete session ban from redis: %w", err))
			}

			if err := redisClient.PublishUnbanAction(ctx, result.Ban.SessionID, result.Ban.IPAddress); err != nil {
				propagationErr = errors.Join(propagationErr, fmt.Errorf("publish session unban action: %w", err))
			}
		}
	}

	if result.Ban.IPAddress != "" {
		if result.RemainingIPBan != nil {
			if err := appredis.SetIndexedValue(
				ctx,
				redisClient.GetClient(),
				appredis.BanIndexKey(),
				appredis.BanIPKey(result.Ban.IPAddress),
				result.RemainingIPBan.Reason,
				redisBanTTL(*result.RemainingIPBan),
			); err != nil {
				propagationErr = errors.Join(propagationErr, fmt.Errorf("refresh ip ban in redis: %w", err))
			}
		} else {
			if err := appredis.DeleteIndexedKey(
				ctx,
				redisClient.GetClient(),
				appredis.BanIndexKey(),
				appredis.BanIPKey(result.Ban.IPAddress),
			); err != nil {
				propagationErr = errors.Join(propagationErr, fmt.Errorf("delete ip ban from redis: %w", err))
			}

			if err := redisClient.PublishUnbanIPAction(ctx, result.Ban.IPAddress); err != nil {
				propagationErr = errors.Join(propagationErr, fmt.Errorf("publish ip unban action: %w", err))
			}
		}
	}

	return propagationErr
}

func redisBanTTL(ban storage.Ban) time.Duration {
	if ban.ExpiresAt == nil {
		return 0
	}

	ttl := time.Until(*ban.ExpiresAt)
	if ttl <= 0 {
		return time.Second
	}

	return ttl
}
