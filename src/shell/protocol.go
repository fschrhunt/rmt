// Package shell carries local prompt commands to an authenticated host shell.
package shell

// Request is a command or interrupt sent over the host connection.
type Request struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
}

// Version identifies the compatible shell protocol.
const Version = 4

// Event is streamed command output, the current remote directory, or an error.
type Event struct {
	Session   string `json:"session,omitempty"`
	Resumed   bool   `json:"resumed,omitempty"`
	Running   bool   `json:"running,omitempty"`
	Sequence  uint64 `json:"sequence,omitempty"`
	Version   int    `json:"version,omitempty"`
	Type      string `json:"type"`
	Data      []byte `json:"data,omitempty"`
	Directory string `json:"directory,omitempty"`
	Status    int    `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
}
