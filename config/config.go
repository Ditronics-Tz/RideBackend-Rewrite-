package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                    string
	JWTSecret               string
	StaffJWTSecret          string
	StaffAccessTTL          time.Duration
	StaffRefreshTTL         time.Duration
	StaffTOTPKey            []byte
	StaffRequire2FAForAdmin bool
	CORSAllowedOrigins      string

	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string

	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string
}

// DSN builds a Postgres connection string for the pgx/gorm driver.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.JWTSecret) == "" {
		return errors.New("JWT_SECRET is required")
	}
	if strings.TrimSpace(c.StaffJWTSecret) == "" {
		return errors.New("STAFF_JWT_SECRET is required")
	}
	if len([]byte(c.StaffJWTSecret)) < 32 {
		return errors.New("STAFF_JWT_SECRET must be at least 32 bytes")
	}
	if c.StaffJWTSecret == c.JWTSecret {
		return errors.New("STAFF_JWT_SECRET must differ from JWT_SECRET")
	}
	if strings.TrimSpace(c.DBPassword) == "" {
		return errors.New("DB_PASSWORD is required")
	}
	if len(c.StaffTOTPKey) != 32 {
		return errors.New("STAFF_TOTP_KEY must be exactly 32 bytes")
	}
	return nil
}

// NewConfigFromEnv loads and validates configuration from environment variables.
func NewConfigFromEnv() (*Config, error) {
	accessTTL, err := parseDuration(os.Getenv("STAFF_ACCESS_TTL"), "15m")
	if err != nil {
		return nil, fmt.Errorf("invalid STAFF_ACCESS_TTL: %w", err)
	}

	refreshTTL, err := parseDuration(os.Getenv("STAFF_REFRESH_TTL"), "7d")
	if err != nil {
		return nil, fmt.Errorf("invalid STAFF_REFRESH_TTL: %w", err)
	}

	totpKey, err := parseTOTPKey(os.Getenv("STAFF_TOTP_KEY"))
	if err != nil && os.Getenv("STAFF_TOTP_KEY") != "" {
		return nil, err
	}

	cfg := &Config{
		Port:                    getEnv("PORT", "3000"),
		JWTSecret:               os.Getenv("JWT_SECRET"),
		StaffJWTSecret:          os.Getenv("STAFF_JWT_SECRET"),
		StaffAccessTTL:          accessTTL,
		StaffRefreshTTL:         refreshTTL,
		StaffTOTPKey:            totpKey,
		StaffRequire2FAForAdmin: parseBool(os.Getenv("STAFF_REQUIRE_2FA_FOR_ADMIN"), true),
		CORSAllowedOrigins:      getEnv("CORS_ALLOWED_ORIGINS", ""),

		GoogleClientID:     getEnv("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: getEnv("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURL:  getEnv("GOOGLE_REDIRECT_URL", ""),

		DBHost:     getEnv("DB_HOST", "127.0.0.1"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     getEnv("DB_NAME", "ride_db"),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func LoadConfig() *Config {
	if err := godotenv.Load(); err != nil {
		if err2 := godotenv.Load("../.env"); err2 != nil {
			_ = godotenv.Load("../../.env")
		}
	}

	cfg, err := NewConfigFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	return cfg
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func parseDuration(val, fallback string) (time.Duration, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		val = fallback
	}
	if strings.HasSuffix(val, "d") {
		daysStr := strings.TrimSuffix(val, "d")
		days, err := strconv.Atoi(daysStr)
		if err != nil {
			return 0, fmt.Errorf("invalid day duration %q: %w", val, err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(val)
}

func parseBool(val string, fallback bool) bool {
	if strings.TrimSpace(val) == "" {
		return fallback
	}
	val = strings.ToLower(strings.TrimSpace(val))
	return val == "1" || val == "true" || val == "yes"
}

func parseTOTPKey(val string) ([]byte, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil, errors.New("STAFF_TOTP_KEY is required")
	}
	if len(val) == 64 {
		b, err := hex.DecodeString(val)
		if err == nil && len(b) == 32 {
			return b, nil
		}
	}
	b := []byte(val)
	if len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("STAFF_TOTP_KEY must be exactly 32 bytes (or 64 hex characters)")
}
