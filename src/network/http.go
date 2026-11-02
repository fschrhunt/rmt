package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// JSONGet reads one bounded JSON response from an agent server.
func JSONGet(ctx context.Context, client *http.Client, address string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}
