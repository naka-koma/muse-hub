package server

import (
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nakakoma/muse-hub/internal/auth"
	"github.com/nakakoma/muse-hub/internal/jobs"
	"github.com/nakakoma/muse-hub/internal/tools"
)

func NewHandler(authConfig auth.Config, logger *slog.Logger) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "muse-hub", Version: "0.1.0"}, nil)
	tools.Register(server, jobs.NewManager(), logger)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	return authConfig.Middleware(mcpHandler, logger)
}
