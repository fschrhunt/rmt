package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/network"
)

// sshCommand runs on the host through SSH. Non-interactive SSH shells often omit the usual
// install directories, so they are appended to PATH.
const sshCommand = `PATH="$PATH:$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin" rmt --pair-code`

// pairHost verifies the host identity and saves its single-use device credential. It pairs
// over SSH when `ssh <name>` reaches the host, and otherwise asks the user to verify by hand.
func pairHost(name, address string, reader *bufio.Reader, output io.Writer) error {
	name = strings.ToLower(name)
	if err := network.ValidateName(name); err != nil {
		return err
	}
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	if err := network.PrepareStorage(dir); err != nil {
		return err
	}
	profiles, err := network.Profiles(dir)
	if err != nil {
		return err
	}
	if _, ok := profiles[name]; ok {
		return errors.New("this host is already paired")
	}
	device, _ := os.Hostname()
	if device == "" {
		device = "rmt client"
	}
	profile, err := pairOverSSH(name, address, device)
	if err == nil {
		fmt.Fprintf(output, "Paired with %s over SSH.\n", name)
	} else {
		fmt.Fprintf(output, "SSH pairing unavailable (%v); pairing manually.\n", err)
		if profile, err = pairManually(address, device, reader, output); err != nil {
			return err
		}
	}
	if err := network.SaveProfile(dir, name, profile); err != nil {
		return fmt.Errorf("host accepted pairing but local save failed; remove the new device from the host's devices.json: %w", err)
	}
	return nil
}

// pairOverSSH fetches a one-time code and the host fingerprint through `ssh <name>`, which
// already authenticates both machines, then enrolls over HTTPS pinned to that fingerprint.
// If the SSH alias reaches a different machine than address, the pin fails and nothing pairs.
func pairOverSSH(name, address, device string) (network.Profile, error) {
	cmd := exec.Command("ssh", "-o", "ConnectTimeout=5", "--", name, sshCommand)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return network.Profile{}, fmt.Errorf("ssh %s: %w", name, err)
	}
	// Shell startup files may print to stdout; the code is always the last line.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var host struct {
		Code        string `json:"code"`
		Fingerprint string `json:"fingerprint"`
	}
	if json.Unmarshal([]byte(lines[len(lines)-1]), &host) != nil || host.Code == "" || host.Fingerprint == "" {
		return network.Profile{}, errors.New("host returned no pairing code")
	}
	return network.Pair(context.Background(), address, host.Fingerprint, host.Code, device)
}

// pairManually shows the host fingerprint for the user to compare with ~/.rmt/pairing.json on
// the host, then enrolls with the one-time code they copy from it.
func pairManually(address, device string, reader *bufio.Reader, output io.Writer) (network.Profile, error) {
	remote, fingerprint, err := network.Inspect(context.Background(), address)
	if err != nil {
		return network.Profile{}, err
	}
	remote = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, remote)
	fmt.Fprintf(output, "Host: %s\nVerification: %s\n", remote, fingerprint[:32])
	fmt.Fprintln(output, "On the host, open ~/.rmt/pairing.json and compare its verify value.")
	fmt.Fprint(output, "Do the verification values match? [y/N]: ")
	answer, err := reader.ReadString('\n')
	if err != nil {
		return network.Profile{}, errors.New("pairing cancelled")
	}
	if !strings.EqualFold(strings.TrimSpace(answer), "y") && !strings.EqualFold(strings.TrimSpace(answer), "yes") {
		return network.Profile{}, errors.New("pairing cancelled")
	}
	fmt.Fprint(output, "One-time code from the host: ")
	code, err := reader.ReadString('\n')
	if err != nil {
		return network.Profile{}, errors.New("pairing cancelled")
	}
	return network.Pair(context.Background(), address, fingerprint, strings.TrimSpace(code), device)
}

// printPairCode serves `rmt --pair-code` on the host: it asks the running host service for a
// fresh one-time code and prints it with the host fingerprint as one JSON line.
func printPairCode(output io.Writer) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	var result map[string]string
	if err := network.Control(dir, http.MethodPost, "/code", nil, &result); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]string{"code": result["code"], "fingerprint": result["fingerprint"]})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "%s\n", data)
	return err
}
