package middleware

import (
	"net/http"
	"strings"

	pkgAuth "dx-service/pkg/auth"

	"github.com/gin-gonic/gin"
)

const (
	ContextUserIDKey  = "userID"
	ContextAdminIDKey = "adminID"
)

func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header"})
			return
		}

		claims, err := pkgAuth.ParseToken(parts[1])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		if claims.Scope != pkgAuth.ScopeUser {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token scope"})
			return
		}

		c.Set(ContextUserIDKey, claims.SubjectID)
		c.Next()
	}
}

func AdminAuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header"})
			return
		}

		claims, err := pkgAuth.ParseToken(parts[1])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		if claims.Scope != pkgAuth.ScopeAdmin {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token scope"})
			return
		}

		c.Set(ContextAdminIDKey, claims.SubjectID)
		c.Next()
	}
}
