package tools

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type AskInput struct {
	Agent        string   `json:"agent" jsonschema:"the configured agent"`
	Task         string   `json:"task" jsonschema:"the question or request"`
	Context      string   `json:"context" jsonschema:"background for the task; use an empty string when there is none"`
	DataCategory []string `json:"data_category" jsonschema:"the categories of data in the task and context"`
}

type AskOutput struct {
	Answer string `json:"answer"`
}

// Ask runs short consultations against one adapter. The adapter must honor
// context cancellation, including stopping any work it started.
type Ask struct {
	agent   string
	timeout time.Duration
	run     func(context.Context, AskInput) (string, error)
}

func NewAsk(agent string, timeout time.Duration, run func(context.Context, AskInput) (string, error)) (*Ask, error) {
	if agent != "claude" && agent != "codex" {
		return nil, errors.New("unsupported ask adapter")
	}
	if timeout <= 0 || run == nil {
		return nil, errors.New("ask requires an adapter and a positive timeout")
	}
	return &Ask{agent: agent, timeout: timeout, run: run}, nil
}

func (a *Ask) Register(server *mcp.Server, logger *slog.Logger) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ask",
		Description: "Phase 1 synthetic probe: ask claude to solve 2 + 3, with an empty context and data_category [code].",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input AskInput) (*mcp.CallToolResult, AskOutput, error) {
		started := time.Now()
		answer, outcome, err := a.call(ctx, input)
		logCall(logger, "ask", outcome, "", started)
		return nil, AskOutput{Answer: answer}, err
	})
}

func (a *Ask) call(ctx context.Context, input AskInput) (answer, outcome string, err error) {
	if input.Agent != a.agent {
		return "", "rejected", errors.New("agent is not configured")
	}
	if strings.TrimSpace(input.Task) == "" {
		return "", "rejected", errors.New("task must not be empty")
	}
	if len(input.DataCategory) == 0 {
		return "", "rejected", errors.New("at least one data category is required")
	}
	for _, category := range input.DataCategory {
		if strings.TrimSpace(category) == "" {
			return "", "rejected", errors.New("data categories must not be empty")
		}
	}

	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	if ctx.Err() == nil {
		answer, err = a.run(ctx, input)
	}
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return "", "timed_out", errors.New("ask timed out")
	case context.Canceled:
		return "", "cancelled", errors.New("ask was cancelled")
	}
	if err != nil {
		// Adapter errors may contain request data or command output.
		return "", "failed", errors.New("ask adapter failed")
	}
	return answer, "allowed", nil
}
