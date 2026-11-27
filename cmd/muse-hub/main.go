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

	"github.com/nakakoma/muse-hub/internal/asyncjobs"
	"github.com/nakakoma/muse-hub/internal/auth"
	"github.com/nakakoma/muse-hub/internal/dummyworker"
	"github.com/nakakoma/muse-hub/internal/phase1"
	"github.com/nakakoma/muse-hub/internal/policy"
	"github.com/nakakoma/muse-hub/internal/server"
	"github.com/nakakoma/muse-hub/internal/tools"
)

func main() {
	if code, handled := dummyworker.Command(os.Args[1:]); handled {
		os.Exit(code)
	}
	if len(os.Args) > 1 {
		if os.Args[1] != "policy" {
			fmt.Fprintln(os.Stderr, errPolicyUsage)
			os.Exit(1)
		}
		if err := runPolicyCommand(context.Background(), os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("configuration rejected", "error", err.Error())
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var features server.Features
	if cfg.phase3Policy != "" {
		features.Policy, err = policy.Load(cfg.phase3Policy)
		if err != nil {
			logger.Error("Phase 3 startup rejected", "error", err.Error())
			os.Exit(1)
		}
	}
	if cfg.phase1Volume != "" {
		startup, cancel := context.WithTimeout(ctx, 30*time.Second)
		adapter, err := phase1.NewClaude(startup, cfg.phase1Volume)
		cancel()
		if err != nil {
			logger.Error("Phase 1 startup rejected", "error", err.Error())
			os.Exit(1)
		}
		features.Ask, err = tools.NewAsk("claude", 35*time.Second, adapter.Ask)
		if err != nil {
			logger.Error("Phase 1 startup rejected", "error", err.Error())
			os.Exit(1)
		}
	}
	if cfg.phase2DB != "" {
		if cfg.phase2Subprocess {
			features.DelegateRunner = dummyworker.Run
		}
		if cfg.codexExe != "" {
			features.CodexRunner, err = dummyworker.NewCodex(cfg.codexExe, cfg.codexHome)
			if err != nil {
				logger.Error("Codex startup rejected", "error", err.Error())
				os.Exit(1)
			}
		}
		startup, cancel := context.WithTimeout(ctx, 5*time.Second)
		features.Jobs, err = asyncjobs.Open(startup, cfg.phase2DB, 30*time.Second)
		if err == nil {
			features.Approvals = cfg.phase5Approvals
			if cfg.phase5Approvals {
				features.Policy, err = features.Jobs.InitializePolicy(startup, features.Policy)
			} else {
				var managed bool
				managed, err = features.Jobs.HasManagedPolicy(startup)
				if err == nil && managed {
					err = errors.New("managed policy database requires MUSE_HUB_PHASE5_APPROVALS=1")
				}
			}
		}
		if err == nil && features.Policy != nil {
			err = features.Jobs.InvalidateReviewSessions(startup, features.Policy.Revision())
		}
		cancel()
		if err != nil {
			if features.Jobs != nil {
				_ = features.Jobs.Close()
			}
			logger.Error("Phase 2 startup rejected", "error", err.Error())
			os.Exit(1)
		}
		defer features.Jobs.Close()
	}

	listener, err := net.Listen("tcp", listenAddress(cfg.port))
	if err != nil {
		logger.Error("listen failed", "error", err.Error())
		os.Exit(1)
	}
	defer listener.Close()
	logger.Info("server started", "address", listener.Addr().String(), "endpoint", "/mcp")

	httpServer := &http.Server{
		Handler:           server.NewHandler(cfg.auth, cfg.tailscaleHost, logger, features),
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "error", err.Error())
		os.Exit(1)
	}
}

type config struct {
	auth             auth.Config
	port             int
	tailscaleHost    string
	phase1Volume     string
	phase2DB         string
	phase2Subprocess bool
	codexExe         string
	codexHome        string
	phase3Policy     string
	phase5Approvals  bool
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
	phase1Volume := os.Getenv("MUSE_HUB_PHASE1_AUTH_VOLUME")
	if phase1Volume != "" && !phase1.ValidVolumeName(phase1Volume) {
		return config{}, fmt.Errorf("MUSE_HUB_PHASE1_AUTH_VOLUME must be a Docker volume name")
	}
	phase2DB := strings.TrimSpace(os.Getenv("MUSE_HUB_PHASE2_DB"))
	if phase1Volume != "" && phase2DB != "" {
		return config{}, errors.New("configure either Phase 1 ask or Phase 2 delegate")
	}
	phase2Subprocess := strings.TrimSpace(os.Getenv("MUSE_HUB_PHASE2_SUBPROCESS"))
	if phase2Subprocess != "" && phase2Subprocess != "1" {
		return config{}, errors.New("MUSE_HUB_PHASE2_SUBPROCESS must be unset or 1")
	}
	if phase2Subprocess == "1" && (phase2DB == "" || !dummyworker.Supported) {
		return config{}, errors.New("Phase 2 subprocess verification requires Windows and the job database")
	}
	codexExe := strings.TrimSpace(os.Getenv("MUSE_HUB_CODEX_EXE"))
	codexHome := strings.TrimSpace(os.Getenv("MUSE_HUB_CODEX_HOME"))
	if (codexExe == "") != (codexHome == "") {
		return config{}, errors.New("set both MUSE_HUB_CODEX_EXE and MUSE_HUB_CODEX_HOME to enable Codex")
	}
	if codexExe != "" && (phase2DB == "" || !dummyworker.Supported) {
		return config{}, errors.New("Codex delegation requires Windows and the job database")
	}
	phase3Policy := strings.TrimSpace(os.Getenv("MUSE_HUB_PHASE3_POLICY"))
	if phase3Policy != "" && phase2DB == "" {
		return config{}, errors.New("Phase 3 policy requires the job database")
	}
	phase5 := strings.TrimSpace(os.Getenv("MUSE_HUB_PHASE5_APPROVALS"))
	if phase5 != "" && phase5 != "1" {
		return config{}, errors.New("MUSE_HUB_PHASE5_APPROVALS must be unset or 1")
	}
	if phase5 == "1" && phase3Policy == "" {
		return config{}, errors.New("Phase 5 approvals require a policy configuration and job database")
	}
	return config{auth: authConfig, port: port, tailscaleHost: tailscaleHost, phase1Volume: phase1Volume,
		phase2DB: phase2DB, phase2Subprocess: phase2Subprocess == "1", codexExe: codexExe, codexHome: codexHome,
		phase3Policy: phase3Policy, phase5Approvals: phase5 == "1"}, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
