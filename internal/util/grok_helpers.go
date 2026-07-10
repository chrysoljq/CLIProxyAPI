package util

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// ProbeSubscriptionTier queries the Grok upstream models API to determine the account's plan tier.
// Returns "supergrok" if the premium "grok-build" model is available; otherwise returns "free".
func ProbeSubscriptionTier(ctx context.Context, accessToken string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://cli-chat-proxy.grok.com/v1/models", nil)
	if err != nil {
		return "free"
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "Grok-CLI/0.2.93 (Linux; x86_64)")
	req.Header.Set("X-Grok-Client-Version", "0.2.93")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "free"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "free"
	}

	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "free"
	}

	for _, model := range list.Data {
		if strings.Contains(model.ID, "grok-build") {
			return "supergrok"
		}
	}
	return "free"
}
