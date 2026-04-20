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
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

type Definition struct {
	ID                string          `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Name              string          `gorm:"not null" json:"name"`
	Slug              string          `gorm:"uniqueIndex;not null" json:"slug"`
	BotType           string          `gorm:"index;not null" json:"bot_type"`
	IsActive          bool            `gorm:"default:true;index" json:"is_active"`
	Description       string          `gorm:"type:text" json:"description"`
	MatchModesJSON    json.RawMessage `gorm:"type:jsonb;default:'[]'" json:"match_modes_json"`
	BotCount          int             `gorm:"default:1" json:"bot_count"`
	TrafficWeight     int             `gorm:"default:100" json:"traffic_weight"`
	TargetingJSON     json.RawMessage `gorm:"type:jsonb;default:'{}'" json:"targeting_json"`
	ScriptJSON        json.RawMessage `gorm:"type:jsonb;default:'{}'" json:"script_json"`
	AIConfigJSON      json.RawMessage `gorm:"column:ai_config_json;type:jsonb;default:'{}'" json:"ai_config_json"`
	MessageLimit      int             `gorm:"default:20" json:"message_limit"`
	SessionTTLSeconds int             `gorm:"default:300" json:"session_ttl_seconds"`
	IdleTimeoutSecs   int             `gorm:"column:idle_timeout_seconds;default:45" json:"idle_timeout_seconds"`
	DisconnectReason  string          `json:"disconnect_reason"`
	CreatedByUsername string          `json:"created_by_username"`
	UpdatedByUsername string          `json:"updated_by_username"`
	CreatedAt         time.Time       `gorm:"autoCreateTime;index" json:"created_at"`
	UpdatedAt         time.Time       `gorm:"autoUpdateTime" json:"updated_at"`
}

func AutoMigrate(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	return db.AutoMigrate(&Definition{})
}
