package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nakakoma/muse-hub/internal/asyncjobs"
	"github.com/nakakoma/muse-hub/internal/auth"
	"github.com/nakakoma/muse-hub/internal/policy"
)

func TestReviewPolicyAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	manager, err := asyncjobs.Open(t.Context(), path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	newPolicy := func(level policy.Level) *policy.Policy {
		t.Helper()
		rules, err := policy.FromConfig(policy.Config{Version: 1, Data: map[string]policy.Level{"code": policy.Full},
			Agents:    map[string]map[string]policy.Level{"dummy_reviewer": {"code": level}},
			Execution: policy.Execution{Read: true, Consult: true}, Resources: []string{"arithmetic"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.InvalidateReviewSessions(t.Context(), rules.Revision()); err != nil {
			t.Fatal(err)
		}
		return rules
	}
	withPolicy := func(rules *policy.Policy, exercise func(func(string, map[string]any) (*mcp.CallToolResult, map[string]any))) {
		t.Helper()
		httpServer := httptest.NewServer(NewHandler(auth.Config{HeaderName: "Authorization", Scheme: "Bearer", Token: integrationToken},
			"", slog.New(slog.DiscardHandler), Features{Jobs: manager, Policy: rules}))
		defer httpServer.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		client := mcp.NewClient(&mcp.Implementation{Name: "review-test", Version: "test"}, nil)
		httpClient := &http.Client{Transport: headerRoundTripper{base: http.DefaultTransport, name: "Authorization", value: "Bearer " + integrationToken}}
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", HTTPClient: httpClient, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		exercise(func(name string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
			t.Helper()
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			if err != nil {
				t.Fatal(err)
			}
			data, _ := result.StructuredContent.(map[string]any)
			return result, data
		})
	}
	type callFunc = func(string, map[string]any) (*mcp.CallToolResult, map[string]any)
	wait := func(call callFunc, id string, want string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			result, data := call("get_result", map[string]any{"job_id": id})
			if !result.IsError && data["status"] == want {
				return data
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("job did not reach %s", want)
		return nil
	}
	discuss := map[string]any{"agents": []string{"dummy_proposer", "dummy_reviewer"}, "topic": "2 + 3",
		"context": "", "data_category": []string{"code"}, "rounds": 2, "mode": "review", "request_id": "review-first"}
	var sessionID, firstJob string
	withPolicy(newPolicy(policy.Full), func(call callFunc) {
		discuss["rounds"] = 4
		if result, _ := call("discuss", discuss); !result.IsError {
			t.Fatal("unbounded rounds accepted")
		}
		discuss["rounds"] = 2
		result, data := call("discuss", discuss)
		if result.IsError {
			t.Fatal("review was rejected")
		}
		firstJob, sessionID = data["job_id"].(string), data["session_id"].(string)
		got := wait(call, firstJob, "succeeded")
		summary := got["review"].(map[string]any)
		if got["result"] != nil || summary["conclusion"] != "The checked result is 5." ||
			len(summary["differences"].([]any)) != 1 || summary["rounds"] != float64(2) {
			t.Fatal("unexpected review summary")
		}
		_, data = call("list_sessions", map[string]any{})
		listed := data["sessions"].([]any)[0].(map[string]any)
		if listed["session_id"] != sessionID || listed["completed_rounds"] != float64(2) ||
			listed["resumable"] != true || len(listed) != 5 {
			t.Fatal("incorrect public session fields")
		}
		discuss["session_id"], discuss["request_id"] = sessionID, "review-next"
		_, data = call("discuss", discuss)
		got = wait(call, data["job_id"].(string), "succeeded")
		if got["review"].(map[string]any)["previous_rounds"] != float64(2) {
			t.Fatal("previous completed rounds were not carried forward")
		}
	})
	withPolicy(newPolicy(policy.Summary), func(call callFunc) {
		discuss["request_id"] = "blocked-session"
		if result, _ := call("discuss", discuss); !result.IsError || result.Content[0].(*mcp.TextContent).Text != "session_restart_required" {
			t.Fatal("changed policy allowed session resume")
		}
		delete(discuss, "session_id")
		discuss["request_id"] = "blocked-transfer"
		_, data := call("discuss", discuss)
		if got := wait(call, data["job_id"].(string), "failed"); got["review"] != nil {
			t.Fatal("the reviewer received full data with summary-only permission")
		}
	})
	withPolicy(nil, func(call callFunc) {
		if result, _ := call("get_result", map[string]any{"job_id": firstJob}); !result.IsError {
			t.Fatal("turning off policy exposed a review result")
		}
		_, data := call("list_jobs", map[string]any{})
		if len(data["jobs"].([]any)) != 0 {
			t.Fatal("turning off policy exposed review metadata")
		}
	})
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(stored), "2 + 3") || !strings.Contains(string(stored), "review_transfer") {
		t.Fatal("database did not retain transfer metadata or retained the input")
	}
}
