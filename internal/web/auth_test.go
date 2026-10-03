package web

import (
	"testing"
)

func TestAuthManager_Authenticate(t *testing.T) {
	mgr := NewAuthManager("secret123")

	if !mgr.IsEnabled() {
		t.Errorf("expected manager to be enabled")
	}

	// Successful login
	session, err := mgr.Authenticate("127.0.0.1", "secret123")
	if err != nil || session == nil {
		t.Fatalf("expected successful login, got: %v", err)
	}

	if session.Token == "" || session.CSRFToken == "" {
		t.Errorf("expected non-empty tokens in session")
	}

	// Validate session
	retrieved, ok := mgr.ValidateSession(session.Token)
	if !ok || retrieved == nil {
		t.Errorf("expected session to be valid")
	}

	// Wrong password
	_, err = mgr.Authenticate("127.0.0.1", "wrongpass")
	if err == nil {
		t.Errorf("expected error on wrong password, got nil")
	}
}

func TestAuthManager_RateLimiting(t *testing.T) {
	mgr := NewAuthManager("mypassword")
	clientIP := "192.168.1.100"

	// 5 failed login attempts
	for i := 0; i < 5; i++ {
		_, err := mgr.Authenticate(clientIP, "bad")
		if err == nil {
			t.Fatalf("attempt %d: expected error, got nil", i)
		}
	}

	// 6th attempt should trigger rate limiting lockout
	_, err := mgr.Authenticate(clientIP, "mypassword")
	if err == nil || err.Error() != "too many failed login attempts; please try again later" {
		t.Fatalf("expected rate limit error, got: %v", err)
	}

	// Different IP should still be allowed
	sess, err := mgr.Authenticate("192.168.1.101", "mypassword")
	if err != nil || sess == nil {
		t.Fatalf("expected different IP to succeed, got: %v", err)
	}
}

func TestAuthManager_CSRF(t *testing.T) {
	mgr := NewAuthManager("password")
	session, err := mgr.Authenticate("127.0.0.1", "password")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	// Valid CSRF
	if !mgr.ValidateCSRF(session, session.CSRFToken) {
		t.Errorf("expected valid CSRF token to pass")
	}

	// Invalid CSRF
	if mgr.ValidateCSRF(session, "invalid_csrf_token") {
		t.Errorf("expected invalid CSRF token to fail")
	}

	// Empty CSRF
	if mgr.ValidateCSRF(session, "") {
		t.Errorf("expected empty CSRF token to fail")
	}
}

func TestAuthManager_RevokeSession(t *testing.T) {
	mgr := NewAuthManager("password")
	session, _ := mgr.Authenticate("127.0.0.1", "password")

	mgr.RevokeSession(session.Token)

	_, ok := mgr.ValidateSession(session.Token)
	if ok {
		t.Errorf("expected revoked session to be invalid")
	}
}

func TestAuthManager_Disabled(t *testing.T) {
	mgr := NewAuthManager("")
	if mgr.IsEnabled() {
		t.Errorf("expected empty password manager to be disabled")
	}
}
