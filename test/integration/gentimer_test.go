//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// stubSystemctl makes systemctl report unitDir as the only unit path and log all other calls.
func stubSystemctl(e *env, unitDir, callLog string) {
	e.stub("systemctl", `if [ "$1" = show ]; then
  echo "UnitPath=`+unitDir+`"
  exit 0
fi
echo "$@" >> `+callLog+`
`)
}

func TestGentimer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	unitDir := e.path("units")
	callLog := e.path("systemctl.log")
	stubSystemctl(e, unitDir, callLog)
	e.writeFile("", "restic.env")
	if err := os.Chmod(e.path("restic.env"), 0o600); err != nil {
		t.Fatal(err)
	}

	// units left over from an older layout or from removed jobs, and an unrelated unit
	for _, name := range []string{"resticara-dir-old.service", "resticara-dir-old.timer", "resticara-dir-a.timer", "resticara-dir-a-prune.timer", "other.service"} {
		e.writeFile("stale", "units", name)
	}

	config := e.writeConfig(baseConfig + dirJob("a", "/srv/repo-a", "/var/www/a") + "retention_prune = 14\n\n" + mysqlJob("b", "b2:bucket:mysql/", "b_db"))
	e.resticara("--config="+config, "gentimer").expect(t, 0, "Systemd timer files written to "+unitDir)

	entries, err := os.ReadDir(unitDir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range entries {
		files = append(files, entry.Name())
	}
	want := []string{
		"other.service",
		"resticara-backup.service",
		"resticara-backup.timer",
		"resticara-dir-a-prune.service",
		"resticara-dir-a-prune.timer",
		"resticara-mysql-b-prune.service",
		"resticara-mysql-b-prune.timer",
	}
	if strings.Join(files, " ") != strings.Join(want, " ") {
		t.Fatalf("unit files:\n%s\nwant:\n%s", strings.Join(files, "\n"), strings.Join(want, "\n"))
	}

	bin, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	unit := func(name string) string {
		content, err := os.ReadFile(filepath.Join(unitDir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	expectLines(t, "resticara-backup.service", unit("resticara-backup.service"),
		"ExecStart="+bin+" --config="+config+" run",
		"EnvironmentFile=-"+e.path("restic.env"),
		"Environment=RESTIC_CACHE_DIR=/var/cache/resticara",
		"CacheDirectory=resticara",
	)
	expectLines(t, "resticara-backup.timer", unit("resticara-backup.timer"), "OnCalendar=daily", "Persistent=true")
	expectLines(t, "resticara-dir-a-prune.service", unit("resticara-dir-a-prune.service"),
		"ExecStart="+bin+" --config="+config+" prune /srv/repo-a",
		"EnvironmentFile=-"+e.path("restic.env"),
	)
	// the prune timer must fire on its own: OnUnitActiveSec alone never does
	expectLines(t, "resticara-dir-a-prune.timer", unit("resticara-dir-a-prune.timer"), "OnActiveSec=1h", "OnUnitActiveSec=14d", "RandomizedDelaySec=1h")
	expectLines(t, "resticara-mysql-b-prune.timer", unit("resticara-mysql-b-prune.timer"), "OnUnitActiveSec=30d")
	expectLines(t, "resticara-mysql-b-prune.service", unit("resticara-mysql-b-prune.service"),
		"ExecStart="+bin+" --config="+config+" prune b2:bucket:mysql/",
	)

	checkSystemctlCalls(t, callLog,
		[]string{
			"disable --now resticara-dir-a.service",
			"disable --now resticara-dir-a.timer",
			"disable --now resticara-dir-old.service",
			"disable --now resticara-dir-old.timer",
		},
		[]string{"resticara-backup.timer", "resticara-dir-a-prune.timer", "resticara-mysql-b-prune.timer"},
	)
}

func TestGentimerWarnsAboutMissingEnvFile(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	stubSystemctl(e, e.path("units"), e.path("systemctl.log"))
	if err := os.MkdirAll(e.path("units"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := e.writeConfig(baseConfig + dirJob("a", "/srv/repo-a", "/var/www/a"))

	e.resticara("--config="+config, "gentimer").expect(t, 0, e.path("restic.env")+" not found")
}

func expectLines(t *testing.T, name, content string, lines ...string) {
	t.Helper()
	have := make(map[string]bool)
	for _, line := range strings.Split(content, "\n") {
		have[line] = true
	}
	for _, line := range lines {
		if !have[line] {
			t.Errorf("%s does not contain line %q:\n%s", name, line, content)
		}
	}
}

// checkSystemctlCalls checks that stale units are disabled first, then systemd is
// reloaded, then every timer is enabled and restarted. The order inside each step
// is not fixed.
func checkSystemctlCalls(t *testing.T, callLog string, disabled, timers []string) {
	t.Helper()
	content, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(content)), "\n")

	reload := -1
	for i, call := range calls {
		if call == "daemon-reload" {
			reload = i
		}
	}
	if reload < 0 {
		t.Fatalf("no daemon-reload in systemctl calls:\n%s", content)
	}

	before := append([]string(nil), calls[:reload]...)
	sort.Strings(before)
	if strings.Join(before, "\n") != strings.Join(disabled, "\n") {
		t.Errorf("calls before daemon-reload:\n%s\nwant:\n%s", strings.Join(before, "\n"), strings.Join(disabled, "\n"))
	}

	after := calls[reload+1:]
	if len(after) != 2*len(timers) {
		t.Errorf("calls after daemon-reload:\n%s\nwant enable and restart for %v", strings.Join(after, "\n"), timers)
	}
	for _, timer := range timers {
		enable, restart := -1, -1
		for i, call := range after {
			switch call {
			case "enable " + timer:
				enable = i
			case "restart " + timer:
				restart = i
			}
		}
		if enable < 0 || restart < enable {
			t.Errorf("%s was not enabled and then restarted:\n%s", timer, strings.Join(after, "\n"))
		}
	}
}
