package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// EnvFile is the name of the file next to the config that holds secrets as
// environment variables: restic credentials (RESTIC_PASSWORD, B2_ACCOUNT_ID,
// B2_ACCOUNT_KEY, ...) and notification secrets (SMTP_PASSWORD, ...).
const EnvFile = "restic.env"

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// EnvFilePath returns the path of the env file that belongs to a config file.
func EnvFilePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), EnvFile)
}

// LoadEnvFile sets the variables of an env file that are not set yet, so the
// environment wins over the file. A missing file is not an error. The format is the
// one systemd reads for EnvironmentFile=, without escapes and line continuations:
// KEY=VALUE lines, optionally quoted values, and comments starting with # or ;.
// It returns warnings for lines it cannot read.
func LoadEnvFile(path string) ([]string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the env file next to the config the admin chose
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var warnings []string
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !envName.MatchString(key) {
			warnings = append(warnings, fmt.Sprintf("line %d of %s is not KEY=VALUE and is ignored", n, path))
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if _, set := os.LookupEnv(key); !set {
			if err := os.Setenv(key, val); err != nil {
				return warnings, fmt.Errorf("setting %s from line %d of %s: %w", key, n, path, err)
			}
		}
	}
	return warnings, scanner.Err()
}
