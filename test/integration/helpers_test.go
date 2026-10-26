//go:build integration

package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const resticPassword = "integration-test"

// env is an isolated test environment: its own HOME, restic cache, config
// directory and a directory with stub commands that comes first in PATH.
type env struct {
	t       *testing.T
	dir     string
	stubDir string
	home    string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{t: t, dir: dir, stubDir: filepath.Join(dir, "stubs"), home: filepath.Join(dir, "home")}
	for _, d := range []string{e.stubDir, e.home, filepath.Join(dir, "templates")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// templates next to the config, where resticara looks for them
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		copyFile(t, filepath.Join(templatesDir, entry.Name()), filepath.Join(dir, "templates", entry.Name()))
	}
	return e
}

func (e *env) environ() []string {
	return []string{
		"PATH=" + e.stubDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + e.home,
		"RESTIC_PASSWORD=" + resticPassword,
		"RESTIC_CACHE_DIR=" + filepath.Join(e.home, "cache"),
	}
}

// path returns a path inside the environment directory.
func (e *env) path(elem ...string) string {
	return filepath.Join(append([]string{e.dir}, elem...)...)
}

// initRepo creates a local restic repository and returns its path.
func (e *env) initRepo(name string) string {
	e.t.Helper()
	repo := e.path(name)
	e.restic("init", "-r", repo)
	return repo
}

// restic runs restic in the environment and fails the test on errors.
func (e *env) restic(args ...string) string {
	e.t.Helper()
	cmd := exec.Command("restic", args...)
	cmd.Env = e.environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("restic %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

type snapshot struct {
	Paths    []string `json:"paths"`
	Hostname string   `json:"hostname"`
}

func (e *env) snapshots(repo string) []snapshot {
	e.t.Helper()
	var snapshots []snapshot
	if err := json.Unmarshal([]byte(e.restic("snapshots", "--json", "-q", "-r", repo)), &snapshots); err != nil {
		e.t.Fatal(err)
	}
	return snapshots
}

// writeConfig writes config.ini with the given content and returns its path.
func (e *env) writeConfig(content string) string {
	e.t.Helper()
	path := e.path("config.ini")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return path
}

// stub installs a shell script that shadows the named command.
func (e *env) stub(name, script string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.stubDir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		e.t.Fatal(err)
	}
}

// writeFile creates a file with parent directories inside the environment.
func (e *env) writeFile(content string, elem ...string) string {
	e.t.Helper()
	path := e.path(elem...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return path
}

type result struct {
	exitCode int
	output   string
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// resticara runs the binary from "/", the working directory systemd uses.
func (e *env) resticara(args ...string) result {
	e.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = "/"
	cmd.Env = e.environ()
	out, err := cmd.CombinedOutput()
	res := result{output: ansi.ReplaceAllString(string(out), "")}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.exitCode = exitErr.ExitCode()
	} else if err != nil {
		e.t.Fatalf("running resticara: %v", err)
	}
	return res
}

// expect fails the test unless the run ended with the exit code and printed every string.
func (r result) expect(t *testing.T, exitCode int, contains ...string) {
	t.Helper()
	if r.exitCode != exitCode {
		t.Errorf("exit code = %d, want %d\noutput:\n%s", r.exitCode, exitCode, r.output)
	}
	for _, s := range contains {
		if !strings.Contains(r.output, s) {
			t.Errorf("output does not contain %q\noutput:\n%s", s, r.output)
		}
	}
}

const baseConfig = `[general]
hostID = hostname

[smtp]
enabled = false

`

func dirJob(name, repo, directory string) string {
	return fmt.Sprintf(`[dir:%s]
bucket = %s
directory = %s
retention_daily = 2
retention_weekly = 7
retention_monthly = 3

`, name, repo, directory)
}

func mysqlJob(name, repo, database string) string {
	return fmt.Sprintf(`[mysql:%s]
bucket = %s
database = %s
retention_daily = 2
retention_weekly = 7
retention_monthly = 3

`, name, repo, database)
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
}
