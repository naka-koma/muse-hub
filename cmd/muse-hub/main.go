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
	"github.com/nakakoma/muse-hub/internal/claude"
	"github.com/nakakoma/muse-hub/internal/dummyworker"
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
	if cfg.policyFile != "" {
		features.Policy, err = policy.Load(cfg.policyFile)
		if err != nil {
			logger.Error("policy configuration rejected", "error", err.Error())
			os.Exit(1)
		}
	}
	if cfg.claudeAskVolume != "" {
		startup, cancel := context.WithTimeout(ctx, 30*time.Second)
		adapter, err := claude.NewClaude(startup, cfg.claudeAskVolume)
		cancel()
		if err != nil {
			logger.Error("Claude ask startup rejected", "error", err.Error())
			os.Exit(1)
		}
		features.Ask, err = tools.NewAsk("claude", 35*time.Second, adapter.Ask)
		if err != nil {
			logger.Error("Claude ask startup rejected", "error", err.Error())
			os.Exit(1)
		}
	}
	if cfg.jobsDB != "" {
		if cfg.dummySubprocess {
			features.DelegateRunner = dummyworker.Run
		}
		if cfg.codexExe != "" {
			codexRunner, err := dummyworker.NewCodex(cfg.codexExe, cfg.codexHome)
			if err != nil {
				logger.Error("Codex startup rejected", "error", err.Error())
				os.Exit(1)
			}
			features.CodexRunner = func(ctx context.Context, task, reference string) (string, error) {
				result, err := codexRunner(ctx, task, reference)
				if err != nil && ctx.Err() == nil {
					logger.Warn("Codex job failed", "stage", err.Error())
				}
				return result, err
			}
		}
		features.CodexText = cfg.codexText
		if cfg.claudeVolume != "" {
			startup, cancel := context.WithTimeout(ctx, 20*time.Second)
			adapter, err := claude.NewClaude(startup, cfg.claudeVolume)
			cancel()
			if err != nil {
				logger.Error("Claude startup rejected", "error", err.Error())
				os.Exit(1)
			}
			features.ClaudeRunner = func(ctx context.Context, task, reference string) (string, error) {
				result, err := adapter.Run(ctx, task, reference)
				if err != nil && ctx.Err() == nil {
					logger.Warn("Claude job failed", "stage", err.Error())
				}
				return result, err
			}
			features.ClaudeText = cfg.claudeText
		}
		startup, cancel := context.WithTimeout(ctx, 5*time.Second)
		features.Jobs, err = asyncjobs.Open(startup, cfg.jobsDB, 30*time.Second)
		if err == nil {
			features.Approvals = cfg.policyApprovals
			if cfg.policyApprovals {
				features.Policy, err = features.Jobs.InitializePolicy(startup, features.Policy)
			} else {
				var managed bool
				managed, err = features.Jobs.HasManagedPolicy(startup)
				if err == nil && managed {
					err = errors.New("managed policy database requires MUSE_HUB_POLICY_APPROVALS=1")
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
			logger.Error("job database startup rejected", "error", err.Error())
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
	auth            auth.Config
	port            int
	tailscaleHost   string
	claudeAskVolume string
	jobsDB          string
	dummySubprocess bool
	codexExe        string
	codexHome       string
	codexText       bool
	claudeVolume    string
	claudeText      bool
	policyFile      string
	policyApprovals bool
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
	askVolume, err := settingWithAlias("MUSE_HUB_CLAUDE_ASK_VOLUME", "MUSE_HUB_PHASE1_AUTH_VOLUME")
	if err != nil {
		return config{}, err
	}
	if askVolume != "" && !claude.ValidVolumeName(askVolume) {
		return config{}, errors.New("MUSE_HUB_CLAUDE_ASK_VOLUME must be a Docker volume name")
	}
	jobsDB, err := jobsDBSetting()
	if err != nil {
		return config{}, err
	}
	if askVolume != "" && jobsDB != "" {
		return config{}, errors.New("configure either MUSE_HUB_CLAUDE_ASK_VOLUME for ask or MUSE_HUB_JOBS_DB for delegate")
	}
	dummySubprocess, err := settingWithAlias("MUSE_HUB_DUMMY_SUBPROCESS", "MUSE_HUB_PHASE2_SUBPROCESS")
	if err != nil {
		return config{}, err
	}
	if dummySubprocess != "" && dummySubprocess != "1" {
		return config{}, errors.New("MUSE_HUB_DUMMY_SUBPROCESS must be unset or 1")
	}
	if dummySubprocess == "1" && (jobsDB == "" || !dummyworker.Supported) {
		return config{}, errors.New("dummy subprocess mode requires Windows and MUSE_HUB_JOBS_DB")
	}
	codexExe := strings.TrimSpace(os.Getenv("MUSE_HUB_CODEX_EXE"))
	codexHome := strings.TrimSpace(os.Getenv("MUSE_HUB_CODEX_HOME"))
	if (codexExe == "") != (codexHome == "") {
		return config{}, errors.New("set both MUSE_HUB_CODEX_EXE and MUSE_HUB_CODEX_HOME to enable Codex")
	}
	if codexExe != "" && (jobsDB == "" || !dummyworker.Supported) {
		return config{}, errors.New("Codex delegation requires Windows and the job database")
	}
	codexText := strings.TrimSpace(os.Getenv("MUSE_HUB_CODEX_TEXT"))
	if codexText != "" && (codexText != "1" || codexExe == "") {
		return config{}, errors.New("MUSE_HUB_CODEX_TEXT requires configured Codex delegation and must be unset or 1")
	}
	claudeVolume := strings.TrimSpace(os.Getenv("MUSE_HUB_CLAUDE_AUTH_VOLUME"))
	if claudeVolume != "" && (!claude.ValidVolumeName(claudeVolume) || jobsDB == "") {
		return config{}, errors.New("Claude delegation requires a Docker login volume name and the job database")
	}
	claudeText := strings.TrimSpace(os.Getenv("MUSE_HUB_CLAUDE_TEXT"))
	if claudeText != "" && (claudeText != "1" || claudeVolume == "") {
		return config{}, errors.New("MUSE_HUB_CLAUDE_TEXT requires configured Claude delegation and must be unset or 1")
	}
	policyFile, err := settingWithAlias("MUSE_HUB_POLICY_FILE", "MUSE_HUB_PHASE3_POLICY")
	if err != nil {
		return config{}, err
	}
	if policyFile != "" && jobsDB == "" {
		return config{}, errors.New("MUSE_HUB_POLICY_FILE requires MUSE_HUB_JOBS_DB")
	}
	approvals, err := settingWithAlias("MUSE_HUB_POLICY_APPROVALS", "MUSE_HUB_PHASE5_APPROVALS")
	if err != nil {
		return config{}, err
	}
	if approvals != "" && approvals != "1" {
		return config{}, errors.New("MUSE_HUB_POLICY_APPROVALS must be unset or 1")
	}
	if approvals == "1" && policyFile == "" {
		return config{}, errors.New("policy approvals require MUSE_HUB_POLICY_FILE and MUSE_HUB_JOBS_DB")
	}
	return config{auth: authConfig, port: port, tailscaleHost: tailscaleHost, claudeAskVolume: askVolume,
		jobsDB: jobsDB, dummySubprocess: dummySubprocess == "1", codexExe: codexExe, codexHome: codexHome, codexText: codexText == "1",
		claudeVolume: claudeVolume, claudeText: claudeText == "1",
		policyFile: policyFile, policyApprovals: approvals == "1"}, nil
}

func jobsDBSetting() (string, error) {
	return settingWithAlias("MUSE_HUB_JOBS_DB", "MUSE_HUB_PHASE2_DB")
}

func settingWithAlias(name, alias string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	legacy := strings.TrimSpace(os.Getenv(alias))
	if value == "" {
		return legacy, nil
	}
	if legacy != "" && value != legacy {
		return "", fmt.Errorf("%s conflicts with %s", name, alias)
	}
	return value, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
