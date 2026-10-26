//go:build integration

package integration

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const hostConfig = `[general]
hostID = server-1

[smtp]
enabled = false

`

func hosts(snapshots []snapshot) map[string]int {
	counts := make(map[string]int)
	for _, s := range snapshots {
		counts[s.Hostname]++
	}
	return counts
}

// TestHostIDMigration covers setting hostID on a machine whose snapshots were made
// under its hostname: the run points out the old snapshots, and the suggested
// command assigns them to hostID.
func TestHostIDMigration(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	data := e.path("data")
	e.writeFile("x", "data", "file.txt")
	e.restic("backup", "-r", repo, "--host", "old-name", data)
	config := e.writeConfig(hostConfig + dirJob("site", repo, data))

	res := e.resticara("--config="+config, "run")
	res.expect(t, 0, "Status: Backup successful", "backup --host server-1 ", "forget --host server-1 ", `1 snapshots of these paths belong to host "old-name"`)
	if got := hosts(e.snapshots(repo)); got["server-1"] != 1 || got["old-name"] != 1 {
		t.Fatalf("hosts = %v, want one snapshot each", got)
	}

	// run the suggested command exactly as printed
	var rewrite string
	for _, line := range strings.Split(res.output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "restic -r ") && strings.Contains(line, " rewrite ") {
			rewrite = strings.TrimSpace(line)
		}
	}
	if rewrite == "" {
		t.Fatalf("no rewrite command in output:\n%s", res.output)
	}
	cmd := exec.Command("sh", "-c", rewrite)
	cmd.Env = e.environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", rewrite, err, out)
	}
	if got := hosts(e.snapshots(repo)); got["server-1"] != 2 || len(got) != 1 {
		t.Fatalf("hosts after rewrite = %v, want 2 snapshots of server-1", got)
	}

	res = e.resticara("--config="+config, "run")
	res.expect(t, 0, "Status: Backup successful")
	if strings.Contains(res.output, "Note:") {
		t.Errorf("note still shown after the rewrite:\n%s", res.output)
	}
}

// TestForgetLeavesOtherHosts checks that retention only touches this host's
// snapshots when several machines share a repository.
func TestForgetLeavesOtherHosts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	data := e.path("data")
	e.writeFile("x", "data", "file.txt")
	for i := 0; i < 3; i++ {
		e.restic("backup", "-r", repo, "--host", "other-server", data)
	}
	// keeps the latest and the oldest snapshot of a day, so forget without --host
	// would remove one of the other server's three snapshots
	config := e.writeConfig(hostConfig + dirJob("site", repo, data))

	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful")
	if got := hosts(e.snapshots(repo)); got["other-server"] != 3 || got["server-1"] != 1 {
		t.Errorf("hosts = %v, want 3 of other-server and 1 of server-1", got)
	}
}

func TestHostnameMode(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("x", "data", "file.txt")
	config := e.writeConfig(baseConfig + dirJob("site", repo, e.path("data")))

	res := e.resticara("--config="+config, "run")
	res.expect(t, 0, "Status: Backup successful")
	if strings.Contains(res.output, "--host") {
		t.Errorf("--host passed with hostID = hostname:\n%s", res.output)
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if got := hosts(e.snapshots(repo)); got[hostname] != 1 {
		t.Errorf("hosts = %v, want the machine's hostname %q", got, hostname)
	}
}
