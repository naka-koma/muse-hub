package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nakakoma/muse-hub/internal/auth"
)

const integrationToken = "0123456789abcdef0123456789abcdef"

func TestTailscaleHost(t *testing.T) {
	const authority = "node.example.ts.net:9443"
	config := auth.Config{HeaderName: "Authorization", Scheme: "Bearer", Token: integrationToken}
	srv := httptest.NewServer(NewHandler(config, authority, slog.New(slog.DiscardHandler)))
	defer srv.Close()

	for _, tc := range []struct {
		name, host, origin string
		authenticated      bool
		want               int
	}{
		{"localhost", "", "", true, http.StatusOK},
		{"configured host", "NODE.example.ts.net:9443", "", true, http.StatusOK},
		{"different host", "other.example.ts.net:9443", "", true, http.StatusForbidden},
		{"different port", "node.example.ts.net:8443", "", true, http.StatusForbidden},
		{"cross origin", authority, "https://other.example.ts.net", true, http.StatusForbidden},
		{"missing token", authority, "", false, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const init = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
			req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(init))
			if err != nil {
				t.Fatal(err)
			}
			if tc.host != "" {
				req.Host = tc.host
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.authenticated {
				req.Header.Set("Authorization", "Bearer "+integrationToken)
			}
			res, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != tc.want {
				t.Fatalf("status=%d, want %d", res.StatusCode, tc.want)
			}
			if tc.want == http.StatusOK && res.Header.Get("Mcp-Session-Id") == "" {
				t.Fatal("missing MCP session ID")
			}
		})
	}
}

type headerRoundTripper struct {
	base       http.RoundTripper
	name       string
	value      string
	extraName  string
	extraValue string
}

func (r headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header.Set(r.name, r.value)
	if r.extraName != "" {
		copy.Header.Set(r.extraName, r.extraValue)
	}
	return r.base.RoundTrip(copy)
}

func TestAcceptanceToolsAndAuthorization(t *testing.T) {
	var logOutput strings.Builder
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	config := auth.Config{HeaderName: "Authorization", Scheme: "Bearer", Token: integrationToken}
	httpServer := httptest.NewServer(NewHandler(config, "", logger))
	defer httpServer.Close()

	unauthorized, err := http.Post(httpServer.URL+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","arguments":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d, want 401", unauthorized.StatusCode)
	}

	wrongToken := "wrong012345678901234567890123"
	wrongClient := &http.Client{Transport: headerRoundTripper{base: http.DefaultTransport, name: "Authorization", value: "Bearer " + wrongToken, extraName: "X-Test-Secret", extraValue: "HEADER_SECRET_SENTINEL"}}
	wrong, err := wrongClient.Post(httpServer.URL+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","arguments":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token status=%d, want 401", wrong.StatusCode)
	}

	httpClient := &http.Client{Transport: headerRoundTripper{base: http.DefaultTransport, name: "Authorization", value: "Bearer " + integrationToken}}
	client := mcp.NewClient(&mcp.Implementation{Name: "muse-hub-test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", HTTPClient: httpClient, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 4 {
		t.Fatalf("tools count=%d, want 4", len(listed.Tools))
	}
	for _, name := range []string{"ping", "echo", "start_dummy_job", "get_result"} {
		found := false
		for _, tool := range listed.Tools {
			if tool.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("tool %q not listed", name)
		}
	}

	call := func(name string, args map[string]any) (*mcp.CallToolResult, error) {
		t.Helper()
		return session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	}
	if _, err := call("ping", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := call("echo", map[string]any{"text": "DO_NOT_LOG_SENTINEL"}); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1000, 1001} {
		result, err := call("echo", map[string]any{"text": strings.Repeat("x", size)})
		if err != nil {
			t.Fatalf("echo(%d): %v", size, err)
		}
		if result.IsError != (size > 1000) {
			t.Errorf("echo(%d) IsError=%v", size, result.IsError)
		}
	}
	for _, delay := range []any{0, 61, -1, "1", 1.5} {
		result, err := call("start_dummy_job", map[string]any{"delay_seconds": delay})
		if err != nil {
			t.Fatalf("start_dummy_job(%v) returned protocol error: %v", delay, err)
		}
		if !result.IsError {
			t.Errorf("start_dummy_job(%v) unexpectedly succeeded", delay)
		}
	}

	started, err := call("start_dummy_job", map[string]any{"delay_seconds": 1})
	if err != nil || started.IsError {
		t.Fatalf("start_dummy_job: result=%v err=%v", started, err)
	}
	var created struct {
		JobID string `json:"job_id"`
	}
	if err := decodeStructured(started.StructuredContent, &created); err != nil {
		t.Fatal(err)
	}
	quick, err := call("get_result", map[string]any{"job_id": created.JobID})
	if err != nil || quick.IsError {
		t.Fatalf("early get_result: result=%v err=%v", quick, err)
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := decodeStructured(quick.StructuredContent, &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "queued" && status.Status != "running" {
		t.Fatalf("early status=%q, want queued or running", status.Status)
	}
	time.Sleep(1100 * time.Millisecond)
	completed, err := call("get_result", map[string]any{"job_id": created.JobID})
	if err != nil || completed.IsError {
		t.Fatalf("completed get_result: result=%v err=%v", completed, err)
	}
	var completedResult struct {
		Status string `json:"status"`
		Result struct {
			Message     string    `json:"message"`
			StartedAt   time.Time `json:"started_at"`
			CompletedAt time.Time `json:"completed_at"`
		} `json:"result"`
	}
	if err := decodeStructured(completed.StructuredContent, &completedResult); err != nil {
		t.Fatal(err)
	}
	if completedResult.Status != "succeeded" || completedResult.Result.Message == "" || completedResult.Result.CompletedAt.IsZero() {
		t.Fatalf("incomplete result: %+v", completedResult)
	}

	missing, err := call("get_result", map[string]any{"job_id": "00000000000000000000000000000000"})
	missingText := ""
	if len(missing.Content) > 0 {
		if text, ok := missing.Content[0].(*mcp.TextContent); ok {
			missingText = text.Text
		}
	}
	if err != nil || !missing.IsError || !strings.Contains(missingText, "job_not_found") {
		t.Fatalf("unknown job result=%v err=%v, want job_not_found", missing, err)
	}

	ids := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		result, err := call("start_dummy_job", map[string]any{"delay_seconds": 60})
		if err != nil || result.IsError {
			t.Fatalf("start job %d of capacity: result=%v err=%v", i+1, result, err)
		}
		var out struct {
			JobID string `json:"job_id"`
		}
		if err := decodeStructured(result.StructuredContent, &out); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, out.JobID)
	}
	sixth, err := call("start_dummy_job", map[string]any{"delay_seconds": 60})
	if err != nil || !sixth.IsError {
		t.Fatalf("sixth concurrent job result=%v err=%v, want tool error", sixth, err)
	}
	_ = ids
	if strings.Contains(logOutput.String(), integrationToken) || strings.Contains(logOutput.String(), wrongToken) || strings.Contains(logOutput.String(), "HEADER_SECRET_SENTINEL") || strings.Contains(logOutput.String(), "DO_NOT_LOG_SENTINEL") {
		t.Fatal("logs included a token, header value, or echo input")
	}
	if !strings.Contains(logOutput.String(), "tool=ping") || !strings.Contains(logOutput.String(), "outcome=denied") {
		t.Fatal("authentication denial log omitted the tool name or denied outcome")
	}
}

func decodeStructured(value any, into any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}
