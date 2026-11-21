package shell

// Style is the user's prompt colors and symbols, read from their shell settings.
// Empty fields mean the client keeps its stock value.
type Style struct {
	User        string `json:"user,omitempty"`
	Path        string `json:"path,omitempty"`
	GitBranch   string `json:"git_branch,omitempty"`
	GitDirty    string `json:"git_dirty,omitempty"`
	GitAction   string `json:"git_action,omitempty"`
	GitArrow    string `json:"git_arrow,omitempty"`
	GitStash    string `json:"git_stash,omitempty"`
	Execution   string `json:"execution,omitempty"`
	PromptOK    string `json:"prompt_ok,omitempty"`
	PromptError string `json:"prompt_error,omitempty"`
	Symbol      string `json:"symbol,omitempty"`
	DownArrow   string `json:"down_arrow,omitempty"`
	UpArrow     string `json:"up_arrow,omitempty"`
	Stash       string `json:"stash,omitempty"`
}
