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

func TestPolicyMCPAndAudit(t *testing.T) {
	const marker = "must-not-be-stored"
	db := filepath.Join(t.TempDir(), "jobs.sqlite")
	jobs, err := asyncjobs.Open(t.Context(), db, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()

	withPolicy := func(read bool, exercise func(call func(string, map[string]any) *mcp.CallToolResult)) {
		t.Helper()
		rules, err := policy.FromConfig(policy.Config{
			Version: 1, Data: map[string]policy.Level{"code": policy.Full, "creative": policy.Summary},
			Execution: policy.Execution{Read: read, Consult: true}, Resources: []string{"arithmetic"},
		})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(NewHandler(auth.Config{HeaderName: "Authorization", Scheme: "Bearer", Token: integrationToken},
			"", slog.New(slog.DiscardHandler), Features{Jobs: jobs, Policy: rules}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		client := mcp.NewClient(&mcp.Implementation{Name: "policy-test", Version: "test"}, nil)
		httpClient := &http.Client{Transport: headerRoundTripper{base: http.DefaultTransport, name: "Authorization", value: "Bearer " + integrationToken}}
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: httpClient, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		exercise(func(name string, args map[string]any) *mcp.CallToolResult {
			t.Helper()
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			if err != nil {
				t.Fatal(err)
			}
			return result
		})
	}
	checkDenied := func(result *mcp.CallToolResult, want string) {
		t.Helper()
		if !result.IsError || len(result.Content) != 1 || result.Content[0].(*mcp.TextContent).Text != want {
			t.Fatalf("expected rejection %s", want)
		}
	}
	var id string
	withPolicy(true, func(call func(string, map[string]any) *mcp.CallToolResult) {
		in := map[string]any{"agent": "dummy", "task": "2 + 3", "context": "", "request_id": "fixture"}
		for _, item := range []struct {
			category []string
			want     string
		}{
			{[]string{"work", "code"}, policy.FixedDeny}, {[]string{marker}, policy.UnknownCategory},
			{[]string{"code", "creative"}, policy.MixedCategories}, {[]string{"creative"}, policy.DisclosureDeny},
		} {
			in["data_category"] = item.category
			checkDenied(call("delegate", in), item.want)
		}
		in["data_category"], in["context"] = []string{"code"}, marker
		checkDenied(call("delegate", in), policy.UnverifiedInput)
		in["context"], in["resource"] = "", "outside"
		checkDenied(call("delegate", in), policy.ResourceDenied)
		delete(in, "resource")
		result := call("delegate", in)
		if result.IsError {
			t.Fatal("verified fixture was rejected")
		}
		id = result.StructuredContent.(map[string]any)["job_id"].(string)
		deadline := time.Now().Add(3 * time.Second)
		for {
			result := call("get_result", map[string]any{"job_id": id})
			if !result.IsError && result.StructuredContent.(map[string]any)["result"] == "5" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("verified result was not returned")
			}
			time.Sleep(30 * time.Millisecond)
		}
		result = call("list_policy", map[string]any{})
		data := result.StructuredContent.(map[string]any)
		if result.IsError || data["grants_enabled"] != false || data["audit"].(map[string]any)["unknown_category"] != float64(1) {
			t.Fatal("policy audit counts or grants status are wrong")
		}
	})
	withPolicy(false, func(call func(string, map[string]any) *mcp.CallToolResult) {
		checkDenied(call("get_result", map[string]any{"job_id": id}), policy.OperationDenied)
		checkDenied(call("list_jobs", map[string]any{}), policy.OperationDenied)
		result := call("cancel_job", map[string]any{"job_id": id})
		if result.IsError || result.StructuredContent.(map[string]any)["result"] != nil {
			t.Fatal("cancel exposed the result despite read being disabled")
		}
	})
	if err := jobs.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(db)
	if err != nil || strings.Contains(string(raw), marker) {
		t.Fatal("untrusted input appeared in the persistent store")
	}
	reopened, err := asyncjobs.Open(t.Context(), db, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	counts, err := reopened.PolicyCounts(t.Context())
	if err != nil || counts.UnknownCategory != 1 || counts.Decisions < 9 {
		t.Fatalf("audit did not persist: %v, %v", counts, err)
	}
}
