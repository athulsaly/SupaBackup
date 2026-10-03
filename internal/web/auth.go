package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	SessionCookieName = "supabackup_session"
	SessionDuration   = 24 * time.Hour
	MaxFailedLogins   = 5
	RateLimitWindow   = 1 * time.Minute
)

// Session represents an authenticated user session.
type Session struct {
	Token     string
	CSRFToken string
	ExpiresAt time.Time
}

// AuthManager manages dashboard authentication, session tokens, CSRF protection, and rate limiting.
type AuthManager struct {
	password string

	mu           sync.RWMutex
	sessions     map[string]Session
	failedLogins map[string][]time.Time
}

// NewAuthManager initializes an AuthManager with the provided dashboard password.
func NewAuthManager(password string) *AuthManager {
	return &AuthManager{
		password:     password,
		sessions:     make(map[string]Session),
		failedLogins: make(map[string][]time.Time),
	}
}

// IsEnabled returns true if a non-empty password is configured.
func (a *AuthManager) IsEnabled() bool {
	return a.password != ""
}

// Authenticate verifies the given password against the configured password using constant-time comparison.
func (a *AuthManager) Authenticate(clientIP, password string) (*Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	// Clean expired rate limit records for this IP
	if attempts, ok := a.failedLogins[clientIP]; ok {
		var valid []time.Time
		for _, t := range attempts {
			if now.Sub(t) < RateLimitWindow {
				valid = append(valid, t)
			}
		}
		a.failedLogins[clientIP] = valid
		if len(valid) >= MaxFailedLogins {
			return nil, fmt.Errorf("too many failed login attempts; please try again later")
		}
	}

	// Constant-time comparison
	match := subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) == 1
	if !match {
		a.failedLogins[clientIP] = append(a.failedLogins[clientIP], now)
		return nil, fmt.Errorf("invalid password")
	}

	// Successful login: clear failed attempts for this IP
	delete(a.failedLogins, clientIP)

	// Generate secure session token and CSRF token
	sessionToken := generateRandomToken(32)
	csrfToken := generateRandomToken(32)

	session := Session{
		Token:     sessionToken,
		CSRFToken: csrfToken,
		ExpiresAt: now.Add(SessionDuration),
	}

	a.sessions[sessionToken] = session
	return &session, nil
}

// ValidateSession checks whether the session token exists and has not expired.
func (a *AuthManager) ValidateSession(token string) (*Session, bool) {
	if token == "" {
		return nil, false
	}

	a.mu.RLock()
	session, exists := a.sessions[token]
	a.mu.RUnlock()

	if !exists || time.Now().After(session.ExpiresAt) {
		if exists {
			a.RevokeSession(token)
		}
		return nil, false
	}

	return &session, true
}

// RevokeSession invalidates a session token.
func (a *AuthManager) RevokeSession(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}

// ValidateCSRF checks if the provided CSRF token matches the session's CSRF token using constant-time comparison.
func (a *AuthManager) ValidateCSRF(session *Session, token string) bool {
	if session == nil || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(session.CSRFToken)) == 1
}

// CreateSessionCookie returns an HttpOnly, SameSite=Strict session cookie.
func CreateSessionCookie(token string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	}
}

func generateRandomToken(bytesLen int) string {
	b := make([]byte, bytesLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
