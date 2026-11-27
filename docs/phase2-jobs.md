# Phase 2 asynchronous job check

English | [日本語](phase2-jobs.ja.md)

Phase 2 checks asynchronous jobs with a fixed synthetic question. The worker runs inside the Hub and returns `5` after about a second. It does not call an external agent.

## Start the Hub

Set `MUSE_HUB_TOKEN` as described in the [README](../README.md). In the same PowerShell session, set the path to a local SQLite file outside the repository and start the Hub.

```powershell
$jobDir = Join-Path $env:LOCALAPPDATA 'MuseHub'
New-Item -ItemType Directory -Path $jobDir -Force | Out-Null
$env:MUSE_HUB_PHASE2_DB = Join-Path $jobDir 'phase2-jobs.sqlite'
go run ./cmd/muse-hub
```

Leave `MUSE_HUB_PHASE1_AUTH_VOLUME` unset. Phase 2 provides `ping` and `echo` alongside `delegate`, `get_result`, `cancel_job`, and `list_jobs`. Use the [Tailscale Serve instructions](usage.md#tailscale-serve) to connect from Muse.

## Call the tools

Call `delegate` with these arguments. Generate a new opaque `request_id` for each new job and reuse it only when retrying the same request. The optional `session` limits that session to one active job.

```json
{"agent":"dummy","task":"2 + 3","context":"","data_category":["code"],"request_id":"sample-1","session":"demo"}
```

`delegate` returns a `job_id` immediately. On a later turn, pass it to `get_result`. A completed job has `status: "succeeded"` and `result: "5"`. `cancel_job` takes the same ID and reports cancellation once the worker has stopped. `list_jobs` returns the 100 most recent summaries without result text.

- At most five jobs can be active at a time. An overlapping request in the same session returns `session_busy`. Reusing a request ID for a different request returns `request_id_conflict`.
- A job times out after 30 seconds. Queued or running jobs found on startup are marked `interrupted`. Submit a new request ID to run the same fixture again.
- SQLite stores job IDs, timestamps, status, the synthetic result, and hashes used to detect retries. The original request ID, session ID, and input text are not stored. Finished jobs and their retry records are removed after seven days during normal access or startup.

## Verify child process cancellation on Windows

Set `MUSE_HUB_PHASE2_SUBPROCESS=1` before starting the Hub with the job database. This uses the same fixture and runs for about five seconds. The Hub starts a fixed helper and its child inside a Windows Job Object. No tool argument is used as a command, and the helper does not inherit the Hub token.

Submit the fixture with a fresh `request_id` and call `cancel_job` with the returned ID while it is running. `cancelled` means that the helper waited for its child and the Hub confirmed that the process tree stopped. If the Hub cannot confirm the stop, the job reports `failed`. Turning off this setting restores the in-process worker.
