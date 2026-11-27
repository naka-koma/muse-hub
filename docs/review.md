# Sessions and review

English | [日本語](review.ja.md)

Start the Hub with a [disclosure policy](policy.md) that permits `read` and `consult` for `arithmetic` and allows `code: full` for both dummy adapters. The review runs entirely inside the Hub.

## Start a review

Call `discuss` with the following fixture. The proposer supplies a draft, the reviewer checks it, and the proposer applies the feedback. The Hub checks the receiving agent's policy before each handoff. One job can run one to three rounds.

```json
{"agents":["dummy_proposer","dummy_reviewer"],"topic":"2 + 3","context":"","data_category":["code"],"rounds":2,"mode":"review","request_id":"review-1"}
```

The response contains `job_id` and the Hub's `session_id`. Pass `job_id` to `get_result` to retrieve `review`, which contains the conclusion, agreements, differences, unresolved items, and round counts. The database stores the final summary and records intermediate decisions as audit metadata. Draft and feedback bodies are discarded. Current policy controls access to the stored summary.

## Continue a session

Call `list_sessions` to find recent sessions and check `resumable`. Repeat the fixture with a new `request_id` and the earlier `session_id` to continue it. The review receives the previously completed round count. Private adapter session IDs remain in the local database.

Loading a different policy revision seals older sessions. Restoring an earlier revision does not reopen them. A session marked as having used a temporary grant also requires a new session. Grants are disabled in the current implementation.

To verify a transfer refusal, change the reviewer override to `dummy_reviewer: {code: summary}` and restart the Hub. A new review now fails at the first full-draft transfer, and no review summary is returned.
