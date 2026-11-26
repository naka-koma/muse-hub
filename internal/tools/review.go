package tools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nakakoma/muse-hub/internal/asyncjobs"
	"github.com/nakakoma/muse-hub/internal/policy"
)

const proposerAgent = "dummy_proposer"
const reviewerAgent = "dummy_reviewer"

type DiscussInput struct {
	Agents       []string `json:"agents" jsonschema:"ordered dummy proposer and reviewer"`
	Topic        string   `json:"topic" jsonschema:"use the synthetic 2 + 3 fixture"`
	Context      string   `json:"context" jsonschema:"use an empty string for the synthetic fixture"`
	DataCategory []string `json:"data_category" jsonschema:"use [code] for the synthetic fixture"`
	Rounds       int      `json:"rounds" jsonschema:"number of review rounds from 1 to 3"`
	Mode         string   `json:"mode" jsonschema:"use review"`
	RequestID    string   `json:"request_id" jsonschema:"retry identifier"`
	SessionID    string   `json:"session_id,omitempty" jsonschema:"omit to start or use the Hub ID from a prior review"`
}

type ReviewSummary struct {
	Conclusion     string   `json:"conclusion"`
	Agreements     []string `json:"agreements"`
	Differences    []string `json:"differences"`
	Unresolved     []string `json:"unresolved"`
	Rounds         int      `json:"rounds"`
	PreviousRounds int      `json:"previous_rounds"`
}

type SessionsOutput struct {
	Sessions []asyncjobs.ReviewSession `json:"sessions"`
}

type JobResultOutput struct {
	asyncjobs.Snapshot
	Review *ReviewSummary `json:"review,omitempty"`
}

func (g policyGate) registerReview(server *mcp.Server, logger *slog.Logger) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "discuss", Description: "Start or continue a bounded synthetic review with dummy_proposer and dummy_reviewer, using mode review and topic 2 + 3.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DiscussInput) (*mcp.CallToolResult, asyncjobs.ReviewReceipt, error) {
		started := time.Now()
		verified := slices.Equal(in.Agents, []string{proposerAgent, reviewerAgent}) && strings.TrimSpace(in.Topic) == "2 + 3" &&
			in.Context == "" && slices.Equal(in.DataCategory, []string{"code"}) && in.Mode == "review" && in.Rounds >= 1 && in.Rounds <= asyncjobs.MaxReviewRounds
		access := reviewAccess(proposerAgent, "consult", policy.Full)
		access.Portions[0].Categories = in.DataCategory
		if err := g.check(ctx, "discuss", access, verified); err != nil {
			return nil, asyncjobs.ReviewReceipt{}, err
		}
		receipt, err := g.manager.SubmitReview(ctx, asyncjobs.ReviewSubmission{
			RequestID: in.RequestID, SessionID: in.SessionID, Revision: g.policy.Revision(), Rounds: in.Rounds,
			Run: func(ctx context.Context, session asyncjobs.ReviewSession) (string, error) {
				summary, err := runReview(ctx, session, in.Rounds, func(stage, agent string, level policy.Level) error {
					return g.check(ctx, stage, reviewAccess(agent, "consult", level), true)
				})
				if err != nil {
					return "", err
				}
				data, err := json.Marshal(summary)
				return string(data), err
			},
		})
		logDelegation(logger, "discuss", receipt.JobID, started, err)
		return nil, receipt, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_sessions", Description: "List Hub review sessions and whether they can be resumed with the current policy.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, SessionsOutput, error) {
		if err := g.checkReviewRead(ctx, "list_sessions"); err != nil {
			return nil, SessionsOutput{}, err
		}
		sessions, err := g.manager.ListReviewSessions(ctx, g.policy.Revision())
		return nil, SessionsOutput{Sessions: sessions}, err
	})
}

func reviewAccess(agent, operation string, level policy.Level) policy.Access {
	return policy.Access{Agent: agent, Operation: operation, Resource: arithmeticResource,
		Portions: []policy.Portion{{Categories: []string{"code"}, Level: level}}}
}

func (g policyGate) checkReviewRead(ctx context.Context, event string) error {
	if g.policy == nil {
		return errors.New(policy.OperationDenied)
	}
	for _, agent := range []string{proposerAgent, reviewerAgent} {
		if err := g.check(ctx, event, reviewAccess(agent, "read", policy.Summary), true); err != nil {
			return err
		}
	}
	return nil
}

// The two dummy adapters use private session IDs. Only a counter of reviewed
// rounds is carried between calls; draft and feedback bodies never reach the DB.
func runReview(ctx context.Context, session asyncjobs.ReviewSession, rounds int,
	allow func(stage, agent string, level policy.Level) error) (ReviewSummary, error) {
	if session.ProposerID == "" || session.ReviewerID == "" || session.ProposerID == session.ReviewerID ||
		rounds < 1 || rounds > asyncjobs.MaxReviewRounds {
		return ReviewSummary{}, asyncjobs.ErrInvalidInput
	}
	check := func(stage, agent string, level policy.Level) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return allow(stage, agent, level)
	}
	if session.CompletedRounds > 0 {
		if err := check("review_resume", proposerAgent, policy.Summary); err != nil {
			return ReviewSummary{}, err
		}
	}
	var draft proposal
	summary := ReviewSummary{Rounds: rounds, PreviousRounds: session.CompletedRounds,
		Agreements: []string{}, Differences: []string{}, Unresolved: []string{}}
	for round := 0; round < rounds; round++ {
		if err := check("review_propose", proposerAgent, policy.Full); err != nil {
			return ReviewSummary{}, err
		}
		if round == 0 {
			draft = propose(session.ProposerID, session.CompletedRounds)
		}
		if err := check("review_transfer", reviewerAgent, policy.Full); err != nil {
			return ReviewSummary{}, err
		}
		feedback := critique(session.ReviewerID, draft.answer)
		if feedback.disagreed {
			summary.Differences = append(summary.Differences, "The first proposal was off by one; the review corrected it.")
		}
		if err := check("review_feedback", proposerAgent, policy.Summary); err != nil {
			return ReviewSummary{}, err
		}
		var err error
		draft, err = revise(session.ProposerID, session.ReviewerID, draft, feedback)
		if err != nil {
			return ReviewSummary{}, err
		}
	}
	answer := strconv.Itoa(draft.answer)
	summary.Conclusion = "The checked result is " + answer + "."
	summary.Agreements = append(summary.Agreements, "Both dummy adapters use "+answer+" in the final answer.")
	return summary, nil
}

type proposal struct {
	adapterSession string
	answer         int
}

type critiqueResult struct {
	adapterSession string
	suggestion     int
	disagreed      bool
}

func propose(adapterSession string, previousRounds int) proposal {
	answer := 4
	if previousRounds > 0 {
		answer = 5
	}
	return proposal{adapterSession: adapterSession, answer: answer}
}

func critique(adapterSession string, answer int) critiqueResult {
	// The reviewer solves the fixed problem independently of the draft.
	expected := 2 + 3
	return critiqueResult{adapterSession: adapterSession, suggestion: expected, disagreed: expected != answer}
}

func revise(proposerID, reviewerID string, draft proposal, feedback critiqueResult) (proposal, error) {
	if draft.adapterSession != proposerID || feedback.adapterSession != reviewerID {
		return proposal{}, asyncjobs.ErrInvalidInput
	}
	draft.answer = feedback.suggestion
	return draft, nil
}
