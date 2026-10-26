//go:build integration

// Package integration runs the resticara binary against real restic repositories.
//
// The tests need restic 0.17 or newer in PATH. Without it they are skipped, unless
// RESTICARA_REQUIRE_RESTIC is set (as in CI), in which case they fail.
package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var (
	// binary is the resticara binary built for this test run.
	binary string
	// templatesDir holds the templates shipped in the repository.
	templatesDir string
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if err := checkRestic(); err != nil {
		if os.Getenv("RESTICARA_REQUIRE_RESTIC") != "" {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "skipping integration tests: %v\n", err)
		return 0
	}

	dir, err := os.MkdirTemp("", "resticara-integration-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)

	binary = filepath.Join(dir, "resticara")
	build := exec.Command("go", "build", "-o", binary, "resticara")
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "building resticara: %v\n", err)
		return 1
	}

	if templatesDir, err = filepath.Abs("../../templates"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

// checkRestic makes sure a restic binary with --stdin-from-command support (0.17+) is in PATH.
func checkRestic() error {
	out, err := exec.Command("restic", "version").Output()
	if err != nil {
		return fmt.Errorf("restic not found: %v", err)
	}
	// "restic 0.19.1 compiled with go1.26.4 on linux/amd64"
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return fmt.Errorf("unexpected restic version output %q", out)
	}
	var major, minor int
	if _, err := fmt.Sscanf(fields[1], "%d.%d", &major, &minor); err != nil {
		return fmt.Errorf("unexpected restic version %q: %v", fields[1], err)
	}
	if major == 0 && minor < 17 {
		return fmt.Errorf("restic %s is too old, 0.17 or newer is required", fields[1])
	}
	return nil
}
