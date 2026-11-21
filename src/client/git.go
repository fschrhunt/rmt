package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/fschrhunt/rmt/src/shell"
)

// Git asks the host for repository state without blocking the local prompt.
func Git(host, directory string) (shell.Git, error) {
	target, err := resolveTarget(host)
	if err != nil {
		return shell.Git{}, err
	}
	defer target.client.CloseIdleConnections()
	endpoint := target.base + "/git?directory=" + url.QueryEscape(directory)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return shell.Git{}, err
	}
	response, err := target.client.Do(request)
	if err != nil {
		return shell.Git{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return shell.Git{}, errors.New("host git status unavailable")
	}
	var info shell.Git
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&info); err != nil {
		return shell.Git{}, err
	}
	return info, nil
}
