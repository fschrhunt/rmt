package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/network"
)

// pairHost verifies the host identity and saves its single-use device credential.
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
	remote, fingerprint, err := network.Inspect(context.Background(), address)
	if err != nil {
		return err
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
		return errors.New("pairing cancelled")
	}
	if !strings.EqualFold(strings.TrimSpace(answer), "y") && !strings.EqualFold(strings.TrimSpace(answer), "yes") {
		return errors.New("pairing cancelled")
	}
	fmt.Fprint(output, "One-time code from the host: ")
	code, err := reader.ReadString('\n')
	if err != nil {
		return errors.New("pairing cancelled")
	}
	device, _ := os.Hostname()
	if device == "" {
		device = "rmt client"
	}
	profile, err := network.Pair(context.Background(), address, fingerprint, strings.TrimSpace(code), device)
	if err != nil {
		return err
	}
	if err := network.SaveProfile(dir, name, profile); err != nil {
		return fmt.Errorf("host accepted pairing but local save failed; remove the new device from the host's devices.json: %w", err)
	}
	return nil
}
