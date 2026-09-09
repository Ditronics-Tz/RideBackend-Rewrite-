package utils

import (
	"errors"
	"time"

	"ride-backend/models"

	"github.com/golang-jwt/jwt/v5"
)

// GenerateToken creates a JWT carrying user id + role (expires in 72h).
func GenerateToken(userID string, role models.Role, secret string) (string, error) {
	claims := jwt.MapClaims{
		"user_id": userID,
		"role":    string(role),
		"exp":     time.Now().Add(time.Hour * 72).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseToken validates a token and returns user id + role.
func ParseToken(tokenString, secret string) (userID string, role models.Role, err error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil || !token.Valid {
		return "", "", errors.New("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", "", errors.New("invalid token claims")
	}
	uid, _ := claims["user_id"].(string)
	r, _ := claims["role"].(string)
	if uid == "" {
		return "", "", errors.New("invalid token claims")
	}
	return uid, models.Role(r), nil
}
