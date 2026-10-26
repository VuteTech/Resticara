// Package systemd generates and installs the systemd units that run resticara.
package systemd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"resticara/internal/config"
)

type Unit struct {
	Name    string
	Content string
}

// Plan is the set of units for a configuration.
type Plan struct {
	// Units are written in this order.
	Units []Unit
	// Timers are enabled and restarted in this order.
	Timers []string
}

// NewPlan renders one backup timer that runs all backups sequentially (one report per
// run), and a prune timer and a check timer per repository. binary and configPath
// must be absolute.
//
// prune and check take an exclusive lock on the repository, so their services are
// ordered after the backup (and the check after the prune): when one is due while
// the other still runs, systemd starts it once the other has finished.
func NewPlan(cfg config.Config, binary, configPath string) Plan {
	serviceEnv := fmt.Sprintf(`EnvironmentFile=-%s
Environment=RESTIC_CACHE_DIR=/var/cache/resticara
CacheDirectory=resticara`, escapeSpecifiers(config.EnvFilePath(configPath)))
	execStart := func(args ...string) string {
		quoted := []string{execArg(binary), execArg("--config=" + configPath)}
		for _, arg := range args {
			quoted = append(quoted, execArg(arg))
		}
		return strings.Join(quoted, " ")
	}

	backupService := fmt.Sprintf(`[Unit]
Description=Resticara backup
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s
%s

[Install]
WantedBy=multi-user.target
`, execStart("run"), serviceEnv+"\n"+serviceSettings(cfg.Systemd, cfg.Systemd.BackupTimeout, localPaths(cfg.Repositories()...)))

	backupTimer := `[Unit]
Description=Resticara backup timer

[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
`

	plan := Plan{
		Units: []Unit{
			{Name: "resticara-backup.service", Content: backupService},
			{Name: "resticara-backup.timer", Content: backupTimer},
		},
		Timers: []string{"resticara-backup.timer"},
	}

	for _, repo := range cfg.Repositories() {
		base := pruneUnitBase(repo.URL)
		maintenance := serviceSettings(cfg.Systemd, cfg.Systemd.MaintenanceTimeout, localPaths(repo))
		pruneService := fmt.Sprintf(`[Unit]
Description=Resticara prune for %s
Wants=network-online.target
After=network-online.target resticara-backup.service

[Service]
Type=oneshot
ExecStart=%s
%s

[Install]
WantedBy=multi-user.target
`, escapeSpecifiers(repo.URL), execStart("prune", repo.URL), serviceEnv+"\n"+maintenance)

		// OnUnitActiveSec alone never fires because it counts from the last run of the service;
		// OnActiveSec schedules the first run after the timer is (re)started.
		pruneTimer := fmt.Sprintf(`[Unit]
Description=Resticara prune timer for %s

[Timer]
OnActiveSec=1h
OnUnitActiveSec=%dd
RandomizedDelaySec=1h

[Install]
WantedBy=timers.target
`, escapeSpecifiers(repo.URL), repo.PruneDays)

		checkBase := checkUnitBase(repo.URL)
		checkService := fmt.Sprintf(`[Unit]
Description=Resticara check for %s
Wants=network-online.target
After=network-online.target resticara-backup.service %s.service

[Service]
Type=oneshot
ExecStart=%s
%s

[Install]
WantedBy=multi-user.target
`, escapeSpecifiers(repo.URL), base, execStart("check", repo.URL), serviceEnv+"\n"+maintenance)

		// the first check runs some hours after the first prune, so both rarely queue up
		checkTimer := fmt.Sprintf(`[Unit]
Description=Resticara check timer for %s

[Timer]
OnActiveSec=6h
OnUnitActiveSec=%dd
RandomizedDelaySec=1h

[Install]
WantedBy=timers.target
`, escapeSpecifiers(repo.URL), repo.CheckDays)

		plan.Units = append(plan.Units,
			Unit{Name: base + ".service", Content: pruneService},
			Unit{Name: base + ".timer", Content: pruneTimer},
			Unit{Name: checkBase + ".service", Content: checkService},
			Unit{Name: checkBase + ".timer", Content: checkTimer},
		)
		plan.Timers = append(plan.Timers, base+".timer", checkBase+".timer")
	}
	return plan
}

// serviceSettings returns the timeout, priority and sandbox settings of a service.
// writable are the local repositories the service writes to.
func serviceSettings(s config.Systemd, timeout string, writable []string) string {
	lines := []string{
		"TimeoutStartSec=" + timeout,
		fmt.Sprintf("Nice=%d", s.Nice),
		"CPUSchedulingPolicy=batch",
		"IOSchedulingClass=" + s.IOSchedulingClass,
	}
	if s.IOSchedulingClass == "best-effort" {
		lines = append(lines, "IOSchedulingPriority=7")
	}
	if s.Hardening {
		lines = append(lines,
			"ProtectSystem=strict",
			"ProtectHome=read-only",
			"PrivateTmp=yes",
			"NoNewPrivileges=yes",
			"ProtectKernelTunables=yes",
			"ProtectControlGroups=yes",
		)
		// restic retries failed writes for a long time, so every local repository must
		// be writable; "-" ignores paths that do not exist (yet) instead of failing
		seen := make(map[string]bool)
		for _, path := range append(writable, s.ReadWritePaths...) {
			if !seen[path] {
				seen[path] = true
				lines = append(lines, "ReadWritePaths="+execArg("-"+path))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// localPaths returns the directories of the local repositories: "local:/path"
// or a plain absolute path. Other backends are reached over the network.
func localPaths(repos ...config.Repository) []string {
	var paths []string
	for _, repo := range repos {
		path := strings.TrimPrefix(repo.URL, "local:")
		if strings.HasPrefix(path, "/") {
			paths = append(paths, path)
		}
	}
	return paths
}

// pruneUnitBase names the prune units of a repository.
func pruneUnitBase(url string) string {
	return repoUnitBase("resticara-prune-", url)
}

// checkUnitBase names the check units of a repository.
func checkUnitBase(url string) string {
	return repoUnitBase("resticara-check-", url)
}

// repoUnitBase names units of a repository: the prefix, a readable part made of the
// URL's letters and digits, and a short hash so different URLs never share a name.
func repoUnitBase(prefix, url string) string {
	var b strings.Builder
	dash := false
	for _, r := range url {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	readable := strings.TrimSuffix(b.String(), "-")
	if len(readable) > 40 {
		readable = strings.TrimSuffix(readable[:40], "-")
	}
	sum := sha256.Sum256([]byte(url))
	return prefix + readable + "-" + hex.EncodeToString(sum[:4])
}

// escapeSpecifiers escapes "%", which systemd expands in most unit settings.
func escapeSpecifiers(s string) string {
	return strings.ReplaceAll(s, "%", "%%")
}

// execArg quotes one ExecStart argument for systemd: "%" and "$" are escaped, and
// arguments with spaces, quotes or backslashes are put in double quotes.
func execArg(arg string) string {
	arg = strings.ReplaceAll(escapeSpecifiers(arg), "$", "$$")
	if arg != "" && !strings.ContainsAny(arg, " \t\n\"'\\;") {
		return arg
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(arg) + `"`
}

// Systemctl runs systemctl commands.
type Systemctl interface {
	Output(args ...string) ([]byte, error)
	Run(args ...string) error
}

// ExecSystemctl runs the systemctl binary.
type ExecSystemctl struct{}

func (ExecSystemctl) Output(args ...string) ([]byte, error) {
	return exec.Command("systemctl", args...).Output() //nolint:gosec // G204: the arguments are unit names from the plan
}

func (ExecSystemctl) Run(args ...string) error {
	return exec.Command("systemctl", args...).Run() //nolint:gosec // G204: the arguments are unit names from the plan
}

// UnitDir returns the directory to write units to: /etc/systemd/system when systemd
// searches it, otherwise the first existing directory of the unit path.
func UnitDir(sys Systemctl) (string, error) {
	unitOut, err := sys.Output("show", "--property=UnitPath")
	if err != nil {
		return "", fmt.Errorf("failed to retrieve systemd unit path: %v", err)
	}
	unitLine := strings.TrimSpace(string(unitOut))
	unitLine = strings.TrimPrefix(unitLine, "UnitPath=")
	paths := strings.FieldsFunc(unitLine, func(r rune) bool { return r == ':' || r == ' ' })
	for _, p := range paths {
		if p == "/etc/systemd/system" {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("could not determine systemd unit directory from UnitPath: %s", unitLine)
}

// Apply removes resticara units that are not part of the plan, writes the plan's units
// to unitDir, reloads systemd and enables and restarts the timers.
func Apply(sys Systemctl, unitDir string, plan Plan) error {
	expected := make(map[string]bool)
	for _, unit := range plan.Units {
		expected[unitBase(unit.Name)] = true
	}

	entries, err := os.ReadDir(unitDir)
	if err != nil {
		return err
	}
	staleSet := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "resticara-") {
			continue
		}
		if !(strings.HasSuffix(name, ".service") || strings.HasSuffix(name, ".timer")) {
			continue
		}
		if base := unitBase(name); !expected[base] {
			staleSet[base] = true
		}
	}
	stale := make([]string, 0, len(staleSet))
	for base := range staleSet {
		stale = append(stale, base)
	}
	sort.Strings(stale)
	for _, base := range stale {
		// a stale unit is often not loaded anymore, so failing to disable it is fine;
		// failing to remove its file is not, as it would stay installed
		_ = sys.Run("disable", "--now", base+".timer")
		_ = sys.Run("disable", "--now", base+".service")
		for _, name := range []string{base + ".timer", base + ".service"} {
			if err := os.Remove(filepath.Join(unitDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("removing stale unit: %w", err)
			}
		}
	}

	for _, unit := range plan.Units {
		//nolint:gosec // G306: systemd unit files are world-readable
		if err := os.WriteFile(filepath.Join(unitDir, unit.Name), []byte(unit.Content), 0644); err != nil {
			return err
		}
	}

	if err := sys.Run("daemon-reload"); err != nil {
		return fmt.Errorf("failed to reload systemd daemon: %v", err)
	}
	for _, t := range plan.Timers {
		if err := sys.Run("enable", t); err != nil {
			return fmt.Errorf("failed to enable %s: %v", t, err)
		}
		if err := sys.Run("restart", t); err != nil {
			return fmt.Errorf("failed to restart %s: %v", t, err)
		}
	}
	return nil
}

func unitBase(name string) string {
	return strings.TrimSuffix(strings.TrimSuffix(name, ".service"), ".timer")
}

// Generate installs the units for cfg and reports to out. binary is the resticara
// binary the units run.
func Generate(sys Systemctl, cfg config.Config, binary, configPath string, out io.Writer) error {
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}

	unitDir, err := UnitDir(sys)
	if err != nil {
		return err
	}
	if err := Apply(sys, unitDir, NewPlan(cfg, binary, configPath)); err != nil {
		return err
	}

	fmt.Fprintf(out, "Systemd timer files written to %s and activated.\n", unitDir)
	envFile := config.EnvFilePath(configPath)
	if _, err := os.Stat(envFile); err != nil {
		fmt.Fprintf(out, "Warning: %s not found; restic credentials (RESTIC_PASSWORD, B2_ACCOUNT_ID, B2_ACCOUNT_KEY) must be set there for the timers to work.\n", envFile)
	} else if warning := config.PermissionWarning(envFile); warning != "" {
		fmt.Fprintln(out, warning)
	}
	return nil
}
