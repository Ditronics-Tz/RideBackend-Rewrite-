package utils_test

import (
	"testing"
	"time"

	"ride-backend/models"
	"ride-backend/utils"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTRealmsAndValidation(t *testing.T) {
	appSecret := "app_realm_secret_key_123456789012"
	staffSecret := "staff_realm_secret_key_1234567890123456"

	t.Run("App token carries correct claims (iss=ride-backend, aud=app)", func(t *testing.T) {
		token, err := utils.GenerateAppToken("u123", models.RoleDriver, appSecret, 1*time.Hour)
		if err != nil {
			t.Fatalf("GenerateAppToken failed: %v", err)
		}

		claims, err := utils.ParseAppToken(token, appSecret)
		if err != nil {
			t.Fatalf("ParseAppToken failed: %v", err)
		}

		if claims["iss"] != "ride-backend" {
			t.Errorf("Expected iss 'ride-backend', got %v", claims["iss"])
		}
		if claims["aud"] != "app" {
			t.Errorf("Expected aud 'app', got %v", claims["aud"])
		}
		if claims["sub"] != "u123" {
			t.Errorf("Expected sub 'u123', got %v", claims["sub"])
		}
		if claims["role"] != string(models.RoleDriver) {
			t.Errorf("Expected role 'driver', got %v", claims["role"])
		}
		if claims["exp"] == nil {
			t.Errorf("Expected exp claim")
		}
	})

	t.Run("Staff token carries correct claims (iss=ride-backend, aud=staff, sub, role, jti)", func(t *testing.T) {
		token, err := utils.GenerateStaffToken("s456", models.StaffRoleAdmin, staffSecret, 15*time.Minute)
		if err != nil {
			t.Fatalf("GenerateStaffToken failed: %v", err)
		}

		claims, err := utils.ParseStaffToken(token, staffSecret)
		if err != nil {
			t.Fatalf("ParseStaffToken failed: %v", err)
		}

		if claims["iss"] != "ride-backend" {
			t.Errorf("Expected iss 'ride-backend', got %v", claims["iss"])
		}
		if claims["aud"] != "staff" {
			t.Errorf("Expected aud 'staff', got %v", claims["aud"])
		}
		if claims["sub"] != "s456" {
			t.Errorf("Expected sub 's456', got %v", claims["sub"])
		}
		if claims["role"] != string(models.StaffRoleAdmin) {
			t.Errorf("Expected role 'admin', got %v", claims["role"])
		}
		if claims["jti"] == nil || claims["jti"] == "" {
			t.Errorf("Expected non-empty jti claim")
		}
		if claims["exp"] == nil {
			t.Errorf("Expected exp claim")
		}
	})

	t.Run("Cross-realm: staff token rejected on app parser", func(t *testing.T) {
		staffToken, _ := utils.GenerateStaffToken("s456", models.StaffRoleAdmin, staffSecret, 15*time.Minute)

		// Even if app parser is given staffSecret or appSecret, audience 'staff' != 'app'
		_, err1 := utils.ParseAppToken(staffToken, appSecret)
		if err1 == nil {
			t.Fatalf("Expected staff token to be rejected by ParseAppToken (wrong secret/aud)")
		}

		_, err2 := utils.ParseAppToken(staffToken, staffSecret)
		if err2 == nil {
			t.Fatalf("Expected staff token to be rejected by ParseAppToken due to aud=staff")
		}
	})

	t.Run("Cross-realm: app token rejected on staff parser", func(t *testing.T) {
		appToken, _ := utils.GenerateAppToken("u123", models.RoleDriver, appSecret, 1*time.Hour)

		_, err1 := utils.ParseStaffToken(appToken, staffSecret)
		if err1 == nil {
			t.Fatalf("Expected app token to be rejected by ParseStaffToken (wrong secret/aud)")
		}

		_, err2 := utils.ParseStaffToken(appToken, appSecret)
		if err2 == nil {
			t.Fatalf("Expected app token to be rejected by ParseStaffToken due to aud=app")
		}
	})

	t.Run("Wrong audience is rejected", func(t *testing.T) {
		claims := jwt.MapClaims{
			"iss":  "ride-backend",
			"aud":  "external_client",
			"sub":  "u123",
			"role": "passenger",
			"exp":  time.Now().Add(1 * time.Hour).Unix(),
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		signed, _ := tok.SignedString([]byte(appSecret))

		if _, err := utils.ParseAppToken(signed, appSecret); err == nil {
			t.Fatal("Expected error for wrong audience")
		}
	})

	t.Run("Wrong issuer is rejected", func(t *testing.T) {
		claims := jwt.MapClaims{
			"iss":  "other-service",
			"aud":  "app",
			"sub":  "u123",
			"role": "passenger",
			"exp":  time.Now().Add(1 * time.Hour).Unix(),
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		signed, _ := tok.SignedString([]byte(appSecret))

		if _, err := utils.ParseAppToken(signed, appSecret); err == nil {
			t.Fatal("Expected error for wrong issuer")
		}
	})

	t.Run("Expired token is rejected", func(t *testing.T) {
		claims := jwt.MapClaims{
			"iss":  "ride-backend",
			"aud":  "app",
			"sub":  "u123",
			"role": "passenger",
			"exp":  time.Now().Add(-10 * time.Minute).Unix(),
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		signed, _ := tok.SignedString([]byte(appSecret))

		if _, err := utils.ParseAppToken(signed, appSecret); err == nil {
			t.Fatal("Expected error for expired token")
		}
	})

	t.Run("Token without exp is rejected (WithExpirationRequired)", func(t *testing.T) {
		claims := jwt.MapClaims{
			"iss":  "ride-backend",
			"aud":  "app",
			"sub":  "u123",
			"role": "passenger",
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		signed, _ := tok.SignedString([]byte(appSecret))

		if _, err := utils.ParseAppToken(signed, appSecret); err == nil {
			t.Fatal("Expected error for token missing exp")
		}
	})

	t.Run("alg=none is rejected", func(t *testing.T) {
		claims := jwt.MapClaims{
			"iss":  "ride-backend",
			"aud":  "app",
			"sub":  "u123",
			"role": "passenger",
			"exp":  time.Now().Add(1 * time.Hour).Unix(),
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
		signed, _ := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)

		if _, err := utils.ParseAppToken(signed, appSecret); err == nil {
			t.Fatal("Expected error for alg=none token")
		}
	})
}
