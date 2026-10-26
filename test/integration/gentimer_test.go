//go:build integration

package integration

import (
	"os"
	"os/exec"
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

	// units left over from older layouts (per-job backup and prune timers) or from
	// removed jobs, and an unrelated unit
	stale := []string{"resticara-dir-old.service", "resticara-dir-old.timer", "resticara-dir-a.timer", "resticara-dir-a-prune.service", "resticara-dir-a-prune.timer"}
	for _, name := range append(stale, "other.service") {
		e.writeFile("stale", "units", name)
	}

	// mysql:b and mysql:c share a repository, which is pruned once, every 7 days; the
	// reopened [mysql:b] section gives both jobs the same prune limits, as required
	config := e.writeConfig(baseConfig +
		dirJob("a", "/srv/repo-a", "/var/www/a") + "retention_prune = 14\n\n" +
		mysqlJob("b", "b2:bucket:mysql/", "b_db") +
		mysqlJob("c", "b2:bucket:mysql/", "c_db") + "retention_prune = 7\nprune_max_repack_size = 2G\n\n" +
		"[mysql:b]\nprune_max_repack_size = 2G\n")
	e.resticara("--config="+config, "gentimer").expect(t, 0, "Systemd timer files written to "+unitDir)

	entries, err := os.ReadDir(unitDir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range entries {
		files = append(files, entry.Name())
	}
	repoA := unitWithPrefix(t, files, "resticara-prune-srv-repo-a-")
	mysql := unitWithPrefix(t, files, "resticara-prune-b2-bucket-mysql-")
	checkA := unitWithPrefix(t, files, "resticara-check-srv-repo-a-")
	checkMysql := unitWithPrefix(t, files, "resticara-check-b2-bucket-mysql-")
	want := []string{"other.service",
		"resticara-backup.service", "resticara-backup.timer",
		checkMysql + ".service", checkMysql + ".timer", checkA + ".service", checkA + ".timer",
		mysql + ".service", mysql + ".timer", repoA + ".service", repoA + ".timer"}
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
	expectLines(t, "resticara-backup.service", unit("resticara-backup.service"),
		"TimeoutStartSec=20h",
		"Nice=10",
		"IOSchedulingClass=best-effort",
		"ProtectSystem=strict",
		"ProtectHome=read-only",
		// the local repository stays writable inside the sandbox; the B2 one needs nothing
		"ReadWritePaths=-/srv/repo-a",
	)
	expectLines(t, "resticara-backup.timer", unit("resticara-backup.timer"), "OnCalendar=daily", "Persistent=true")
	expectLines(t, repoA+".service", unit(repoA+".service"),
		"Description=Resticara prune for /srv/repo-a",
		"ExecStart="+bin+" --config="+config+" prune /srv/repo-a",
		"EnvironmentFile=-"+e.path("restic.env"),
	)
	// the prune timer must fire on its own: OnUnitActiveSec alone never does
	expectLines(t, repoA+".timer", unit(repoA+".timer"), "OnActiveSec=1h", "OnUnitActiveSec=14d", "RandomizedDelaySec=1h")
	expectLines(t, mysql+".timer", unit(mysql+".timer"), "OnUnitActiveSec=7d")
	expectLines(t, mysql+".service", unit(mysql+".service"),
		"After=network-online.target resticara-backup.service",
		"ExecStart="+bin+" --config="+config+" prune b2:bucket:mysql/",
	)
	expectLines(t, checkA+".service", unit(checkA+".service"),
		"After=network-online.target resticara-backup.service "+repoA+".service",
		"ExecStart="+bin+" --config="+config+" check /srv/repo-a",
		"EnvironmentFile=-"+e.path("restic.env"),
	)
	expectLines(t, checkA+".timer", unit(checkA+".timer"), "OnActiveSec=6h", "OnUnitActiveSec=30d", "RandomizedDelaySec=1h")
	expectLines(t, mysql+".service", unit(mysql+".service"), "TimeoutStartSec=12h", "ProtectSystem=strict")
	if strings.Contains(unit(mysql+".service"), "ReadWritePaths") {
		t.Errorf("the prune of a B2 repository got ReadWritePaths:\n%s", unit(mysql+".service"))
	}

	// let systemd itself check the generated units where it is available
	if _, err := exec.LookPath("systemd-analyze"); err == nil {
		var paths []string
		for _, f := range files {
			if strings.HasPrefix(f, "resticara-") {
				paths = append(paths, filepath.Join(unitDir, f))
			}
		}
		verify := exec.Command("systemd-analyze", append([]string{"verify", "--man=no"}, paths...)...)
		if out, err := verify.CombinedOutput(); err != nil || len(strings.TrimSpace(string(out))) > 0 {
			t.Errorf("systemd-analyze verify: %v\n%s", err, out)
		}
	}

	var disabled []string
	for _, base := range []string{"resticara-dir-a", "resticara-dir-a-prune", "resticara-dir-old"} {
		disabled = append(disabled, "disable --now "+base+".service", "disable --now "+base+".timer")
	}
	sort.Strings(disabled)
	checkSystemctlCalls(t, callLog, disabled, []string{"resticara-backup.timer", mysql + ".timer", checkMysql + ".timer", repoA + ".timer", checkA + ".timer"})
}

// unitWithPrefix returns the base name of the unit whose name starts with prefix.
func unitWithPrefix(t *testing.T, files []string, prefix string) string {
	t.Helper()
	for _, f := range files {
		if strings.HasPrefix(f, prefix) && strings.HasSuffix(f, ".service") {
			return strings.TrimSuffix(f, ".service")
		}
	}
	t.Fatalf("no unit starting with %s in %v", prefix, files)
	return ""
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
