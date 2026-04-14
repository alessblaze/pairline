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

package storage

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormotel "gorm.io/plugin/opentelemetry/tracing"
)

type Database struct {
	db *gorm.DB
}

var (
	sharedDB   *Database
	dbInitOnce sync.Once
)

type DBConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// Models
type AdminAccount struct {
	ID                string    `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Username          string    `gorm:"uniqueIndex;not null" json:"username"`
	PasswordHash      string    `gorm:"not null" json:"-"`
	Role              string    `gorm:"not null;default:'moderator'" json:"role"`
	CreatedAt         time.Time `gorm:"autoCreateTime" json:"created_at"`
	CreatedByUsername string    `json:"created_by_username"`
	IsActive          bool      `gorm:"default:true" json:"is_active"`
}

type Report struct {
	ID                        string     `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ReporterSessionID         string     `gorm:"index" json:"reporter_session_id"`
	ReportedSessionID         string     `gorm:"index" json:"reported_session_id"`
	ReporterIP                string     `json:"reporter_ip"`
	ReportedIP                string     `gorm:"index" json:"reported_ip"`
	Reason                    string     `json:"reason"`
	Description               string     `json:"description"`
	ChatLog                   string     `gorm:"type:jsonb;default:'[]'" json:"chat_log"`
	Status                    string     `gorm:"default:'pending';index:idx_reports_status_created_at,priority:1" json:"status"`
	AutoModerationState       string     `gorm:"default:'pending';index" json:"auto_moderation_state"`
	AutoModerationDecision    string     `gorm:"index" json:"auto_moderation_decision"`
	AutoModerationCategories  string     `gorm:"type:jsonb;default:'[]'" json:"auto_moderation_categories"`
	AutoModerationSummary     string     `gorm:"type:text" json:"auto_moderation_summary"`
	AutoModerationError       string     `gorm:"type:text" json:"auto_moderation_error"`
	AutoModerationModel       string     `json:"auto_moderation_model"`
	AutoModerationAttempts    int        `gorm:"default:0" json:"auto_moderation_attempts"`
	AutoModerationClaimedAt   *time.Time `json:"auto_moderation_claimed_at"`
	AutoModerationCompletedAt *time.Time `json:"auto_moderation_completed_at"`
	CreatedAt                 time.Time  `gorm:"autoCreateTime;index:idx_reports_status_created_at,priority:2" json:"created_at"`
	ReviewedByUsername        string     `json:"reviewed_by_username"`
	ReviewedAt                *time.Time `json:"reviewed_at"`
}

type Ban struct {
	ID                 string     `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	SessionID          string     `gorm:"index" json:"session_id"`
	IPAddress          string     `gorm:"index" json:"ip_address"`
	Reason             string     `json:"reason"`
	BannedByUsername   string     `json:"banned_by_username"`
	CreatedAt          time.Time  `gorm:"autoCreateTime;index:idx_bans_active_created_at,priority:2" json:"created_at"`
	ExpiresAt          *time.Time `json:"expires_at"`
	IsActive           bool       `gorm:"default:true;index:idx_bans_active_created_at,priority:1" json:"is_active"`
	UnbannedAt         *time.Time `json:"unbanned_at"`
	UnbannedByUsername string     `json:"unbanned_by_username"`
}

type BannedWord struct {
	ID                string    `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Word              string    `gorm:"not null" json:"word"`
	NormalizedWord    string    `gorm:"uniqueIndex;not null" json:"normalized_word"`
	CreatedByUsername string    `json:"created_by_username"`
	CreatedAt         time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}

type AdminSetting struct {
	Key               string    `gorm:"primaryKey;size:128" json:"key"`
	Value             string    `gorm:"not null" json:"value"`
	UpdatedByUsername string    `json:"updated_by_username"`
	CreatedAt         time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt         time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

type AdminActivityLog struct {
	ID            string    `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	AdminUsername string    `gorm:"index" json:"admin_username"`
	Action        string    `json:"action"`
	Details       string    `gorm:"type:jsonb" json:"details"`
	IPAddress     string    `json:"ip_address"`
	CreatedAt     time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func NewDatabase() *Database {
	dbInitOnce.Do(func() {
		config := DBConfig{
			Host:     getEnv("POSTGRES_HOST", "localhost"),
			Port:     getEnv("POSTGRES_PORT", "5432"),
			User:     getEnv("POSTGRES_USER", "postgres"),
			Password: getEnv("POSTGRES_PASSWORD", "postgres"),
			DBName:   getEnv("POSTGRES_DB", "omegle"),
			SSLMode:  getEnv("POSTGRES_SSLMODE", "disable"),
		}

		dsn := fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
			config.Host,
			config.Port,
			config.User,
			config.Password,
			config.DBName,
			config.SSLMode,
		)

		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err != nil {
			panic(fmt.Errorf("open database: %w", err))
		}

		if err := db.Use(gormotel.NewPlugin()); err != nil {
			panic(fmt.Errorf("enable database tracing: %w", err))
		}

		sqlDB, err := db.DB()
		if err != nil {
			panic(fmt.Errorf("get sql DB: %w", err))
		}

		sqlDB.SetMaxOpenConns(getEnvAsInt("POSTGRES_MAX_OPEN_CONNS", 25))
		sqlDB.SetMaxIdleConns(getEnvAsInt("POSTGRES_MAX_IDLE_CONNS", 5))
		sqlDB.SetConnMaxLifetime(time.Duration(getEnvAsInt("POSTGRES_CONN_MAX_LIFETIME_MINUTES", 30)) * time.Minute)
		sqlDB.SetConnMaxIdleTime(time.Duration(getEnvAsInt("POSTGRES_CONN_MAX_IDLE_MINUTES", 10)) * time.Minute)

		if err := sqlDB.Ping(); err != nil {
			panic(fmt.Errorf("ping database: %w", err))
		}

		if err := db.AutoMigrate(&AdminAccount{}, &Report{}, &Ban{}, &BannedWord{}, &AdminSetting{}, &AdminActivityLog{}); err != nil {
			panic(fmt.Errorf("auto migrate database: %w", err))
		}

		if err := createRootAdmin(db); err != nil {
			panic(fmt.Errorf("create root admin: %w", err))
		}

		sharedDB = &Database{db: db}
	})

	return sharedDB
}

func (d *Database) GetDB() *gorm.DB {
	if d == nil {
		return nil
	}
	return d.db
}

func (d *Database) Close() error {
	if d == nil || d.db == nil {
		return nil
	}

	sqlDB, err := d.db.DB()
	if err != nil {
		return fmt.Errorf("get sql DB for close: %w", err)
	}

	return sqlDB.Close()
}

func createRootAdmin(db *gorm.DB) error {
	username := getEnv("ROOT_ADMIN_USERNAME", "admin")
	password := os.Getenv("ROOT_ADMIN_PASSWORD")

	return db.Transaction(func(tx *gorm.DB) error {
		var adminAccount AdminAccount
		result := tx.Where("username = ?", username).First(&adminAccount)
		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return fmt.Errorf("find root admin: %w", result.Error)
		}

		exists := result.Error == nil
		if password == "" {
			if exists {
				return nil
			}
			return fmt.Errorf("ROOT_ADMIN_PASSWORD environment variable is required to bootstrap root admin %q", username)
		}

		hash, err := HashPassword(password)
		if err != nil {
			return fmt.Errorf("hash root admin password: %w", err)
		}

		if !exists {
			admin := &AdminAccount{
				Username:     username,
				PasswordHash: hash,
				Role:         "root",
				IsActive:     true,
			}

			createResult := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "username"}},
				DoNothing: true,
			}).Create(admin)
			if createResult.Error != nil {
				return fmt.Errorf("insert root admin: %w", createResult.Error)
			}

			if createResult.RowsAffected > 0 {
				return nil
			}

			if err := tx.Where("username = ?", username).First(&adminAccount).Error; err != nil {
				return fmt.Errorf("reload root admin after conflict: %w", err)
			}
			exists = true
		}

		if exists && !CheckPasswordHash(password, adminAccount.PasswordHash) {
			if err := tx.Model(&adminAccount).Update("password_hash", hash).Error; err != nil {
				return fmt.Errorf("update root admin password hash: %w", err)
			}
		}

		return nil
	})
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(bytes), err
}

func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return defaultValue
	}

	return value
}
