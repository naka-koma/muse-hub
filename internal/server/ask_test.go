package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nakakoma/muse-hub/internal/auth"
	"github.com/nakakoma/muse-hub/internal/tools"
)

func TestAskWithStubAdapter(t *testing.T) {
	const contextMarker = "DUMMY_CONTEXT_MARKER"
	const adapterErrorMarker = "DUMMY_ADAPTER_ERROR_MARKER"
	var calls atomic.Int32
	seen := make(chan tools.AskInput, 1)
	timedOut := make(chan error, 1)
	ask, err := tools.NewAsk("codex", 200*time.Millisecond, func(ctx context.Context, input tools.AskInput) (string, error) {
		calls.Add(1)
		switch input.Task {
		case "dummy success":
			seen <- input
			return "dummy answer", nil
		case "dummy timeout":
			<-ctx.Done()
			timedOut <- ctx.Err()
			return "late answer", nil
		default:
			return "", errors.New(adapterErrorMarker)
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	config := auth.Config{HeaderName: "Authorization", Scheme: "Bearer", Token: integrationToken}
	srv := httptest.NewServer(NewHandler(config, "", logger, Features{Ask: ask}))
	defer srv.Close()
	unauthenticated, err := srv.Client().Post(srv.URL+"/mcp", "application/json", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ask","arguments":{"task":"`+contextMarker+`"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized || calls.Load() != 0 {
		t.Fatal("an unauthenticated request reached ask")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpClient := &http.Client{Transport: headerRoundTripper{base: http.DefaultTransport, name: "Authorization", value: "Bearer " + integrationToken}}
	client := mcp.NewClient(&mcp.Implementation{Name: "ask-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: httpClient, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil || !slices.ContainsFunc(listed.Tools, func(tool *mcp.Tool) bool { return tool.Name == "ask" }) {
		t.Fatalf("ask not listed: %v", err)
	}

	call := func(agent, task string, categories []string) (*mcp.CallToolResult, error) {
		return session.CallTool(ctx, &mcp.CallToolParams{Name: "ask", Arguments: tools.AskInput{
			Agent: agent, Task: task, Context: contextMarker, DataCategory: categories,
		}})
	}
	for _, input := range []struct {
		agent, task string
		categories  []string
	}{
		{"claude", "dummy success", []string{"code"}},
		{"codex", " ", []string{"code"}},
		{"codex", "dummy success", []string{}},
		{"codex", "dummy success", []string{" "}},
	} {
		result, err := call(input.agent, input.task, input.categories)
		if err != nil || !result.IsError || calls.Load() != 0 {
			t.Fatalf("invalid input was not rejected before calling the adapter: %v", err)
		}
	}

	categories := []string{"code", "creative"}
	result, err := call("codex", "dummy success", categories)
	if err != nil || result.IsError {
		t.Fatalf("ask failed: %v", err)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured["answer"] != "dummy answer" {
		t.Fatalf("unexpected ask answer: %v", result.StructuredContent)
	}
	select {
	case input := <-seen:
		if input.Task != "dummy success" || input.Context != contextMarker || !slices.Equal(input.DataCategory, categories) {
			t.Fatal("adapter did not receive the separate task, context, and categories")
		}
	default:
		t.Fatal("adapter did not receive the request")
	}

	for _, tc := range []struct{ task, want string }{
		{"dummy failure", "ask adapter failed"},
		{"dummy timeout", "ask timed out"},
	} {
		result, err := call("codex", tc.task, categories)
		if err != nil || !result.IsError || len(result.Content) != 1 {
			t.Fatalf("expected an ask tool error: %v", err)
		}
		message, ok := result.Content[0].(*mcp.TextContent)
		if !ok || message.Text != tc.want {
			t.Fatalf("unexpected tool error for %s", tc.task)
		}
	}
	select {
	case err := <-timedOut:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unexpected adapter cancellation: %v", err)
		}
	default:
		t.Fatal("deadline was not passed to adapter")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	for _, private := range []string{contextMarker, adapterErrorMarker, integrationToken, "dummy success"} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("ask request or adapter data leaked to the log")
		}
	}
}
