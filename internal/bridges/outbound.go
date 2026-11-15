package bridges

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"agentmem/internal/netsafe"
)

// Bounds shared by every outbound JSON call. A sidecar that returns more than
// this is a fault, not a result: truncating evidence silently would let a
// receipt claim rows that were never seen.
const (
	MaxOutboundRequestBytes  = 1 << 20
	MaxOutboundResponseBytes = 4 << 20
	maxOutboundRetries       = 1
)

// PostJSON sends one JSON payload to a configured outbound endpoint and returns
// the raw response body. The endpoint is re-validated against the egress
// allowlist on every call, redirects are re-checked per hop and TLS downgrade is
// refused. Only transport failures and 5xx are retried, and no error ever
// carries the URL, the host or the payload.
func PostJSON(ctx context.Context, endpoint, path string, allowHosts []string, timeout time.Duration, payload any) ([]byte, error) {
	if err := netsafe.ValidateEndpoint(endpoint, allowHosts); err != nil {
		return nil, errors.New("bridges: outbound endpoint is not allowed")
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("bridges: outbound payload is not serialisable")
	}
	if len(encoded) > MaxOutboundRequestBytes {
		return nil, errors.New("bridges: outbound payload exceeds the request size limit")
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: netsafe.RedirectPolicy(
			allowHosts, netsafe.DefaultMaxHops),
	}
	var lastErr error
	for attempt := 0; attempt <= maxOutboundRetries; attempt++ {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+path, bytes.NewReader(encoded))
		if reqErr != nil {
			// net/http errors embed the full URL; only the type is retained.
			return nil, fmt.Errorf("bridges: build outbound request (%T)", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, doErr := client.Do(req)
		if doErr != nil {
			lastErr = fmt.Errorf("bridges: outbound transport failure (%T)", doErr)
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxOutboundResponseBytes+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("bridges: outbound response could not be read (%T)", readErr)
		}
		if len(data) > MaxOutboundResponseBytes {
			return nil, errors.New("bridges: outbound response exceeds the size limit")
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("bridges: outbound endpoint returned %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("bridges: outbound endpoint returned %d", resp.StatusCode)
		}
		return data, nil
	}
	return nil, fmt.Errorf("bridges: outbound request failed after %d attempts (%v)", maxOutboundRetries+1, lastErr)
}
