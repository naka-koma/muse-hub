package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nakakoma/muse-hub/internal/auth"
	"github.com/nakakoma/muse-hub/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("configuration rejected", "error", err.Error())
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", listenAddress(cfg.port))
	if err != nil {
		logger.Error("listen failed", "error", err.Error())
		os.Exit(1)
	}
	defer listener.Close()
	logger.Info("server started", "address", listener.Addr().String(), "endpoint", "/mcp")

	httpServer := &http.Server{
		Handler:           server.NewHandler(cfg.auth, cfg.tailscaleHost, logger),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "error", err.Error())
		os.Exit(1)
	}
}

type config struct {
	auth          auth.Config
	port          int
	tailscaleHost string
}

var tailscaleDNSName = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.ts\.net$`)

func validTailscaleHost(value string) bool {
	host, port, hasPort := strings.Cut(value, ":")
	if !tailscaleDNSName.MatchString(host) {
		return false
	}
	if !hasPort {
		return true
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == port
}

func listenAddress(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func loadConfig() (config, error) {
	token := os.Getenv("MUSE_HUB_TOKEN")
	if token == "" {
		return config{}, fmt.Errorf("MUSE_HUB_TOKEN is required; generate one with the PowerShell command in README.md")
	}
	header := envOr("MUSE_HUB_AUTH_HEADER", "Authorization")
	scheme := envOr("MUSE_HUB_AUTH_SCHEME", "Bearer")
	authConfig := auth.Config{HeaderName: header, Scheme: scheme, Token: token}
	if err := authConfig.Validate(); err != nil {
		return config{}, err
	}
	port := 8787
	if raw := os.Getenv("MUSE_HUB_PORT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			return config{}, fmt.Errorf("MUSE_HUB_PORT must be an integer from 1 to 65535")
		}
		port = parsed
	}
	tailscaleHost := strings.TrimSpace(os.Getenv("MUSE_HUB_TAILSCALE_HOST"))
	if tailscaleHost != "" && !validTailscaleHost(tailscaleHost) {
		return config{}, fmt.Errorf("MUSE_HUB_TAILSCALE_HOST must be a Tailscale DNS name, optionally followed by :port")
	}
	return config{auth: authConfig, port: port, tailscaleHost: tailscaleHost}, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
