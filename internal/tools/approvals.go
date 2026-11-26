package tools

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nakakoma/muse-hub/internal/policy"
)

type ProposePolicyInput struct {
	Candidate policy.Config `json:"candidate" jsonschema:"complete replacement policy reviewed by the local operator"`
	Reason    string        `json:"reason" jsonschema:"one of reduce_disclosure, review_access, or maintenance"`
}

type ProposePolicyOutput struct {
	ProposalID string    `json:"proposal_id"`
	Revision   string    `json:"revision"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (g policyGate) registerProposals(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "propose_policy", Description: "Submit a pending policy proposal for the operator to review locally. This tool cannot approve or apply changes.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ProposePolicyInput) (*mcp.CallToolResult, ProposePolicyOutput, error) {
		candidate, err := policy.FromConfig(input.Candidate)
		if err != nil {
			return nil, ProposePolicyOutput{}, err
		}
		proposal, err := g.manager.ProposePolicy(ctx, g.policy.Revision(), input.Reason, candidate)
		if err != nil {
			return nil, ProposePolicyOutput{}, err
		}
		return nil, ProposePolicyOutput{ProposalID: proposal.ID, Revision: proposal.Revision, Status: proposal.Status, ExpiresAt: proposal.ExpiresAt}, nil
	})
}
