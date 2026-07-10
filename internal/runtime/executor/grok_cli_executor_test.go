package executor

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestGrokCLIExecutor_InjectGrokCLIAttributes(t *testing.T) {
	cfg := &config.Config{}
	exec := NewGrokCLIExecutor(cfg)

	auth := &cliproxyauth.Auth{
		Metadata: map[string]any{
			"access_token": "test-token-123",
		},
	}

	exec.injectGrokCLIAttributes(auth)

	if auth.Attributes["base_url"] != "https://cli-chat-proxy.grok.com/v1" {
		t.Errorf("base_url = %q, want https://cli-chat-proxy.grok.com/v1", auth.Attributes["base_url"])
	}
	if auth.Attributes["header:User-Agent"] != "Grok-CLI/0.2.93 (Linux; x86_64)" {
		t.Errorf("header:User-Agent = %q, want Grok-CLI/0.2.93", auth.Attributes["header:User-Agent"])
	}
	if auth.Attributes["header:X-Grok-Client-Version"] != "0.2.93" {
		t.Errorf("header:X-Grok-Client-Version = %q, want 0.2.93", auth.Attributes["header:X-Grok-Client-Version"])
	}
	if auth.Attributes["api_key"] != "test-token-123" {
		t.Errorf("api_key = %q, want test-token-123", auth.Attributes["api_key"])
	}
}

func TestGrokCLIExecutor_MapModelForUpstream(t *testing.T) {
	cfg := &config.Config{}
	exec := NewGrokCLIExecutor(cfg)

	t.Run("Free Account", func(t *testing.T) {
		auth := &cliproxyauth.Auth{
			Metadata: map[string]any{
				"subscription_tier": "free",
			},
		}
		req := cliproxyexecutor.Request{
			Model:   "grok-build-cli",
			Payload: []byte(`{"model":"grok-build-cli","messages":[]}`),
		}

		exec.mapModelForUpstream(auth, &req)

		if req.Model != "grok-4.5" {
			t.Errorf("req.Model = %q, want grok-4.5", req.Model)
		}
		if !strings.Contains(string(req.Payload), `"model":"grok-4.5"`) {
			t.Errorf("req.Payload = %s, want to contain grok-4.5", string(req.Payload))
		}
	})

	t.Run("SuperGrok Account", func(t *testing.T) {
		auth := &cliproxyauth.Auth{
			Metadata: map[string]any{
				"subscription_tier": "supergrok",
			},
		}
		req := cliproxyexecutor.Request{
			Model:   "grok-build-cli",
			Payload: []byte(`{"model":"grok-build-cli","messages":[]}`),
		}

		exec.mapModelForUpstream(auth, &req)

		if req.Model != "grok-build" {
			t.Errorf("req.Model = %q, want grok-build", req.Model)
		}
		if !strings.Contains(string(req.Payload), `"model":"grok-build"`) {
			t.Errorf("req.Payload = %s, want to contain grok-build", string(req.Payload))
		}
	})
}

func TestGrokCLIExecutor_ClassifyGrokCLIError(t *testing.T) {
	cfg := &config.Config{}
	exec := NewGrokCLIExecutor(cfg)

	t.Run("Quota Limit Failure (Curly Apostrophe)", func(t *testing.T) {
		rawErr := errors.New("You’ve reached your free Grok Build usage limit for now.")
		classified := exec.classifyGrokCLIError(rawErr)

		errObj, ok := classified.(*cliproxyauth.Error)
		if !ok {
			t.Fatalf("expected *cliproxyauth.Error, got %T", classified)
		}
		if errObj.HTTPStatus != http.StatusTooManyRequests {
			t.Errorf("HTTPStatus = %d, want 429", errObj.HTTPStatus)
		}
		if errObj.Code != "quota_exhausted" {
			t.Errorf("Code = %q, want quota_exhausted", errObj.Code)
		}
	})

	t.Run("Quota Limit Failure (ASCII Apostrophe)", func(t *testing.T) {
		rawErr := errors.New("You've reached your free Grok Build usage limit for now.")
		classified := exec.classifyGrokCLIError(rawErr)

		errObj, ok := classified.(*cliproxyauth.Error)
		if !ok {
			t.Fatalf("expected *cliproxyauth.Error, got %T", classified)
		}
		if errObj.HTTPStatus != http.StatusTooManyRequests {
			t.Errorf("HTTPStatus = %d, want 429", errObj.HTTPStatus)
		}
	})

	t.Run("Generic Rate Limit", func(t *testing.T) {
		rawErr := errors.New("upstream rate limit exceeded")
		classified := exec.classifyGrokCLIError(rawErr)

		errObj, ok := classified.(*cliproxyauth.Error)
		if !ok {
			t.Fatalf("expected *cliproxyauth.Error, got %T", classified)
		}
		if errObj.HTTPStatus != http.StatusTooManyRequests {
			t.Errorf("HTTPStatus = %d, want 429", errObj.HTTPStatus)
		}
	})

	t.Run("Other Error", func(t *testing.T) {
		rawErr := errors.New("some other gateway failure")
		classified := exec.classifyGrokCLIError(rawErr)

		if classified != rawErr {
			t.Errorf("expected original error, got %v", classified)
		}
	})
}

func TestGrokCLIExecutor_PrepareRequest(t *testing.T) {
	cfg := &config.Config{}
	exec := NewGrokCLIExecutor(cfg)

	req, _ := http.NewRequest(http.MethodPost, "https://cli-chat-proxy.grok.com/v1/chat/completions", nil)
	auth := &cliproxyauth.Auth{}

	err := exec.PrepareRequest(req, auth)
	if err != nil {
		t.Fatalf("PrepareRequest failed: %v", err)
	}
}
