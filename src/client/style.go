package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/fschrhunt/rmt/src/shell"
)

// Style reads the host user's prompt styling for the inherit mode.
func Style(host string) (shell.Style, error) {
	target, err := resolveTarget(host)
	if err != nil {
		return shell.Style{}, err
	}
	defer target.client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.base+"/style", nil)
	if err != nil {
		return shell.Style{}, err
	}
	response, err := target.client.Do(request)
	if err != nil {
		return shell.Style{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return shell.Style{}, errors.New("host prompt style unavailable")
	}
	var style shell.Style
	if err := json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&style); err != nil {
		return shell.Style{}, err
	}
	return style, nil
}
