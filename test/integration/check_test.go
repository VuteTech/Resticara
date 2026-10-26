//go:build integration

package integration

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	t.Parallel()
	h := newHealthchecksServer(t, http.StatusOK)
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("x", "data", "file.txt")
	config := e.writeConfig(healthcheckConfig(h, "always") + "check_url = " + h.URL + "/check-check\n\n" +
		dirJob("site", repo, e.path("data")) + "check_read_data_subset = 100%\n")
	e.resticara("--config="+config, "run", "dir:site").expect(t, 0, "Status: Backup successful")

	e.resticara("--config="+config, "check", "all").expect(t, 0, "Checking repository "+repo, "read 100.0% of packfiles", "no errors were found")
	if end := expectRun(t, h.received(), "check-check", "0"); !strings.Contains(end.body, "no errors were found") {
		t.Errorf("check body:\n%s", end.body)
	}

	e.resticara("--config="+config, "check", e.path("other")).expect(t, 1, "not found in config")
	e.resticara("--config="+config, "check").expect(t, 2, "Usage: resticara check <all|repository>")
}

// TestCheckFindsDamage removes a data file from the repository: the check must fail
// and report it through the notification channels.
func TestCheckFindsDamage(t *testing.T) {
	t.Parallel()
	matrix := newMatrixServer(t)
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("x", "data", "file.txt")
	config := e.writeConfig(matrix.config("problems") + dirJob("site", repo, e.path("data")) + "check_read_data_subset = 100%\n")
	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful")

	packs, err := filepath.Glob(filepath.Join(repo, "data", "*", "*"))
	if err != nil || len(packs) == 0 {
		t.Fatalf("no pack files in the repository: %v", err)
	}
	for _, pack := range packs {
		if err := os.Remove(pack); err != nil {
			t.Fatal(err)
		}
	}

	e.resticara("--config="+config, "check", repo).expect(t, 1, "Check failed for "+repo, "Matrix message sent!")
	sent := matrix.sent()
	if len(sent) != 1 || !strings.Contains(sent[0], "❌ [server-1] CHECK FAILED: "+repo) {
		t.Errorf("messages = %q", sent)
	}
}
