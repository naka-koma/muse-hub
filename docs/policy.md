# Disclosure policy and audit

English | [日本語](policy.ja.md)

The policy applies to the [asynchronous dummy job](jobs.md). Run the same fixed question throughout this check. No external agent receives the input.

## Load a policy

Save the example as `policy.yaml` outside the repository. Set `MUSE_HUB_POLICY_FILE` to its path before starting the Hub with `MUSE_HUB_JOBS_DB`. Invalid YAML prevents startup.

```yaml
version: 1
data:
  code: full
  creative: summary
  personal: none
agents:
  reviewer:
    code: summary
execution:
  consult: true
  read: true
resources:
  - arithmetic
```

The policy checks fixed prohibitions, allowed operations and resources, then the disclosure level for each piece of data. `work` and `health` are always `none`. Unknown categories and content with inseparable categories are denied. Known levels are `none`, `summary`, and `full`; `summary` requires a summary from a trusted source. A category in a request is only a label. The Hub verifies the fixed fixture independently.

Resource names are logical aliases. They cannot grant filesystem access. `consult` controls jobs; `read` controls their results and history. Shell commands, writes, pushes, and temporary grants are unavailable in this mode.

## Check refusals and audit counts

Follow the [dummy job example](jobs.md#check-delegation-with-a-dummy-job). Then try an unknown category or `work`. Both calls should be denied. Use a fresh `request_id` for a new job. With `read: false`, an otherwise valid result is withheld after the Hub restarts. Cancellation remains available and returns status without the answer.

`list_policy` returns the effective settings, a revision ID, and aggregate audit counts. The database records the timestamp, operation, revision, known category and disclosure level when available, and decision. It omits request text, results, and raw unknown category names from the audit table. The audit count can span multiple revisions.

See [sessions and review](review.md) to check the same policy on transfers between dummy adapters.
