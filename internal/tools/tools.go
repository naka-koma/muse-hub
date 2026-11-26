package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nakakoma/muse-hub/internal/jobs"
)

type PingInput struct{}
type PingOutput struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}
type EchoInput struct {
	Text string `json:"text" jsonschema:"text to return, at most 1000 characters"`
}
type EchoOutput struct {
	Text string `json:"text"`
}
type StartJobInput struct {
	DelaySeconds int `json:"delay_seconds" jsonschema:"dummy job duration in seconds, from 1 to 60"`
}
type StartJobOutput struct {
	JobID string `json:"job_id"`
}
type GetResultInput struct {
	JobID string `json:"job_id" jsonschema:"job id returned by start_dummy_job"`
}

type GetResultOutput struct {
	Status jobs.Status  `json:"status"`
	Result *jobs.Result `json:"result,omitempty"`
}

func Register(server *mcp.Server, manager *jobs.Manager, logger *slog.Logger) {
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Description: "Check service availability and return its current time."}, func(context.Context, *mcp.CallToolRequest, PingInput) (*mcp.CallToolResult, PingOutput, error) {
		out := PingOutput{Status: "ok", Timestamp: time.Now().UTC()}
		logCall(logger, "ping", "allowed", "", time.Now())
		return nil, out, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Return the supplied text unchanged (up to 1,000 characters)."}, func(_ context.Context, _ *mcp.CallToolRequest, input EchoInput) (*mcp.CallToolResult, EchoOutput, error) {
		started := time.Now()
		if len([]rune(input.Text)) > 1000 {
			logCall(logger, "echo", "rejected", "", started)
			return nil, EchoOutput{}, fmt.Errorf("text must be at most 1000 characters")
		}
		logCall(logger, "echo", "allowed", "", started)
		return nil, EchoOutput{Text: input.Text}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "start_dummy_job", Description: "Start a delayed in-memory dummy job (1 to 60 seconds)."}, func(_ context.Context, _ *mcp.CallToolRequest, input StartJobInput) (*mcp.CallToolResult, StartJobOutput, error) {
		started := time.Now()
		id, err := manager.Start(time.Duration(input.DelaySeconds) * time.Second)
		if err != nil {
			logCall(logger, "start_dummy_job", "rejected", "", started)
			return nil, StartJobOutput{}, err
		}
		logCall(logger, "start_dummy_job", "allowed", id, started)
		return nil, StartJobOutput{JobID: id}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_result", Description: "Return the status and, when complete, result of a dummy job."}, func(_ context.Context, _ *mcp.CallToolRequest, input GetResultInput) (*mcp.CallToolResult, GetResultOutput, error) {
		started := time.Now()
		if strings.TrimSpace(input.JobID) == "" {
			logCall(logger, "get_result", "rejected", "", started)
			return nil, GetResultOutput{}, jobs.ErrNotFound
		}
		snapshot, err := manager.Get(input.JobID)
		if err != nil {
			status := "failed"
			if errors.Is(err, jobs.ErrNotFound) {
				status = "job_not_found"
			}
			logCall(logger, "get_result", status, input.JobID, started)
			return nil, GetResultOutput{}, err
		}
		logCall(logger, "get_result", "allowed", input.JobID, started)
		return nil, GetResultOutput{Status: snapshot.Status, Result: snapshot.Result}, nil
	})
}

func logCall(logger *slog.Logger, tool, outcome, jobID string, started time.Time) {
	logger.Info("tool request", "tool", tool, "outcome", outcome, "job_id", jobID, "duration_ms", time.Since(started).Milliseconds())
}
