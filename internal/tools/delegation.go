package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nakakoma/muse-hub/internal/asyncjobs"
	"github.com/nakakoma/muse-hub/internal/policy"
)

type DelegateInput struct {
	Agent        string   `json:"agent" jsonschema:"use dummy for the Phase 2 probe"`
	Task         string   `json:"task" jsonschema:"use 2 + 3 for the Phase 2 probe"`
	Context      string   `json:"context" jsonschema:"use an empty string for the Phase 2 probe"`
	DataCategory []string `json:"data_category" jsonschema:"use [code] for the Phase 2 probe"`
	RequestID    string   `json:"request_id" jsonschema:"a unique ID for the request; reuse it only when retrying the same request"`
	Session      string   `json:"session,omitempty" jsonschema:"optional session ID; only one job can run in a session at a time"`
	Resource     string   `json:"resource,omitempty" jsonschema:"optional resource alias; use arithmetic for the synthetic probe"`
}

type DelegatedJobInput struct {
	JobID string `json:"job_id" jsonschema:"job ID returned by delegate"`
}

type ListJobsOutput struct {
	Jobs []asyncjobs.Snapshot `json:"jobs"`
}

func RegisterDelegation(server *mcp.Server, manager *asyncjobs.Manager, logger *slog.Logger, rules *policy.Policy) {
	gate := policyGate{policy: rules, manager: manager}
	if rules != nil {
		gate.register(server)
		gate.registerReview(server, logger)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delegate",
		Description: "Start a persisted asynchronous Phase 2 dummy job and return its ID. Use agent dummy, task 2 + 3, empty context, and data_category [code].",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input DelegateInput) (*mcp.CallToolResult, StartJobOutput, error) {
		started := time.Now()
		resource := input.Resource
		if resource == "" {
			resource = arithmeticResource
		}
		verified := input.Agent == "dummy" && strings.TrimSpace(input.Task) == "2 + 3" && input.Context == "" &&
			len(input.DataCategory) == 1 && input.DataCategory[0] == "code" && resource == arithmeticResource
		if err := gate.check(ctx, "delegate", policy.Access{
			Agent: input.Agent, Operation: "consult", Resource: resource,
			Portions: []policy.Portion{{Categories: input.DataCategory, Level: policy.Full}},
		}, verified); err != nil {
			return nil, StartJobOutput{}, err
		}
		if !verified {
			logCall(logger, "delegate", "rejected", "", started)
			return nil, StartJobOutput{}, asyncjobs.ErrInvalidInput
		}
		id, err := manager.Submit(ctx, asyncjobs.Submission{
			RequestID: input.RequestID, Session: input.Session, WorkKey: "synthetic-sum-v1", Run: runDummyDelegation,
		})
		logDelegation(logger, "delegate", id, started, err)
		return nil, StartJobOutput{JobID: id}, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_result", Description: "Return the status and completed result of a delegated job.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input DelegatedJobInput) (*mcp.CallToolResult, JobResultOutput, error) {
		started := time.Now()
		job, err := manager.Get(ctx, input.JobID)
		if err != nil {
			logDelegation(logger, "get_result", "", started, err)
			return nil, JobResultOutput{}, err
		}
		out := JobResultOutput{Snapshot: job}
		if job.ReviewSession != "" {
			if err := gate.checkReviewRead(ctx, "get_review_result"); err != nil {
				return nil, JobResultOutput{}, err
			}
			if job.Result != nil {
				var summary ReviewSummary
				if err := json.Unmarshal([]byte(*job.Result), &summary); err != nil {
					return nil, JobResultOutput{}, asyncjobs.ErrUnavailable
				}
				out.Result, out.Review = nil, &summary
			}
		} else if err := gate.check(ctx, "get_result", arithmeticRead(), true); err != nil {
			return nil, JobResultOutput{}, err
		}
		logDelegation(logger, "get_result", job.JobID, started, err)
		return nil, out, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "cancel_job", Description: "Request cancellation and return the job status once its worker has stopped.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input DelegatedJobInput) (*mcp.CallToolResult, asyncjobs.Snapshot, error) {
		started := time.Now()
		wait, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		job, err := manager.Cancel(wait, input.JobID)
		if rules != nil || job.ReviewSession != "" {
			job.Result = nil
			job.ReviewSession = ""
		}
		logDelegation(logger, "cancel_job", job.JobID, started, err)
		return nil, job, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_jobs", Description: "Return the latest 100 delegated job summaries without their result bodies.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ListJobsOutput, error) {
		started := time.Now()
		if err := gate.check(ctx, "list_jobs", arithmeticRead(), true); err != nil {
			return nil, ListJobsOutput{}, err
		}
		list, err := manager.List(ctx)
		if err == nil && slices.ContainsFunc(list, func(job asyncjobs.Snapshot) bool { return job.ReviewSession != "" }) {
			if err := gate.checkReviewRead(ctx, "list_review_jobs"); err != nil {
				list = slices.DeleteFunc(list, func(job asyncjobs.Snapshot) bool { return job.ReviewSession != "" })
			}
		}
		logDelegation(logger, "list_jobs", "", started, err)
		return nil, ListJobsOutput{Jobs: list}, err
	})
}

func runDummyDelegation(ctx context.Context) (string, error) {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "5", nil
	}
}

func logDelegation(logger *slog.Logger, tool, id string, started time.Time, err error) {
	if err != nil {
		logCall(logger, tool, "failed", "", started)
		return
	}
	logCall(logger, tool, "allowed", id, started)
}
