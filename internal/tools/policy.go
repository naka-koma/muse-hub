package tools

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nakakoma/muse-hub/internal/asyncjobs"
	"github.com/nakakoma/muse-hub/internal/policy"
)

const arithmeticResource = "arithmetic"

type PolicyOutput struct {
	Revision      string                 `json:"revision"`
	Policy        policy.Config          `json:"policy"`
	Audit         asyncjobs.PolicyCounts `json:"audit"`
	GrantsEnabled bool                   `json:"grants_enabled"`
}

type policyGate struct {
	policy  *policy.Policy
	manager *asyncjobs.Manager
}

func (g policyGate) check(ctx context.Context, event string, access policy.Access, verified bool) error {
	if g.policy == nil {
		return nil
	}
	decision := g.policy.Check(access)
	if decision.Allowed() && !verified {
		decision = policy.Decision{Code: policy.UnverifiedInput}
	}
	if decision.Allowed() && len(access.Portions) == 1 {
		// The caller established that a verified portion has exactly one known category.
		decision.Category = access.Portions[0].Categories[0]
		decision.Level = access.Portions[0].Level
	}
	if err := g.manager.RecordPolicy(ctx, asyncjobs.PolicyAudit{
		Event: event, Revision: g.policy.Revision(), Category: decision.Category,
		Level: string(decision.Level), Outcome: decision.Code,
	}); err != nil {
		return err
	}
	if !decision.Allowed() {
		return errors.New(decision.Code)
	}
	return nil
}

func (g policyGate) register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_policy", Description: "Return the effective policy and aggregate audit counts.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, PolicyOutput, error) {
		counts, err := g.manager.PolicyCounts(ctx)
		if err != nil {
			return nil, PolicyOutput{}, err
		}
		return nil, PolicyOutput{Revision: g.policy.Revision(), Policy: g.policy.Config(), Audit: counts}, nil
	})
}

func arithmeticRead(agent string) policy.Access {
	return policy.Access{Agent: agent, Operation: "read", Resource: arithmeticResource,
		Portions: []policy.Portion{{Categories: []string{"code"}, Level: policy.Full}}}
}
