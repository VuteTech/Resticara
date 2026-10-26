// Package systemd generates and installs the systemd units that run resticara.
package systemd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"resticara/internal/config"
)

// EnvFile is the name of the file next to the config that holds restic credentials
// (RESTIC_PASSWORD, B2_ACCOUNT_ID, B2_ACCOUNT_KEY, ...).
const EnvFile = "restic.env"

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
// run) and a prune timer per job. binary and configPath must be absolute.
func NewPlan(cfg config.Config, binary, configPath string) Plan {
	serviceEnv := fmt.Sprintf(`EnvironmentFile=-%s
Environment=RESTIC_CACHE_DIR=/var/cache/resticara
CacheDirectory=resticara`, filepath.Join(filepath.Dir(configPath), EnvFile))

	backupService := fmt.Sprintf(`[Unit]
Description=Resticara backup
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s --config=%s run
%s

[Install]
WantedBy=multi-user.target
`, binary, configPath, serviceEnv)

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

	for _, job := range cfg.Jobs {
		pruneService := fmt.Sprintf(`[Unit]
Description=Resticara prune for %s
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s --config=%s prune %s
%s

[Install]
WantedBy=multi-user.target
`, job.Key, binary, configPath, job.Repository, serviceEnv)

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
`, job.Key, job.PruneDays)

		base := "resticara-" + sanitizeName(job.Key) + "-prune"
		plan.Units = append(plan.Units,
			Unit{Name: base + ".service", Content: pruneService},
			Unit{Name: base + ".timer", Content: pruneTimer},
		)
		plan.Timers = append(plan.Timers, base+".timer")
	}
	return plan
}

func sanitizeName(name string) string {
	replacer := strings.NewReplacer(":", "-", "/", "-", " ", "-")
	return replacer.Replace(name)
}

// Systemctl runs systemctl commands.
type Systemctl interface {
	Output(args ...string) ([]byte, error)
	Run(args ...string) error
}

// ExecSystemctl runs the systemctl binary.
type ExecSystemctl struct{}

func (ExecSystemctl) Output(args ...string) ([]byte, error) {
	return exec.Command("systemctl", args...).Output()
}

func (ExecSystemctl) Run(args ...string) error {
	return exec.Command("systemctl", args...).Run()
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
		sys.Run("disable", "--now", base+".timer")
		sys.Run("disable", "--now", base+".service")
		os.Remove(filepath.Join(unitDir, base+".timer"))
		os.Remove(filepath.Join(unitDir, base+".service"))
	}

	for _, unit := range plan.Units {
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
	envFile := filepath.Join(filepath.Dir(configPath), EnvFile)
	if _, err := os.Stat(envFile); err != nil {
		fmt.Fprintf(out, "Warning: %s not found; restic credentials (RESTIC_PASSWORD, B2_ACCOUNT_ID, B2_ACCOUNT_KEY) must be set there for the timers to work.\n", envFile)
	} else if warning := config.PermissionWarning(envFile); warning != "" {
		fmt.Fprintln(out, warning)
	}
	return nil
}
