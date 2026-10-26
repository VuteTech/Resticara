//go:build integration

package integration

import (
	"os"
	"strings"
	"testing"
)

func TestDirBackupSucceeds(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	data := e.path("data")
	e.writeFile("hello", "data", "index.html")
	config := e.writeConfig(baseConfig + dirJob("site", repo, data))

	e.resticara("--config="+config, "run").expect(t, 0, "Executing command dir:site", "Status: Backup successful")

	snapshots := e.snapshots(repo)
	if len(snapshots) != 1 || len(snapshots[0].Paths) != 1 || snapshots[0].Paths[0] != data {
		t.Fatalf("snapshots = %+v, want one snapshot of %s", snapshots, data)
	}
	if out := e.restic("dump", "-r", repo, "latest", data+"/index.html"); out != "hello" {
		t.Errorf("restored file = %q, want %q", out, "hello")
	}
}

func TestUnreadableFileIsWarning(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root can read files without permissions")
	}
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("ok", "data", "ok.txt")
	locked := e.writeFile("secret", "data", "locked.txt")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	config := e.writeConfig(baseConfig + dirJob("site", repo, e.path("data")))

	// restic exits with 3 but saves the snapshot, which is a warning and not a failure
	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup completed with warnings", "locked.txt")

	if n := len(e.snapshots(repo)); n != 1 {
		t.Errorf("got %d snapshots, want 1", n)
	}
}

func TestMysqlBackup(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	argsLog := e.path("mysqldump.args")
	e.stub("mysqldump", `echo "$@" > `+argsLog+`
echo "CREATE TABLE posts (id int);"
`)
	config := e.writeConfig(baseConfig + mysqlJob("site", repo, "site_db"))

	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful")

	snapshots := e.snapshots(repo)
	if len(snapshots) != 1 || snapshots[0].Paths[0] != "/site_db.sql" {
		t.Fatalf("snapshots = %+v, want one snapshot of /site_db.sql", snapshots)
	}
	if out := e.restic("dump", "-r", repo, "latest", "/site_db.sql"); out != "CREATE TABLE posts (id int);\n" {
		t.Errorf("dump content = %q", out)
	}
	args, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(args)); got != "--single-transaction site_db" {
		t.Errorf("mysqldump args = %q, want %q", got, "--single-transaction site_db")
	}
}

func TestFailingMysqldumpFailsRun(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	dbRepo := e.initRepo("repo-db")
	dirRepo := e.initRepo("repo-dir")
	e.writeFile("hello", "data", "index.html")
	e.stub("mysqldump", `echo "partial dump"
echo "mysqldump: Got error: 1045: Access denied" >&2
exit 2
`)
	config := e.writeConfig(baseConfig + mysqlJob("site", dbRepo, "site_db") + dirJob("site", dirRepo, e.path("data")))

	e.resticara("--config="+config, "run").expect(t, 1, "Status: BACKUP FAILED!", "Access denied")

	// a failed dump must not be stored, while the other job still runs
	if n := len(e.snapshots(dbRepo)); n != 0 {
		t.Errorf("got %d snapshots of the failed dump, want 0", n)
	}
	if n := len(e.snapshots(dirRepo)); n != 1 {
		t.Errorf("got %d snapshots of the directory, want 1", n)
	}
}

func TestForgetAppliesRetention(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("v1", "data", "file.txt")
	config := e.writeConfig(baseConfig + dirJob("site", repo, e.path("data")))

	for i := 1; i <= 4; i++ {
		e.writeFile(strings.Repeat("x", i), "data", "file.txt")
		e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful")
	}

	// All runs happen on the same day, so the daily, weekly and monthly rules keep the latest
	// snapshot. restic also keeps the oldest one while the rules are not filled; the two in
	// between must be forgotten.
	if n := len(e.snapshots(repo)); n != 2 {
		t.Errorf("got %d snapshots after forget, want 2", n)
	}
	file := e.path("data", "file.txt")
	if out := e.restic("dump", "-r", repo, "latest", file); out != "xxxx" {
		t.Errorf("latest snapshot has %q, want %q", out, "xxxx")
	}
}

func TestRunSingleJob(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repoA := e.initRepo("repo-a")
	repoB := e.initRepo("repo-b")
	e.writeFile("a", "a", "file.txt")
	e.writeFile("b", "b", "file.txt")
	config := e.writeConfig(baseConfig + dirJob("a", repoA, e.path("a")) + dirJob("b", repoB, e.path("b")))

	e.resticara("--config="+config, "run", "dir:a").expect(t, 0, "Executing command dir:a")

	if n := len(e.snapshots(repoA)); n != 1 {
		t.Errorf("got %d snapshots in repo-a, want 1", n)
	}
	if n := len(e.snapshots(repoB)); n != 0 {
		t.Errorf("got %d snapshots in repo-b, want 0", n)
	}

	e.resticara("--config="+config, "run", "dir:missing").expect(t, 1, "Command dir:missing not found in config")
}

func TestJobsRunInOrder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	config := baseConfig
	for _, name := range []string{"c", "a", "b"} {
		e.writeFile(name, name, "file.txt")
		config += dirJob(name, e.initRepo("repo-"+name), e.path(name))
	}
	res := e.resticara("--config="+e.writeConfig(config), "run")
	res.expect(t, 0)

	a := strings.Index(res.output, "Executing command dir:a")
	b := strings.Index(res.output, "Executing command dir:b")
	c := strings.Index(res.output, "Executing command dir:c")
	if a < 0 || !(a < b && b < c) {
		t.Errorf("jobs did not run in sorted order:\n%s", res.output)
	}
}
