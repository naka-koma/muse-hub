package main

import "testing"

func TestLoadConfigRequiresToken(t *testing.T) {
	t.Setenv("MUSE_HUB_TOKEN", "")
	t.Setenv("MUSE_HUB_AUTH_HEADER", "")
	t.Setenv("MUSE_HUB_AUTH_SCHEME", "")
	t.Setenv("MUSE_HUB_PORT", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig() succeeded without a token")
	}
}

func TestListenAddressIsLoopbackOnly(t *testing.T) {
	if got := listenAddress(8787); got != "127.0.0.1:8787" {
		t.Fatalf("listenAddress() = %q, want 127.0.0.1:8787", got)
	}
}
