package hosts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// openCodeService discovers or starts OpenCode's own shared service and authenticates locally.
func openCodeService(ctx context.Context) (*programBackend, error) {
	disabled, err := nativeCommand(ctx, "opencode", "service", "get", "disabled")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(disabled) == "true" {
		return nil, errors.New("OpenCode's shared service is disabled in its native configuration")
	}
	status, err := nativeCommand(ctx, "opencode", "service", "status")
	target, statusErr := localServiceURL(status)
	if err == nil && statusErr != nil && !strings.EqualFold(strings.TrimSpace(status), "stopped") {
		return nil, statusErr
	}
	if err != nil || statusErr != nil {
		// Native services may inherit output descriptors, so do not capture their output.
		if err := startNative(ctx, "opencode", "service", "start"); err != nil {
			return nil, fmt.Errorf("cannot start OpenCode's native service: %w", err)
		}
		status, err = nativeCommand(ctx, "opencode", "service", "status")
		if err != nil {
			return nil, err
		}
		target, err = localServiceURL(status)
		if err != nil {
			return nil, err
		}
	}
	raw, err := nativeCommand(ctx, "opencode", "service", "get", "password")
	if err != nil {
		return nil, err
	}
	password := raw
	if strings.HasPrefix(raw, "\"") {
		if err := json.Unmarshal([]byte(raw), &password); err != nil {
			return nil, errors.New("invalid OpenCode service credential")
		}
	}
	if password == "" || password == "null" {
		return nil, errors.New("OpenCode service credential unavailable")
	}
	transport := &http.Transport{Proxy: nil, IdleConnTimeout: 30 * time.Second}
	b := &programBackend{name: "opencode", target: target, password: password, transport: transport}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, target.String()+"/api/info", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", password)
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach OpenCode's native service: %w", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenCode's native service refused authentication: %s", response.Status)
	}
	return b, nil
}
