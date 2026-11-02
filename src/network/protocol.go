package network

// Info is the host metadata returned to clients.
type Info struct {
	Home     string            `json:"home"`
	Programs []string          `json:"programs,omitempty"`
	Problems map[string]string `json:"problems,omitempty"`
	Versions map[string]string `json:"versions,omitempty"`
}
