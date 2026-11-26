# Phase 5 policy proposals and approval

English | [日本語](phase5-approval.ja.md)

Start with the [Phase 3 policy and job database](phase3-policy.md). Set `MUSE_HUB_PHASE5_APPROVALS=1` before starting the Hub. The first start copies the YAML policy into the local database. Later starts use the effective policy from that database. Keep the YAML path configured.

## Propose a change

Call `list_policy`, edit a copy of its `policy` object, and submit the entire object as the `candidate` to `propose_policy`. For example, start with `consult: false` and propose enabling synthetic consultation:

```json
{
  "reason": "review_access",
  "candidate": {
    "version": 1,
    "data": {"code": "full", "work": "none", "health": "none"},
    "execution": {"read": true, "consult": true},
    "resources": ["arithmetic"]
  }
}
```

The reason is a code: `reduce_disclosure`, `review_access`, or `maintenance`. The response contains a pending proposal ID, candidate revision, and expiry. Proposals expire after 24 hours. Only proposals that do not expand any permission can be applied automatically at startup. The decision is recorded as `auto_applied`.

## Review and decide locally

Stop the Hub first. On the PC that owns the job database, keep `MUSE_HUB_PHASE2_DB` pointed at that file. Review the current policy and the proposed candidate before deciding.

```powershell
go run ./cmd/muse-hub policy current
go run ./cmd/muse-hub policy list
go run ./cmd/muse-hub policy approve PROPOSAL_ID CANDIDATE_REVISION
```

Replace the placeholders with the values you reviewed. Use `policy reject` with the same arguments to reject it. Approval checks the proposal ID, candidate revision, expiry, and active policy. Applying one proposal supersedes other proposals based on the previous policy. Restart the Hub to serve the new policy. Affected review sessions stay sealed even if an older policy is restored.

The approval command has no MCP endpoint. It requires local access to the database and will fail while the Hub holds the database lock. Keep the database outside the repository and out of adapter mounts. Temporary job grants remain disabled.

## Weekly review

```powershell
go run ./cmd/muse-hub policy weekly
```

This command summarizes the last seven days of proposal and audit decisions. The summary contains counts, time bounds, and the active revision. Proposal history is available through `policy list`.
