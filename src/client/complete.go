package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// Complete lists remote paths relative to the prompt's current directory.
func (s *Shell) Complete(ctx context.Context, directory, prefix string) ([]string, error) {
	s.write.Lock()
	httpClient, base := s.client, s.base
	s.write.Unlock()
	data, err := json.Marshal(map[string]string{"directory": directory, "prefix": prefix})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/complete", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := httpClient.Do(request)
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
