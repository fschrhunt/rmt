// Package shell carries local prompt commands to an authenticated host shell.
package shell

// Request is a command or interrupt sent over the host connection.
type Request struct {
	Type     string   `json:"type"`
	Command  string   `json:"command,omitempty"`
	Alias    string   `json:"alias,omitempty"`
	Programs []string `json:"programs,omitempty"`
}

// Version identifies the compatible shell protocol.
const Version = 10

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
	Home      string `json:"home,omitempty"`
	User      string `json:"user,omitempty"`
	Host      string `json:"host,omitempty"`
	Status    int    `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
	Program   string `json:"program,omitempty"`
}

// Git describes the repository state shown in the local prompt.
type Git struct {
	Branch string `json:"branch,omitempty"`
	Dirty  bool   `json:"dirty,omitempty"`
	Action string `json:"action,omitempty"`
	Ahead  int    `json:"ahead,omitempty"`
	Behind int    `json:"behind,omitempty"`
	Stash  int    `json:"stash,omitempty"`
}
