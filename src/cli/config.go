package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/fschrhunt/rmt/src/config"
)

// configCommand prints the effective workspace or sets it for the next host start.
func configCommand(args []string) error {
	path, home, err := config.Path()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		c, err := config.Read(path, home)
		if err != nil {
			return err
		}
		agents := c.Agents
		if agents == nil {
			agents = []string{"opencode", "codex"}
		}
		listen := c.Listen
		if listen == "" {
			listen = "off"
		}
		fmt.Printf("workspace: %s\nagents:    %s\nlisten:    %s\nconfig:    %s\n", c.Workspace, strings.Join(agents, ", "), listen, path)
		return nil
	}
	if len(args) != 2 || args[0] != "set" {
		return errors.New("usage: rmt config [set <workspace>]")
	}
	if err := config.SetWorkspace(path, home, args[1]); err != nil {
		return err
	}
	c, err := config.Read(path, home)
	if err != nil {
		return err
	}
	fmt.Printf("Workspace set to %s. Restart the host service when it is safe to do so.\n", c.Workspace)
	return nil
}
