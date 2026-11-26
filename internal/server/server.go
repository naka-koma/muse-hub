package server

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nakakoma/muse-hub/internal/asyncjobs"
	"github.com/nakakoma/muse-hub/internal/auth"
	"github.com/nakakoma/muse-hub/internal/jobs"
	"github.com/nakakoma/muse-hub/internal/policy"
	"github.com/nakakoma/muse-hub/internal/tools"
)

type Features struct {
	Ask       *tools.Ask
	Jobs      *asyncjobs.Manager
	Policy    *policy.Policy
	Approvals bool
}

func NewHandler(authConfig auth.Config, tailscaleHost string, logger *slog.Logger, features Features) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "muse-hub", Version: "0.1.0"}, nil)
	if features.Jobs != nil {
		tools.Register(server, nil, logger)
		tools.RegisterDelegation(server, features.Jobs, logger, features.Policy, features.Approvals)
	} else {
		tools.Register(server, jobs.NewManager(), logger)
		if features.Ask != nil {
			features.Ask.Register(server, logger)
		}
	}
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	var handler http.Handler = mcpHandler
	if tailscaleHost != "" {
		originProtection := http.NewCrossOriginProtection()
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(r.Host, tailscaleHost) {
				if err := originProtection.Check(r); err != nil {
					http.Error(w, "forbidden origin", http.StatusForbidden)
					return
				}
				// Tailscale Serve preserves Host. Normalize only the configured authority
				// so the SDK's localhost guard still checks every other request.
				r = r.WithContext(r.Context())
				r.Host = "localhost"
			}
			mcpHandler.ServeHTTP(w, r)
		})
	}
	return authConfig.Middleware(handler, logger)
}
