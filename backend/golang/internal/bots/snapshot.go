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

package bots

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

const (
	DefinitionsSnapshotKey = "bots:definitions:snapshot"
	SettingsSnapshotKey    = "bots:settings:snapshot"
)

type SnapshotSyncer interface {
	Sync(ctx context.Context, db *gorm.DB, username string) error
}

type RedisSnapshotSyncer struct {
	redis *appredis.Client
}

type SnapshotPayload struct {
	Settings    SettingsResponse `json:"settings"`
	Definitions []Definition     `json:"definitions"`
	UpdatedAt   int64            `json:"updated_at"`
	UpdatedBy   string           `json:"updated_by_username"`
}

func NewRedisSnapshotSyncer(redisClient *appredis.Client) *RedisSnapshotSyncer {
	if redisClient == nil {
		return nil
	}
	return &RedisSnapshotSyncer{redis: redisClient}
}

func (s *RedisSnapshotSyncer) Sync(ctx context.Context, db *gorm.DB, username string) error {
	if s == nil || s.redis == nil || db == nil {
		return nil
	}

	settings, err := LoadSettings(ctx, db)
	if err != nil {
		return err
	}

	var definitions []Definition
	if err := db.WithContext(ctx).
		Where("is_active = ?", true).
		Order("updated_at DESC").
		Find(&definitions).Error; err != nil {
		return err
	}

	payload := SnapshotPayload{
		Settings:    settings,
		Definitions: definitions,
		UpdatedAt:   time.Now().UnixMilli(),
		UpdatedBy:   username,
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if err := s.redis.GetClient().Set(ctx, DefinitionsSnapshotKey, encoded, 0).Err(); err != nil {
		return err
	}
	if err := s.redis.GetClient().Set(ctx, SettingsSnapshotKey, encoded, 0).Err(); err != nil {
		return err
	}
	return s.redis.PublishBotConfigRefreshAction(ctx)
}

func isDuplicateKeyError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
