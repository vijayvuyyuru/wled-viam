package wled

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// PostState sends a JSON payload to POST /json/state on the WLED device.
// It returns the parsed response body and logs non-200 status codes and
// WLED error codes ({"error": N}) via the resource logger.
func (s *wledWled) PostState(ctx context.Context, payload map[string]interface{}) (map[string]interface{}, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	url := s.wledBase + "/json/state"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Non-JSON response — log and return the status error
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("POST %s: HTTP %d: %s", url, resp.StatusCode, string(respBody))
		}
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Log WLED error codes (e.g. 9 = buffer overflow, 3/4 = device busy)
	if errCode, ok := result["error"]; ok {
		s.logger.Warnw("WLED returned error code", "url", url, "error", errCode)
	}

	if resp.StatusCode != http.StatusOK {
		s.logger.Warnw("WLED non-200 response", "url", url, "status", resp.StatusCode, "body", result)
		return result, fmt.Errorf("POST %s: HTTP %d", url, resp.StatusCode)
	}

	return result, nil
}

// GetState retrieves the full state from GET /json/state on the WLED device.
// The ESP32 is the source of truth — the Go module maintains no local state.
func (s *wledWled) GetState(ctx context.Context) (map[string]interface{}, error) {
	url := s.wledBase + "/json/state"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		s.logger.Warnw("WLED non-200 response", "url", url, "status", resp.StatusCode)
		return nil, fmt.Errorf("GET %s: HTTP %d: %s", url, resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return result, nil
}
