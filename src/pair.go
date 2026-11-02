package rmt

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/fschrhunt/rmt/src/network"
)

// networkDir returns the user's private network identity and profile directory.
func networkDir() (string, error) {
	path, _, err := configPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

// pairHost verifies a host fingerprint and saves its single-use device credential.
func pairHost(name, address string, input io.Reader, output io.Writer) error {
	name = strings.ToLower(name)
	if err := network.ValidateName(name); err != nil {
		return err
	}
	for _, reserved := range []string{"setup", "serve", "config", "ls", "connect", "pair", "code", "devices", "revoke", "hosts", "unpair", "help"} {
		if name == reserved {
			return errors.New("host name is reserved for an rmt command")
		}
	}
	dir, err := networkDir()
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
		return errors.New("a paired host already uses that name")
	}
	host, fingerprint, err := network.Inspect(context.Background(), address)
	if err != nil {
		return err
	}
	host = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, host)
	fmt.Fprintf(output, "Host: %s\nFingerprint: %s\nCompare this fingerprint with 'rmt code' on the host.\n", host, fingerprint)
	reader := bufio.NewReader(input)
	fmt.Fprint(output, "Do the fingerprints match? [y/N]: ")
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
		return fmt.Errorf("host accepted pairing but local save failed; revoke the new device on the host: %w", err)
	}
	fmt.Fprintf(output, "Paired %s. Connect with: rmt %s <agent>\n", name, name)
	return nil
}

// hostControl handles local pairing codes and device management without the network.
func hostControl(command string, args []string) error {
	dir, err := networkDir()
	if err != nil {
		return err
	}
	switch command {
	case "hosts":
		if len(args) != 0 {
			return errors.New("usage: rmt hosts")
		}
		profiles, err := network.Profiles(dir)
		if err != nil {
			return err
		}
		if len(profiles) == 0 {
			fmt.Println("No paired hosts.")
			return nil
		}
		names := make([]string, 0, len(profiles))
		for name := range profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Printf("%-16s %s\n", name, profiles[name].URL)
		}
	case "unpair":
		if len(args) != 1 {
			return errors.New("usage: rmt unpair <host>")
		}
		if err := network.RemoveProfile(dir, args[0]); err != nil {
			return err
		}
		fmt.Println("Local host removed. Revoke this device on the host to remove its access.")
	case "code":
		if len(args) != 0 {
			return errors.New("usage: rmt code")
		}
		var result struct {
			Code        string `json:"code"`
			Fingerprint string `json:"fingerprint"`
			Expires     string `json:"expires"`
		}
		if err := network.Control(dir, http.MethodPost, "/code", nil, &result); err != nil {
			return err
		}
		fmt.Printf("Fingerprint: %s\nOne-time code: %s\nExpires: %s\n", result.Fingerprint, result.Code, result.Expires)
	case "devices":
		if len(args) != 0 {
			return errors.New("usage: rmt devices")
		}
		var devices []network.Device
		if err := network.Control(dir, http.MethodGet, "/devices", nil, &devices); err != nil {
			return err
		}
		if len(devices) == 0 {
			fmt.Println("No paired devices.")
			return nil
		}
		fmt.Printf("%-16s %s\n", "ID", "DEVICE")
		for _, device := range devices {
			fmt.Printf("%-16s %s\n", device.ID, device.Name)
		}
	case "revoke":
		if len(args) != 1 {
			return errors.New("usage: rmt revoke <device-id>")
		}
		data, _ := json.Marshal(map[string]string{"id": args[0]})
		if err := network.Control(dir, http.MethodPost, "/revoke", bytes.NewReader(data), nil); err != nil {
			return err
		}
		fmt.Println("Device revoked; its active connections were closed.")
	}
	return nil
}
