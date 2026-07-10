package executor

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	xaiauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/xai"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type GrokCLIExecutor struct {
	cfg     *config.Config
	xaiExec *XAIExecutor
}

func NewGrokCLIExecutor(cfg *config.Config) *GrokCLIExecutor {
	return &GrokCLIExecutor{
		cfg:     cfg,
		xaiExec: NewXAIExecutor(cfg),
	}
}

func (e *GrokCLIExecutor) Identifier() string {
	return "grok-cli"
}

func (e *GrokCLIExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	auth = e.cloneAuth(auth)
	e.injectGrokCLIAttributes(auth)
	return e.xaiExec.PrepareRequest(req, auth)
}

func (e *GrokCLIExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	auth = e.cloneAuth(auth)
	e.injectGrokCLIAttributes(auth)
	return e.xaiExec.HttpRequest(ctx, auth, req)
}

func (e *GrokCLIExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	auth = e.cloneAuth(auth)
	e.injectGrokCLIAttributes(auth)
	e.mapModelForUpstream(auth, &req)
	e.stripUnsupportedParams(&req)
	e.injectWebSearchTool(&req)

	resp, err := e.xaiExec.Execute(ctx, auth, req, opts)
	if err != nil {
		return resp, e.classifyGrokCLIError(err)
	}
	return resp, nil
}

func (e *GrokCLIExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	auth = e.cloneAuth(auth)
	e.injectGrokCLIAttributes(auth)
	e.mapModelForUpstream(auth, &req)
	e.stripUnsupportedParams(&req)
	e.injectWebSearchTool(&req)

	res, err := e.xaiExec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		return res, e.classifyGrokCLIError(err)
	}
	return res, nil
}

func (e *GrokCLIExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	auth = e.cloneAuth(auth)
	e.injectGrokCLIAttributes(auth)
	e.mapModelForUpstream(auth, &req)
	e.stripUnsupportedParams(&req)
	return e.xaiExec.CountTokens(ctx, auth, req, opts)
}

// injectWebSearchTool ensures the request includes built-in search tools in the tools array.
// Function-format web_search/x_search (from OpenAI-compatible clients) are converted to
// built-in format. Other tools are left untouched.
func (e *GrokCLIExecutor) injectWebSearchTool(req *cliproxyexecutor.Request) {
	if req == nil || len(req.Payload) == 0 {
		return
	}

	// If tools exists but is not an array (e.g. null, string, number), delete it first.
	toolsVal := gjson.GetBytes(req.Payload, "tools")
	if toolsVal.Exists() && !toolsVal.IsArray() {
		if updated, err := sjson.DeleteBytes(req.Payload, "tools"); err == nil {
			req.Payload = updated
		}
	}

	// Rebuild tools array: strip function-format web_search/x_search, keep everything else.
	hasBuiltinWebSearch := false
	hasBuiltinXSearch := false
	newTools := []byte(`[]`)

	tools := gjson.GetBytes(req.Payload, "tools")
	if tools.IsArray() {
		for _, t := range tools.Array() {
			toolType := t.Get("type").String()
			funcName := t.Get("function.name").String()
			topName := t.Get("name").String()

			// Already built-in format — keep and mark as present
			if toolType == "web_search" {
				hasBuiltinWebSearch = true
				newTools, _ = sjson.SetRawBytes(newTools, "-1", []byte(t.Raw))
				continue
			}
			if toolType == "x_search" {
				hasBuiltinXSearch = true
				newTools, _ = sjson.SetRawBytes(newTools, "-1", []byte(t.Raw))
				continue
			}

			// Function-format web_search/x_search → convert to built-in later, drop here
			if funcName == "web_search" || topName == "web_search" {
				hasBuiltinWebSearch = true
				continue
			}
			if funcName == "x_search" || topName == "x_search" {
				hasBuiltinXSearch = true
				continue
			}

			// Keep all other tools unchanged
			newTools, _ = sjson.SetRawBytes(newTools, "-1", []byte(t.Raw))
		}
	}

	// Add built-in tools if not already present
	if !hasBuiltinWebSearch {
		newTools, _ = sjson.SetRawBytes(newTools, "-1", []byte(`{"type":"web_search"}`))
	}
	if !hasBuiltinXSearch {
		newTools, _ = sjson.SetRawBytes(newTools, "-1", []byte(`{"type":"x_search"}`))
	}

	if updated, err := sjson.SetRawBytes(req.Payload, "tools", newTools); err == nil {
		req.Payload = updated
	}
}

// stripUnsupportedParams removes OpenAI-specific parameters that the Grok CLI gateway does not accept.
// Grok API rejects presence_penalty, frequency_penalty, and other OpenAI-only fields with a 400 error.
func (e *GrokCLIExecutor) stripUnsupportedParams(req *cliproxyexecutor.Request) {
	if req == nil || len(req.Payload) == 0 {
		return
	}
	// Parameters Grok CLI gateway does not support (confirmed via xAI API docs)
	// presence_penalty and frequency_penalty are explicitly marked "NOT SUPPORTED"
	unsupported := []string{
		"presence_penalty",
		"frequency_penalty",
	}
	for _, key := range unsupported {
		if updated, err := sjson.DeleteBytes(req.Payload, key); err == nil {
			req.Payload = updated
		}
	}
}

// Refresh handles OAuth credential rotation for xAI using its PKCE loop.
func (e *GrokCLIExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	log.Debugf("grok-cli executor: refresh called")
	if refreshed, handled, err := helps.RefreshAuthViaHome(ctx, e.cfg, auth); handled {
		return refreshed, err
	}
	if auth == nil {
		return nil, fmt.Errorf("grok-cli executor: auth is nil")
	}
	refreshToken := e.metadataString(auth.Metadata, "refresh_token")
	if refreshToken == "" {
		return auth, nil
	}
	tokenEndpoint := e.metadataString(auth.Metadata, "token_endpoint")

	svc := xaiauth.NewXAIAuthWithProxyURL(e.cfg, auth.ProxyURL)
	td, err := svc.RefreshTokens(ctx, refreshToken, tokenEndpoint)
	if err != nil {
		return nil, err
	}

	tier := util.ProbeSubscriptionTier(ctx, td.AccessToken)

	auth = e.cloneAuth(auth)

	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["type"] = "grok-cli"
	auth.Metadata["auth_kind"] = "oauth"
	auth.Metadata["access_token"] = td.AccessToken
	auth.Metadata["subscription_tier"] = tier
	if td.RefreshToken != "" {
		auth.Metadata["refresh_token"] = td.RefreshToken
	}
	if td.IDToken != "" {
		auth.Metadata["id_token"] = td.IDToken
	}
	if td.TokenType != "" {
		auth.Metadata["token_type"] = td.TokenType
	}
	if td.ExpiresIn > 0 {
		auth.Metadata["expires_in"] = td.ExpiresIn
	}
	if td.Expire != "" {
		auth.Metadata["expired"] = td.Expire
	}
	auth.Metadata["last_refresh"] = time.Now().UTC().Format(time.RFC3339)

	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	auth.Attributes["base_url"] = "https://cli-chat-proxy.grok.com/v1"
	auth.Attributes["api_key"] = td.AccessToken
	auth.Attributes["subscription_tier"] = tier

	return auth, nil
}

func (e *GrokCLIExecutor) injectGrokCLIAttributes(auth *cliproxyauth.Auth) {
	if auth == nil {
		return
	}
	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}

	// Force base URL to route through the private Grok CLI gateway
	auth.Attributes["base_url"] = "https://cli-chat-proxy.grok.com/v1"

	// Apply custom headers mapping to enforce client version checks at gateway.
	// This overrides the built-in HTTP client User-Agent and prevents WAF 426 blocks.
	auth.Attributes["header:User-Agent"] = "Grok-CLI/0.2.93 (Linux; x86_64)"
	auth.Attributes["header:X-Grok-Client-Version"] = "0.2.93"
	auth.Attributes["header:Accept"] = "application/json, text/event-stream"

	token := ""
	if auth.Attributes["api_key"] != "" {
		token = strings.TrimSpace(auth.Attributes["api_key"])
	} else if auth.Metadata != nil {
		token = e.metadataString(auth.Metadata, "access_token")
	}
	if token != "" {
		auth.Attributes["api_key"] = token
	}
}

func (e *GrokCLIExecutor) mapModelForUpstream(auth *cliproxyauth.Auth, req *cliproxyexecutor.Request) {
	if auth == nil || req == nil {
		return
	}

	isFree := true
	tier := ""
	if auth.Metadata != nil {
		tier = e.metadataString(auth.Metadata, "subscription_tier")
	}
	if tier == "" && auth.Attributes != nil {
		tier = auth.Attributes["subscription_tier"]
	}

	if tier != "" && !strings.EqualFold(tier, "free") && !strings.EqualFold(tier, "free-tier") {
		isFree = false
	}

	if isFree {
		// Free tier accounts are forced to use grok-4.5
		req.Model = "grok-4.5"
	} else {
		// Paid tier accounts resolve to the registered upstream name (e.g. grok-build-cli -> grok-build)
		if modelInfo := registry.LookupStaticModelInfo(req.Model); modelInfo != nil && modelInfo.Name != "" {
			req.Model = modelInfo.Name
		}
	}

	// Always rewrite the serialized JSON payload to match the final request model name.
	if len(req.Payload) > 0 && req.Model != "" {
		if updated, err := sjson.SetBytes(req.Payload, "model", req.Model); err == nil {
			req.Payload = updated
		}
	}
}

func (e *GrokCLIExecutor) classifyGrokCLIError(err error) error {
	if err == nil {
		return nil
	}
	errStr := err.Error()
	normalized := strings.ToLower(errStr)

	// Match free tier usage quota limit failures, ignoring apostrophe casing variations
	if strings.Contains(normalized, "reached your free") && strings.Contains(normalized, "grok build usage limit") {
		return &cliproxyauth.Error{
			Code:       "quota_exhausted",
			Message:    "You've reached your free Grok Build usage limit",
			Retryable:  false,
			HTTPStatus: http.StatusTooManyRequests, // 429 triggers Conductor cooldown/retry after
		}
	}

	// Match generic rate limits or HTTP 429 upstream responses
	if strings.Contains(normalized, "rate limit") || strings.Contains(normalized, "too many requests") {
		return &cliproxyauth.Error{
			Code:       "rate_limited",
			Message:    err.Error(),
			Retryable:  true,
			HTTPStatus: http.StatusTooManyRequests, // 429
		}
	}

	return err
}

func (e *GrokCLIExecutor) metadataString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func (e *GrokCLIExecutor) cloneAuth(auth *cliproxyauth.Auth) *cliproxyauth.Auth {
	if auth == nil {
		return nil
	}
	clone := *auth
	if auth.Attributes != nil {
		clone.Attributes = make(map[string]string, len(auth.Attributes))
		for k, v := range auth.Attributes {
			clone.Attributes[k] = v
		}
	}
	if auth.Metadata != nil {
		clone.Metadata = make(map[string]any, len(auth.Metadata))
		for k, v := range auth.Metadata {
			clone.Metadata[k] = v
		}
	}
	return &clone
}