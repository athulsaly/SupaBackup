package web

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	DefaultDashboardPort = "8080"
)

// Server wraps the dashboard HTTP server.
type Server struct {
	httpServer *http.Server
	port       string
}

// NewServer initializes a Server instance if dashboard is enabled.
func NewServer(sc *ServerContext, port string) *Server {
	if port == "" {
		port = os.Getenv("DASHBOARD_PORT")
	}
	if port == "" {
		port = DefaultDashboardPort
	}
	port = strings.TrimPrefix(port, ":")

	handler := NewRouter(sc)

	return &Server{
		port: port,
		httpServer: &http.Server{
			Addr:         ":" + port,
			Handler:      handler,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
	}
}

// Start launches the HTTP server listening on the configured port.
func (s *Server) Start() error {
	log.Printf("[web] dashboard listening on http://0.0.0.0:%s", s.port)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("web server failed: %w", err)
	}
	return nil
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	log.Printf("[web] shutting down web dashboard...")
	return s.httpServer.Shutdown(ctx)
}
