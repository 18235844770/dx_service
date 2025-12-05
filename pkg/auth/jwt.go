package auth

import (
	"errors"
	"time"

	"dx-service/internal/config"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken = errors.New("invalid token")
)

const (
	ScopeUser  = "user"
	ScopeAdmin = "admin"
)

type Claims struct {
	SubjectID int64  `json:"subjectId"`
	Scope     string `json:"scope"`
	jwt.RegisteredClaims
}

func GenerateToken(userID int64) (string, error) {
	return generateToken(userID, ScopeUser)
}

func GenerateAdminToken(adminID int64) (string, error) {
	return generateToken(adminID, ScopeAdmin)
}

func generateToken(subjectID int64, scope string) (string, error) {
	duration := time.Duration(config.GlobalConfig.JWT.Expire) * time.Hour
	claims := Claims{
		SubjectID: subjectID,
		Scope:     scope,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(duration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   scope,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.GlobalConfig.JWT.Secret))
}

func ParseToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(config.GlobalConfig.JWT.Secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
