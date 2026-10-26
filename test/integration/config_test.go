//go:build integration

package integration

import (
	"os"
	"sort"
	"strings"
	"testing"
)

func TestSeveralDirectoriesAndSpaces(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	site := e.path("site")
	files := e.path("My Files")
	e.writeFile("site", "site", "index.html")
	e.writeFile("notes", "My Files", "notes.txt")
	config := e.writeConfig(baseConfig + `[dir:site]
bucket = ` + repo + `
directory = ` + site + `
directory = ` + files + `
retention_daily = 2
retention_weekly = 7
retention_monthly = 3
`)

	// the report shows the path with a space quoted, so the command can be copied into a shell
	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful", "backup "+site+" '"+files+"'")

	snapshots := e.snapshots(repo)
	if len(snapshots) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(snapshots))
	}
	paths := snapshots[0].Paths
	sort.Strings(paths)
	want := []string{files, site}
	sort.Strings(want)
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Errorf("snapshot paths = %q, want %q", paths, want)
	}
	if out := e.restic("dump", "-r", repo, "latest", files+"/notes.txt"); out != "notes" {
		t.Errorf("restored file = %q", out)
	}
}

func TestMysqldumpArgsAndAllDatabases(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	argsLog := e.path("mysqldump.args")
	e.stub("mysqldump", `echo "$@" > `+argsLog+`
echo "-- dump"
`)
	config := e.writeConfig(baseConfig + `[mysql:all]
bucket = ` + repo + `
database = --all-databases
mysqldump_args = --routines --events
retention_daily = 2
retention_weekly = 7
retention_monthly = 3
`)

	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful")

	args, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(args)); got != "--single-transaction --routines --events --all-databases" {
		t.Errorf("mysqldump args = %q", got)
	}
	// same snapshot path as before this option existed, so forget keeps grouping old snapshots
	if snapshots := e.snapshots(repo); len(snapshots) != 1 || snapshots[0].Paths[0] != "/--all-databases.sql" {
		t.Errorf("snapshots = %+v", snapshots)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.writeFile("x", "data", "file.txt")
	config := e.writeConfig(baseConfig + dirJob("site", e.path("repo"), e.path("data")) + dirJob("typo", e.path("repo"), e.path("dta")))

	e.resticara("--config="+config, "validate").expect(t, 0,
		"Warning: directory \""+e.path("dta")+"\" of dir:typo does not exist",
		"Configuration "+config+" is valid: 2 backup jobs.",
	)
}

func TestValidateReportsAllErrors(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	config := e.writeConfig(baseConfig + `[dir:site]
bucket = /srv/repo
retention_daily = 1
retention_weekly = x
retention_monthly = 1

[mysql:db]
bucket = /srv/repo-db
bucket = /srv/other
database = website_db
retention_daily = 1
retention_weekly = 1
retention_monthly = 1
retention_dialy = 3
`)

	res := e.resticara("--config="+config, "validate")
	res.expect(t, 1,
		"Warning: unknown key 'retention_dialy' in [mysql:db] is ignored",
		"Error reading config:\n",
		"  'directory' is required for dir:site\n",
		"  'retention_weekly' for dir:site must be an integer\n",
		"  'bucket' is set more than once in [mysql:db]\n",
	)
	// a run with the same config must not start any backup
	e.resticara("--config="+config, "run").expect(t, 1, "Error reading config:")
}

// TestSpaceSeparatedDirectories covers configs that listed several directories on one
// line, which used to work by accident because commands were split on spaces.
func TestSpaceSeparatedDirectories(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("a", "a", "file.txt")
	e.writeFile("b", "b", "file.txt")
	config := e.writeConfig(baseConfig + dirJob("site", repo, e.path("a")+" "+e.path("b")))

	e.resticara("--config="+config, "run").expect(t, 1,
		"put each on its own 'directory =' line",
		"Status: BACKUP FAILED!",
	)
}

func TestRunPrintsConfigWarnings(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("x", "data", "file.txt")
	config := e.writeConfig(baseConfig + dirJob("site", repo, e.path("data")) + "excldue = *.tmp\n")

	e.resticara("--config="+config, "run").expect(t, 0,
		"Warning: unknown key 'excldue' in [dir:site] is ignored",
		"Status: Backup successful",
	)
}
