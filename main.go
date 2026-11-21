// Command rmt connects native program terminals to remote devices.
package main

import "github.com/fschrhunt/rmt/src/cli"

// version is set to the release number when packaging.
var version = "dev"

func main() {
	cli.Main(version)
}
