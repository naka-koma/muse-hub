package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef"

func TestMiddlewareRequiresExactToken(t *testing.T) {
	cfg := Config{HeaderName: "Authorization", Scheme: "Bearer", Token: testToken}
	called := false
	h := cfg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}), nil)

	for _, tc := range []struct {
		name, header string
		status       int
		wantCalled   bool
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer wrong012345678901234567890123", status: http.StatusUnauthorized},
		{name: "valid", header: "Bearer " + testToken, status: http.StatusNoContent, wantCalled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(""))
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || called != tc.wantCalled {
				t.Fatalf("status=%d called=%v, want status=%d called=%v", w.Code, called, tc.status, tc.wantCalled)
			}
		})
	}
}

func TestAPIKeyHeader(t *testing.T) {
	cfg := Config{HeaderName: "X-Muse-Key", Scheme: "ApiKey", Token: testToken}
	if got := cfg.value(); got != testToken {
		t.Fatalf("value() = %q, want raw API key", got)
	}
}

func TestValidateRequiresLongToken(t *testing.T) {
	if err := (Config{HeaderName: "Authorization", Scheme: "Bearer", Token: "short"}).Validate(); err == nil {
		t.Fatal("Validate() accepted a short token")
	}
}
