package web

import (
	"crypto/tls"
	"net/http/httptest"
	"os"
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

func TestAuthManager_CookieSecure(t *testing.T) {
	// Case 1: default/auto over plain HTTP -> secure false (TrueNAS LAN)
	_ = os.Unsetenv("DASHBOARD_COOKIE_SECURE")
	reqHTTP := httptest.NewRequest("GET", "http://truenas:8080/", nil)
	c := CreateSessionCookie(reqHTTP, "tok1", 3600)
	if c.Secure {
		t.Errorf("expected Secure=false for plain HTTP under auto mode")
	}

	// Case 2: default/auto over TLS -> secure true
	reqTLS := httptest.NewRequest("GET", "https://truenas:8080/", nil)
	reqTLS.TLS = &tls.ConnectionState{}
	c = CreateSessionCookie(reqTLS, "tok2", 3600)
	if !c.Secure {
		t.Errorf("expected Secure=true for TLS under auto mode")
	}

	// Case 3: default/auto behind HTTPS reverse proxy (X-Forwarded-Proto) -> secure true
	reqProxy := httptest.NewRequest("GET", "http://truenas:8080/", nil)
	reqProxy.Header.Set("X-Forwarded-Proto", "https")
	c = CreateSessionCookie(reqProxy, "tok3", 3600)
	if !c.Secure {
		t.Errorf("expected Secure=true with X-Forwarded-Proto: https under auto mode")
	}

	// Case 4: explicit DASHBOARD_COOKIE_SECURE=true -> always true even over HTTP
	t.Setenv("DASHBOARD_COOKIE_SECURE", "true")
	c = CreateSessionCookie(reqHTTP, "tok4", 3600)
	if !c.Secure {
		t.Errorf("expected Secure=true when DASHBOARD_COOKIE_SECURE=true")
	}

	// Case 5: explicit DASHBOARD_COOKIE_SECURE=false -> always false even over TLS
	t.Setenv("DASHBOARD_COOKIE_SECURE", "false")
	c = CreateSessionCookie(reqTLS, "tok5", 3600)
	if c.Secure {
		t.Errorf("expected Secure=false when DASHBOARD_COOKIE_SECURE=false")
	}
}

