//go:build integration

package integration

import (
	"strings"
	"testing"
)

func TestPrune(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repoA := e.initRepo("repo-a")
	repoB := e.initRepo("repo-b")
	e.writeFile("a", "a", "file.txt")
	e.writeFile("b", "b", "file.txt")
	config := e.writeConfig(baseConfig + dirJob("a", repoA, e.path("a")) + dirJob("b", repoB, e.path("b")))
	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful")

	res := e.resticara("--config="+config, "prune", "all")
	res.expect(t, 0, "Pruning repository "+repoA, "Pruning repository "+repoB)
	if n := strings.Count(res.output, "Pruning repository"); n != 2 {
		t.Errorf("pruned %d repositories, want 2", n)
	}

	e.resticara("--config="+config, "prune", repoA).expect(t, 0, "Pruning repository "+repoA)
	e.resticara("--config="+config, "prune", e.path("other")).expect(t, 1, "not found in config")
	e.resticara("--config="+config, "prune").expect(t, 2, "Usage: resticara prune")
}

func TestPruneFailureExitCode(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// the repository was never initialized, so restic fails
	missing := e.path("not-a-repo")
	config := e.writeConfig(baseConfig + dirJob("a", missing, e.path("a")))

	e.resticara("--config="+config, "prune", "all").expect(t, 1, "Prune failed for "+missing)
}
