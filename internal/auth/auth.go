package auth

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Config describes the single accepted authentication header.
type Config struct {
	HeaderName string
	Scheme     string
	Token      string
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.HeaderName) == "" {
		return fmt.Errorf("authentication header name is required")
	}
	if strings.TrimSpace(c.Scheme) == "" {
		return fmt.Errorf("authentication scheme is required")
	}
	if len([]byte(c.Token)) < 32 {
		return fmt.Errorf("authentication token must contain at least 32 bytes")
	}
	return nil
}

func (c Config) Middleware(next http.Handler, logger *slog.Logger) http.Handler {
	want := c.value()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		got := r.Header.Get(c.HeaderName)
		if len(got) != len(want) || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			tool := requestToolName(r)
			if logger != nil {
				logger.Info("request rejected", "tool", tool, "outcome", "denied", "job_id", "", "duration_ms", time.Since(started).Milliseconds())
			}
			w.Header().Set("WWW-Authenticate", c.Scheme)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestToolName reads only the MCP method and tool name for safe denial logs.
// The body is restored before returning and is never included in logs.
func requestToolName(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	const maxInspect = 4096
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxInspect))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
	var request struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if json.Unmarshal(body, &request) != nil || request.Method != "tools/call" {
		return ""
	}
	switch request.Params.Name {
	case "ping", "echo", "start_dummy_job", "get_result", "ask", "delegate", "cancel_job", "list_jobs", "list_policy", "discuss", "list_sessions", "propose_policy":
		return request.Params.Name
	default:
		return ""
	}
}

func (c Config) value() string {
	if strings.EqualFold(c.Scheme, "Bearer") {
		return "Bearer " + c.Token
	}
	return c.Token
}
