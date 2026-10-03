package web

import (
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"strings"
)

//go:embed assets/*
var assetsFS embed.FS

// NewRouter sets up all HTTP routes, middlewares, embedded assets, and handlers.
func NewRouter(sc *ServerContext) http.Handler {
	mux := http.NewServeMux()

	// Embedded static assets (/assets/*)
	staticFS, err := fs.Sub(assetsFS, "assets")
	if err == nil {
		mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(staticFS))))
	}

	// Templates
	indexTmpl := template.Must(template.ParseFS(assetsFS, "assets/index.html"))
	loginTmpl := template.Must(template.ParseFS(assetsFS, "assets/login.html"))

	// Health check endpoint (D-10, public)
	mux.HandleFunc("/healthz", sc.HandleHealthz)

	// Login routes
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		clientIP := getClientIP(r)

		if r.Method == http.MethodGet {
			// Check if already authenticated
			if cookie, err := r.Cookie(SessionCookieName); err == nil {
				if _, ok := sc.Auth.ValidateSession(cookie.Value); ok {
					http.Redirect(w, r, "/", http.StatusSeeOther)
					return
				}
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = loginTmpl.Execute(w, map[string]any{})
			return
		}

		if r.Method == http.MethodPost {
			password := r.FormValue("password")
			session, err := sc.Auth.Authenticate(clientIP, password)
			if err != nil {
				statusCode := http.StatusUnauthorized
				if errors.Is(err, ErrRateLimited) {
					statusCode = http.StatusTooManyRequests
					w.Header().Set("Retry-After", "60")
				}

				if strings.Contains(r.Header.Get("Accept"), "application/json") {
					writeError(w, statusCode, err.Error())
					return
				}

				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(statusCode)
				_ = loginTmpl.Execute(w, map[string]any{
					"Error": err.Error(),
				})
				return
			}

			// Set session cookie
			cookie := CreateSessionCookie(r, session.Token, int(SessionDuration.Seconds()))
			http.SetCookie(w, cookie)

			// If JSON request, return token, else redirect to /
			if strings.Contains(r.Header.Get("Accept"), "application/json") {
				writeJSON(w, http.StatusOK, map[string]string{
					"message":    "authenticated",
					"csrf_token": session.CSRFToken,
				})
				return
			}

			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}

		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	// Logout route (POST-only with CSRF validation)
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		session, ok := sc.Auth.ValidateSession(cookie.Value)
		if !ok {
			clearCookie := CreateSessionCookie(r, "", -1)
			http.SetCookie(w, clearCookie)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Validate CSRF token from header or form
		csrfToken := r.Header.Get("X-CSRF-Token")
		if csrfToken == "" {
			csrfToken = r.FormValue("csrf_token")
		}

		if !sc.Auth.ValidateCSRF(session, csrfToken) {
			http.Error(w, "Forbidden: invalid CSRF token", http.StatusForbidden)
			return
		}

		sc.Auth.RevokeSession(cookie.Value)
		clearCookie := CreateSessionCookie(r, "", -1)
		http.SetCookie(w, clearCookie)

		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			writeJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
			return
		}

		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})

	// UI Home Route
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		session, ok := sc.Auth.ValidateSession(cookie.Value)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = indexTmpl.Execute(w, map[string]any{
			"CSRFToken": session.CSRFToken,
		})
	})

	// API Dispatcher with Auth and CSRF middleware
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		// 1. Session verification
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized: session missing")
			return
		}

		session, ok := sc.Auth.ValidateSession(cookie.Value)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized: session invalid or expired")
			return
		}

		// 2. CSRF verification on mutating methods (POST, PUT, DELETE)
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			csrfHeader := r.Header.Get("X-CSRF-Token")
			if csrfHeader == "" {
				csrfHeader = r.FormValue("csrf_token")
			}

			if !sc.Auth.ValidateCSRF(session, csrfHeader) {
				writeError(w, http.StatusForbidden, "forbidden: invalid CSRF token")
				return
			}
		}

		// 3. API Route Dispatching
		path := strings.TrimPrefix(r.URL.Path, "/api")
		parts := strings.Split(strings.Trim(path, "/"), "/")

		if len(parts) == 1 && parts[0] == "jobs" && r.Method == http.MethodGet {
			sc.HandleGetJobs(w, r)
			return
		}

		if len(parts) == 1 && parts[0] == "config" {
			if r.Method == http.MethodGet {
				sc.HandleGetConfig(w, r)
				return
			} else if r.Method == http.MethodPost {
				sc.HandleSaveConfig(w, r)
				return
			}
		}

		if len(parts) >= 2 && parts[0] == "jobs" {
			jobName := parts[1]

			if len(parts) == 3 && parts[2] == "run" && r.Method == http.MethodPost {
				sc.HandleRunJob(w, r, jobName)
				return
			}

			if len(parts) == 3 && parts[2] == "backups" && r.Method == http.MethodGet {
				sc.HandleGetBackups(w, r, jobName)
				return
			}

			if len(parts) == 3 && parts[2] == "logs" && r.Method == http.MethodGet {
				sc.HandleGetLogs(w, r, jobName)
				return
			}

			if len(parts) == 5 && parts[2] == "backups" && parts[4] == "download" && r.Method == http.MethodGet {
				filename := parts[3]
				sc.HandleDownloadBackup(w, r, jobName, filename)
				return
			}
		}

		writeError(w, http.StatusNotFound, "api endpoint not found")
	})

	return mux
}

func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			return strings.TrimSpace(parts[0])
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
