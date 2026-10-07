package utils

import (
	"errors"
	"time"

	"ride-backend/models"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	JWTIssuer      = "ride-backend"
	AudienceApp    = "app"
	AudienceStaff  = "staff"
	DefaultAppTTL  = 72 * time.Hour
)

// GenerateAppToken creates a JWT for the "app" realm (passengers, drivers).
func GenerateAppToken(userID string, role models.Role, secret string, ttl time.Duration) (string, error) {
	if ttl == 0 {
		ttl = DefaultAppTTL
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":     JWTIssuer,
		"aud":     AudienceApp,
		"sub":     userID,
		"user_id": userID,
		"role":    string(role),
		"iat":     now.Unix(),
		"exp":     now.Add(ttl).Unix(),
		"jti":     uuid.NewString(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// GenerateToken is backwards-compatible wrapper for GenerateAppToken.
func GenerateToken(userID string, role models.Role, secret string) (string, error) {
	return GenerateAppToken(userID, role, secret, DefaultAppTTL)
}

// ParseAppToken validates an "app" realm JWT strictly.
func ParseAppToken(tokenString, secret string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(JWTIssuer),
		jwt.WithAudience(AudienceApp),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		return nil, errors.New("invalid or unauthorized token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}

// ParseToken is backwards-compatible wrapper for ParseAppToken.
func ParseToken(tokenString, secret string) (string, models.Role, error) {
	claims, err := ParseAppToken(tokenString, secret)
	if err != nil {
		return "", "", err
	}
	uid, _ := claims["sub"].(string)
	if uid == "" {
		uid, _ = claims["user_id"].(string)
	}
	rStr, _ := claims["role"].(string)
	role, err := models.ParseRole(rStr)
	if err != nil {
		return "", "", err
	}
	return uid, role, nil
}

// GenerateStaffToken creates a JWT for the "staff" realm (admin, support).
func GenerateStaffToken(staffID string, role models.StaffRole, secret string, ttl time.Duration) (string, error) {
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   JWTIssuer,
		"aud":   AudienceStaff,
		"sub":   staffID,
		"role":  string(role),
		"iat":   now.Unix(),
		"exp":   now.Add(ttl).Unix(),
		"jti":   uuid.NewString(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseStaffToken validates a "staff" realm JWT strictly.
func ParseStaffToken(tokenString, secret string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(JWTIssuer),
		jwt.WithAudience(AudienceStaff),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		return nil, errors.New("invalid or unauthorized staff token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}
