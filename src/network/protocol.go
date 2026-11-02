package network

// Legacy Tailscale listener ports shared by host and client.
const (
	ControlPort  = "47470"
	OpenCodePort = "47471"
	CodexPort    = "47472"
)

// Info is the host metadata returned to clients.
type Info struct {
	Workspace string   `json:"workspace,omitempty"`
	Dev       string   `json:"dev,omitempty"` // Compatibility with older host services.
	Agents    []string `json:"agents,omitempty"`
}

// WorkspacePath returns the configured host workspace, including for older services.
func (h Info) WorkspacePath() string {
	if h.Workspace != "" {
		return h.Workspace
	}
	return h.Dev
}
