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
	StorageDriver     string
	R2AccountID       string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2PublicBucket    string
	R2PrivateBucket   string
	R2PublicBaseURL   string
	AppEnv            string
	DeviceHashPepper  string

	PaymentProviderName          string
	PaymentGatewayURL            string
	PaymentGatewayKey            string
	PaymentWebhookSecret         string
	PaymentWebhookPreviousSecret string
	PaymentEncryptionKey         []byte
	DriverMaxNegativeBalance     int
	PayoutMin                    int
	PayoutAutoApproveMax         int
	RefundSupportLimit           int

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

	DBHost                   string
	DBPort                   string
	DBUser                   string
	DBPassword               string
	DBName                   string
	DBSSLMode                string
	DeletionGraceDays        int
	ExportTTLHours           int
	ExportDir                string
	FinancialRetentionYears  int
	RunBackgroundJobs        bool
	ReportFlagThreshold      int
	ReportFlagWindowDays     int
	SOSAckSLASeconds         int
	SOSOncallPhones          string
	SOSLocationRetentionDays int
	SOSFalseAlarmThreshold   int
	PublicBaseURL            string
}

// DSN builds a Postgres connection string for the pgx/gorm driver.
func (c *Config) DSN() string {
	quote := func(value string) string {
		return "'" + strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "'", "\\'") + "'"
	}
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s connect_timeout=10",
		quote(c.DBHost), quote(c.DBPort), quote(c.DBUser), quote(c.DBPassword), quote(c.DBName), quote(c.DBSSLMode),
	)
}

func (c *Config) Validate() error {
	if err := ValidateOrigins(c.CORSAllowedOrigins); err != nil {
		return err
	}
	if c.AppEnv == "production" && c.DBSSLMode != "require" && c.DBSSLMode != "verify-ca" && c.DBSSLMode != "verify-full" {
		return errors.New("DB_SSLMODE must require TLS in production")
	}
	if c.StorageDriver != "" && c.StorageDriver != "local" && c.StorageDriver != "r2" {
		return errors.New("STORAGE_DRIVER must be local or r2")
	}
	if c.StorageDriver == "r2" {
		for _, setting := range []struct{ name, value string }{
			{"R2_ACCOUNT_ID", c.R2AccountID}, {"R2_ACCESS_KEY_ID", c.R2AccessKeyID}, {"R2_SECRET_ACCESS_KEY", c.R2SecretAccessKey}, {"R2_PUBLIC_BUCKET", c.R2PublicBucket}, {"R2_PRIVATE_BUCKET", c.R2PrivateBucket}, {"R2_PUBLIC_BASE_URL", c.R2PublicBaseURL},
		} {
			if strings.TrimSpace(setting.value) == "" {
				return fmt.Errorf("%s is required for R2 storage", setting.name)
			}
		}
		if c.R2PublicBucket == c.R2PrivateBucket {
			return errors.New("R2 public and private buckets must differ")
		}
	}
	if c.AppEnv != "" && c.AppEnv != "development" && c.AppEnv != "production" {
		return errors.New("APP_ENV must be development or production")
	}
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
	if c.AppEnv == "production" {
		for _, secret := range []struct{ name, value string }{
			{"JWT_SECRET", c.JWTSecret}, {"STAFF_JWT_SECRET", c.StaffJWTSecret},
		} {
			if len([]byte(secret.value)) < 32 {
				return fmt.Errorf("%s must be at least 32 bytes in production", secret.name)
			}
			lower := strings.ToLower(secret.value)
			for _, marker := range []string{"example", "changeme", "change_me", "change-me", "replace", "placeholder", "test_", "test-", "your-secret", "your_secret"} {
				if strings.Contains(lower, marker) {
					return fmt.Errorf("%s must not use an example value in production", secret.name)
				}
			}
		}
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
		StorageDriver:                getEnv("STORAGE_DRIVER", "local"),
		R2AccountID:                  getEnv("R2_ACCOUNT_ID", ""),
		R2AccessKeyID:                getEnv("R2_ACCESS_KEY_ID", ""),
		R2SecretAccessKey:            getEnv("R2_SECRET_ACCESS_KEY", ""),
		R2PublicBucket:               getEnv("R2_PUBLIC_BUCKET", ""),
		R2PrivateBucket:              getEnv("R2_PRIVATE_BUCKET", ""),
		R2PublicBaseURL:              getEnv("R2_PUBLIC_BASE_URL", ""),
		AppEnv:                       getEnv("APP_ENV", "development"),
		DeviceHashPepper:             getEnv("DEVICE_HASH_PEPPER", ""),
		PaymentProviderName:          getEnv("PAYMENT_PROVIDER", "mock"),
		PaymentGatewayURL:            getEnv("PAYMENT_GATEWAY_URL", ""),
		PaymentGatewayKey:            getEnv("PAYMENT_GATEWAY_KEY", ""),
		PaymentWebhookSecret:         getEnv("PAYMENT_WEBHOOK_SECRET", ""),
		PaymentWebhookPreviousSecret: getEnv("PAYMENT_WEBHOOK_PREVIOUS_SECRET", ""),
		PaymentEncryptionKey:         []byte(getEnv("PAYMENT_ENCRYPTION_KEY", "")),
		DriverMaxNegativeBalance:     getEnvInt("DRIVER_MAX_NEGATIVE_BALANCE", 10000),
		PayoutMin:                    getEnvInt("PAYOUT_MIN", 1000),
		PayoutAutoApproveMax:         getEnvInt("PAYOUT_AUTO_APPROVE_MAX", 10000),
		RefundSupportLimit:           getEnvInt("REFUND_SUPPORT_LIMIT", 5000),

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

		DBHost:                   getEnv("DB_HOST", "127.0.0.1"),
		DBPort:                   getEnv("DB_PORT", "5432"),
		DBUser:                   getEnv("DB_USER", "postgres"),
		DBPassword:               os.Getenv("DB_PASSWORD"),
		DBName:                   getEnv("DB_NAME", "ride_db"),
		DBSSLMode:                getEnv("DB_SSLMODE", defaultSSLMode()),
		DeletionGraceDays:        getEnvInt("DELETION_GRACE_DAYS", 14),
		ExportTTLHours:           getEnvInt("EXPORT_TTL_HOURS", 168),
		ExportDir:                getEnv("EXPORT_DIR", "./uploads/private/exports"),
		FinancialRetentionYears:  getEnvInt("FINANCIAL_RETENTION_YEARS", 0),
		RunBackgroundJobs:        parseBool(os.Getenv("RUN_BACKGROUND_JOBS"), false),
		ReportFlagThreshold:      getEnvInt("REPORT_FLAG_THRESHOLD", 3),
		ReportFlagWindowDays:     getEnvInt("REPORT_FLAG_WINDOW_DAYS", 30),
		SOSAckSLASeconds:         getEnvInt("SOS_ACK_SLA_SECONDS", 60),
		SOSOncallPhones:          getEnv("SOS_ONCALL_PHONES", ""),
		SOSLocationRetentionDays: getEnvInt("SOS_LOCATION_RETENTION_DAYS", 30),
		SOSFalseAlarmThreshold:   getEnvInt("SOS_FALSE_ALARM_THRESHOLD", 3),
		PublicBaseURL:            getEnv("PUBLIC_BASE_URL", "http://localhost:3000"),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func getEnvInt(key string, fallback int) int {
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil && value > 0 {
		return value
	}
	return fallback
}

func defaultSSLMode() string {
	if getEnv("APP_ENV", "development") == "production" {
		return "require"
	}
	return "disable"
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
