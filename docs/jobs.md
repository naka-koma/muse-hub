# Delegation and job results

English | [日本語](jobs.ja.md)

Use `delegate` to submit a job and `get_result` to retrieve its answer. Enable Claude or Codex to send jobs to those agents. The built-in dummy worker returns `5` for a test question.

## Start the Hub

Set `MUSE_HUB_TOKEN` as described in the [README](../README.md). In the same PowerShell session, set the path to a local SQLite file outside the repository and start the Hub.

```powershell
$jobDir = Join-Path $env:LOCALAPPDATA 'MuseHub'
New-Item -ItemType Directory -Path $jobDir -Force | Out-Null
$env:MUSE_HUB_JOBS_DB = Join-Path $jobDir 'muse-jobs.sqlite'
go run ./cmd/muse-hub
```

Leave `MUSE_HUB_CLAUDE_ASK_VOLUME` unset to use `delegate`. Follow the [Tailscale Serve instructions](usage.md#tailscale-serve) to connect from Muse.

## Check delegation with a dummy job

Call `delegate` with these arguments. Generate a new opaque `request_id` for each new job and reuse it only when retrying the same request. The optional `session` limits that session to one active job.

```json
{"agent":"dummy","task":"2 + 3","context":"","data_category":["code"],"request_id":"sample-1","session":"demo"}
```

`delegate` returns a `job_id` immediately. On a later turn, pass it to `get_result`. A completed job has `status: "succeeded"` and `result: "5"`. `cancel_job` takes the same ID and reports cancellation once the worker has stopped. `list_jobs` returns the 100 most recent summaries without result text.

- At most five jobs can be active at a time. An overlapping request in the same session returns `session_busy`. Reusing a request ID for a different request returns `request_id_conflict`.
- A job times out after 30 seconds. Queued or running jobs found on startup are marked `interrupted`. Submit a new request ID to run the same fixture again.
- SQLite stores job IDs, timestamps, status, results, agent names, and hashes used to detect retries. The original request ID, session ID, and input text are not stored. Finished jobs and their retry records are removed after seven days during normal access or startup.

## Verify child process cancellation on Windows

Set `MUSE_HUB_DUMMY_SUBPROCESS=1` before starting the Hub with the job database. This uses the same fixture and runs for about five seconds. The Hub starts a fixed helper and its child inside a Windows Job Object. No tool argument is used as a command, and the helper does not inherit the Hub token.

Submit the fixture with a fresh `request_id` and call `cancel_job` with the returned ID while it is running. `cancelled` means that the helper waited for its child and the Hub confirmed that the process tree stopped. If the Hub cannot confirm the stop, the job reports `failed`. Turning off this setting restores the in-process worker.

## Check Codex delegation on Windows

Create a dedicated Codex home outside the repository and sign in to Codex there. Use `codex login` with `CODEX_HOME` set to that directory in a separate PowerShell session. Set `MUSE_HUB_CODEX_HOME` to the same directory and `MUSE_HUB_CODEX_EXE` to the absolute path to the native `codex.exe` before starting the Hub with the job database.

Call `delegate` with `agent: "codex"`, `task: "2 + 3"`, empty `context`, `data_category: ["code"]`, and a fresh `request_id`. Use the returned ID with `get_result` and expect `succeeded` with result `"5"`. The Hub sends a fixed arithmetic prompt, runs Codex with tools such as shell and web search disabled, and accepts only the expected answer. When using a policy, allow the `code` category and `arithmetic` resource for Codex.

### Send text and context to Codex

With Codex configured, set `MUSE_HUB_CODEX_TEXT=1` before starting a Hub with no disclosure policy. Submit a nonempty `task` of up to 4 KiB and optional reference `context` of up to 16 KiB, measured in UTF-8 bytes. Keep `data_category: ["code"]` and use a fresh `request_id` when either field changes.

```json
{"agent":"codex","task":"Summarize the context in one sentence.","context":"The sample build took 2 minutes. The previous build took 5 minutes.","data_category":["code"],"request_id":"summary-1"}
```

Submit only text you can share with Codex. The CLI gets the text over standard input and uses the same restricted tool setup as the arithmetic check. The Hub saves the reply for `get_result`, with a limit of 16 KiB. A Hub with a disclosure policy accepts only the fixed probe and withholds results from earlier free-text requests.

## Delegate to Claude

Prepare the [isolated Claude container](claude-isolation.md) and keep its Compose proxy running. To enable asynchronous Claude jobs, configure `MUSE_HUB_CLAUDE_AUTH_VOLUME` and the job database. Set `MUSE_HUB_CLAUDE_TEXT=1` to allow tasks and context on a Hub without a disclosure policy. The input and output limits are the same as for Codex. Claude runs one job at a time.

Use `agent: "claude"` with `delegate`, then pass its job ID to `get_result`. The adapter sends prompts over standard input, disables Claude's tools and connectors, and removes the job container when finished or cancelled.
