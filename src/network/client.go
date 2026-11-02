package network

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// normalizeURL accepts only a direct HTTPS host without paths or embedded credentials.
func normalizeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("address must be an HTTPS host and port, such as https://host:47473")
	}
	return "https://" + u.Host, nil
}

// pinnedTransport verifies the host certificate's SHA-256 fingerprint on every TLS connection.
func pinnedTransport(fingerprint string) (*http.Transport, error) {
	want, err := hex.DecodeString(fingerprint)
	if err != nil || len(want) != sha256.Size {
		return nil, errors.New("invalid host certificate fingerprint")
	}
	return &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, VerifyConnection: func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("host sent no certificate")
		}
		got := sha256.Sum256(state.PeerCertificates[0].Raw)
		if subtle.ConstantTimeCompare(got[:], want) != 1 {
			return errors.New("host identity changed; verify and pair again")
		}
		return nil
	}}}, nil
}

// noRedirect prevents credentials from being forwarded to another host.
func noRedirect(*http.Request, []*http.Request) error { return errors.New("redirects are not allowed") }

// Inspect reads an untrusted host identity without sending any credentials.
func Inspect(ctx context.Context, raw string) (string, string, error) {
	base, err := normalizeURL(raw)
	if err != nil {
		return "", "", err
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: noRedirect}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/identity", nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("cannot reach host: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return "", "", errors.New("host does not offer pairing")
	}
	var info struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&info) != nil {
		return "", "", errors.New("invalid host identity")
	}
	sum := sha256.Sum256(resp.TLS.PeerCertificates[0].Raw)
	return info.Name, hex.EncodeToString(sum[:]), nil
}

// Pair sends the single-use code only to the fingerprint the user verified.
func Pair(ctx context.Context, raw, fingerprint, code, name string) (Profile, error) {
	base, err := normalizeURL(raw)
	if err != nil {
		return Profile{}, err
	}
	tr, err := pinnedTransport(fingerprint)
	if err != nil {
		return Profile{}, err
	}
	defer tr.CloseIdleConnections()
	data, err := json.Marshal(map[string]string{"code": code, "name": name})
	if err != nil {
		return Profile{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/pair", bytes.NewReader(data))
	if err != nil {
		return Profile{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: noRedirect}
	resp, err := client.Do(req)
	if err != nil {
		return Profile{}, fmt.Errorf("pairing failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Profile{}, fmt.Errorf("pairing failed: %s", resp.Status)
	}
	var result struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result) != nil || result.Token == "" {
		return Profile{}, errors.New("invalid pairing response")
	}
	return Profile{URL: base, Fingerprint: fingerprint, Token: result.Token}, nil
}

type bearerTransport struct {
	base  *http.Transport
	token string
}

// CloseIdleConnections releases cached connections when a client or bridge finishes.
func (t bearerTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }

// RoundTrip authenticates a request without mutating the caller's headers.
func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

// Client returns a pinned, authenticated HTTP client for a paired host.
func (p Profile) Client() (*http.Client, error) {
	base, err := normalizeURL(p.URL)
	if err != nil {
		return nil, err
	}
	_ = base
	if strings.TrimSpace(p.Token) == "" {
		return nil, errors.New("missing device credential")
	}
	tr, err := p.Transport()
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: noRedirect}, nil
}

// Transport returns a pinned, authenticated transport for HTTP and WebSocket proxies.
func (p Profile) Transport() (http.RoundTripper, error) {
	if _, err := normalizeURL(p.URL); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Token) == "" {
		return nil, errors.New("missing device credential")
	}
	tr, err := pinnedTransport(p.Fingerprint)
	if err != nil {
		return nil, err
	}
	return bearerTransport{base: tr, token: p.Token}, nil
}
