package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supabackup/internal/config"
	"supabackup/internal/engine"
	"supabackup/internal/health"
	"supabackup/internal/scheduler"
)

type dummyEngine struct{}

func (d *dummyEngine) Backup(ctx context.Context, job config.JobConfig, targetPath string) error {
	return os.WriteFile(targetPath, []byte("backup-data"), 0644)
}

func (d *dummyEngine) Verify(ctx context.Context, targetPath string) error {
	return nil
}

func setupTestServer(t *testing.T, password string, isWritable bool) (*ServerContext, http.Handler, string, string) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	backupDir := filepath.Join(tempDir, "backups")
	statusFile := filepath.Join(tempDir, "status.json")

	yamlContent := `
jobs:
  - name: "test-job"
    engine: "postgres"
    schedule: "0 2 * * *"
    retention_days: 14
    url_env: "TEST_DB_URL"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	t.Setenv("TEST_DB_URL", "postgresql://user:secret@localhost:5432/db")

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	cfg.BackupDir = backupDir
	cfg.StatusFile = statusFile

	tracker, err := health.NewTracker(statusFile, cfg.Jobs)
	if err != nil {
		t.Fatalf("failed to create tracker: %v", err)
	}

	resolver := func(name string) (engine.Engine, error) {
		return &dummyEngine{}, nil
	}

	sched := scheduler.NewScheduler(cfg, tracker, nil, resolver)
	logBuf := health.NewLogBuffer(50)
	sched.SetLogBuffer(logBuf)

	authMgr := NewAuthManager(password)

	sc := &ServerContext{
		StartTime:  time.Now(),
		Config:     cfg,
		Tracker:    tracker,
		Scheduler:  sched,
		LogBuffer:  logBuf,
		Auth:       authMgr,
		BackupDir:  backupDir,
		IsWritable: isWritable,
	}

	handler := NewRouter(sc)
	return sc, handler, configPath, backupDir
}

func TestRoutes_Healthz(t *testing.T) {
	_, handler, _, _ := setupTestServer(t, "pass123", false)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse json: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", resp["status"])
	}
}

func TestRoutes_AuthFlow(t *testing.T) {
	_, handler, _, _ := setupTestServer(t, "mypassword", false)

	// 1. Unauthenticated GET / should redirect to /login
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Errorf("expected redirect to /login, got %d (loc: %s)", w.Code, w.Header().Get("Location"))
	}

	// 2. Unauthenticated GET /api/jobs should return 401
	req = httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", w.Code)
	}

	// 3. POST /login with wrong password
	form := url.Values{}
	form.Set("password", "wrongpass")
	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad login, got %d", w.Code)
	}

	// 4. POST /login with correct password
	form.Set("password", "mypassword")
	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for valid login, got %d", w.Code)
	}

	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("expected session cookie to be set")
	}

	var loginResp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &loginResp)
	csrfToken := loginResp["csrf_token"]
	if csrfToken == "" {
		t.Fatalf("expected non-empty csrf_token in login response")
	}

	// 5. Authenticated GET /api/jobs
	req = httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /api/jobs, got %d", w.Code)
	}

	var jobs []JobView
	if err := json.Unmarshal(w.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("failed to parse jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Name != "test-job" {
		t.Errorf("unexpected jobs response: %+v", jobs)
	}
}

func TestRoutes_RunJobAndCSRF(t *testing.T) {
	sc, handler, _, _ := setupTestServer(t, "mypassword", false)
	sess, _ := sc.Auth.Authenticate("127.0.0.1", "mypassword")
	cookie := CreateSessionCookie(nil, sess.Token, 3600)

	// POST /api/jobs/test-job/run without CSRF -> 403 Forbidden
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/test-job/run", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden without CSRF, got %d", w.Code)
	}

	// POST /api/jobs/test-job/run with valid CSRF -> 202 Accepted
	req = httptest.NewRequest(http.MethodPost, "/api/jobs/test-job/run", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", sess.CSRFToken)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202 Accepted, got %d", w.Code)
	}
}

func TestRoutes_PathTraversalProtection(t *testing.T) {
	sc, handler, _, backupDir := setupTestServer(t, "pass", false)
	sess, _ := sc.Auth.Authenticate("127.0.0.1", "pass")
	cookie := CreateSessionCookie(nil, sess.Token, 3600)

	// Create valid backup file in job folder
	jobDir := filepath.Join(backupDir, "test-job")
	_ = os.MkdirAll(jobDir, 0755)
	validFile := filepath.Join(jobDir, "test-job_2026-10-03_02-00.dump")
	_ = os.WriteFile(validFile, []byte("dump-content"), 0644)

	// Valid download
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/test-job/backups/test-job_2026-10-03_02-00.dump/download", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK for valid download, got %d", w.Code)
	}

	// Path traversal attempt 1: ../../ encoded
	req = httptest.NewRequest(http.MethodGet, "/api/jobs/test-job/backups/..%2F..%2Fetc%2Fpasswd/download", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Errorf("expected traversal to be blocked (400 or 404), got %d", w.Code)
	}

	// Path traversal attempt 2: ServeMux redirects uncleaned paths containing /../ (307 / 301)
	req = httptest.NewRequest(http.MethodGet, "/api/jobs/test-job/backups/../download", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Errorf("expected traversal to be blocked, got 200 OK")
	}

	// Direct HandleDownloadBackup unit tests verifying D-7 validation
	testCases := []string{
		"..",
		".",
		"../../etc/passwd",
		"subdir/file.dump",
		`..\..\windows\system32`,
		"valid/../../../etc/passwd",
	}
	for _, tc := range testCases {
		wRec := httptest.NewRecorder()
		sc.HandleDownloadBackup(wRec, req, "test-job", tc)
		if wRec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request for filename %q, got %d", tc, wRec.Code)
		}
	}
}

func TestRoutes_ConfigViewAndEdit(t *testing.T) {
	// Case 1: Read-only config (writable=false)
	sc, handler, _, _ := setupTestServer(t, "pass", false)
	sess, _ := sc.Auth.Authenticate("127.0.0.1", "pass")
	cookie := CreateSessionCookie(nil, sess.Token, 3600)

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var configResp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &configResp)
	if configResp["writable"] != false {
		t.Errorf("expected writable to be false")
	}

	// Attempting POST should return 403 Forbidden
	req = httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader("jobs: []"))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", sess.CSRFToken)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden when config is not writable, got %d", w.Code)
	}

	// Case 2: Writable config (writable=true)
	sc.IsWritable = true
	newYAML := `
jobs:
  - name: "reloaded-job"
    engine: "postgres"
    schedule: "0 5 * * *"
    retention_days: 21
    url_env: "TEST_DB_URL"
`
	req = httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(newYAML))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", sess.CSRFToken)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for config save, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestRoutes_LogoutCSRF(t *testing.T) {
	sc, handler, _, _ := setupTestServer(t, "pass", false)
	sess, _ := sc.Auth.Authenticate("127.0.0.1", "pass")
	cookie := CreateSessionCookie(nil, sess.Token, 3600)

	// 1. GET /logout -> 405 Method Not Allowed
	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET /logout, got %d", w.Code)
	}

	// 2. POST /logout without CSRF -> 403 Forbidden
	req = httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for POST /logout without CSRF, got %d", w.Code)
	}

	// 3. POST /logout with invalid CSRF -> 403 Forbidden
	req = httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader("csrf_token=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for POST /logout with bad CSRF, got %d", w.Code)
	}

	// 4. POST /logout with valid CSRF in form -> 303 Redirect to /login
	req = httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader("csrf_token="+sess.CSRFToken))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 Redirect for valid logout, got %d", w.Code)
	}

	// Session should now be revoked
	if _, ok := sc.Auth.ValidateSession(sess.Token); ok {
		t.Errorf("expected session to be revoked after logout")
	}
}

func TestRoutes_SymlinkTraversal(t *testing.T) {
	sc, handler, _, backupDir := setupTestServer(t, "pass", false)
	sess, _ := sc.Auth.Authenticate("127.0.0.1", "pass")
	cookie := CreateSessionCookie(nil, sess.Token, 3600)

	// Create a secret sensitive file outside the backup directory
	secretDir := t.TempDir()
	secretFile := filepath.Join(secretDir, "sensitive.txt")
	_ = os.WriteFile(secretFile, []byte("super-secret-system-data"), 0600)

	// Create job directory
	jobDir := filepath.Join(backupDir, "test-job")
	_ = os.MkdirAll(jobDir, 0755)

	// Create a malicious symlink inside job directory pointing to the sensitive file outside
	symlinkPath := filepath.Join(jobDir, "test-job_malicious.dump")
	if err := os.Symlink(secretFile, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Attempt to download the symlinked file
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/test-job/backups/test-job_malicious.dump/download", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Must be blocked (400 Bad Request)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for symlink traversal, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "super-secret") {
		t.Fatalf("CRITICAL: leaked sensitive data through symlink!")
	}
}

func TestRoutes_LoginRateLimiting429(t *testing.T) {
	_, handler, _, _ := setupTestServer(t, "correctpassword", false)

	// Make 5 failed attempts
	for i := 0; i < 5; i++ {
		form := url.Values{}
		form.Set("password", "badpassword")
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401 Unauthorized, got %d", i, w.Code)
		}
	}

	// 6th attempt should return 429 Too Many Requests with Retry-After header
	form := url.Values{}
	form.Set("password", "correctpassword")
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests, got %d", w.Code)
	}
	if retryAfter := w.Header().Get("Retry-After"); retryAfter != "60" {
		t.Errorf("expected Retry-After: 60, got %q", retryAfter)
	}

	// Also verify JSON accept returns 429 JSON
	reqJSON := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	reqJSON.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqJSON.Header.Set("Accept", "application/json")
	wJSON := httptest.NewRecorder()
	handler.ServeHTTP(wJSON, reqJSON)

	if wJSON.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for JSON request, got %d", wJSON.Code)
	}
	if retryAfter := wJSON.Header().Get("Retry-After"); retryAfter != "60" {
		t.Errorf("expected Retry-After: 60, got %q", retryAfter)
	}
}


