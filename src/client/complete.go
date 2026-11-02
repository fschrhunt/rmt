package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// Complete lists remote paths relative to the prompt's current directory.
func Complete(host, directory, prefix string) ([]string, error) {
	target, err := resolveTarget(host)
	if err != nil {
		return nil, err
	}
	defer target.client.CloseIdleConnections()
	data, err := json.Marshal(map[string]string{"directory": directory, "prefix": prefix})
	if err != nil {
		return nil, err
	}
	response, err := target.client.Post(target.base+"/complete", "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("remote completion unavailable")
	}
	var matches []string
	err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&matches)
	return matches, err
}
