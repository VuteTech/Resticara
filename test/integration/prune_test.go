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

func TestPruneLimits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("x", "data", "file.txt")
	config := e.writeConfig(baseConfig + dirJob("site", repo, e.path("data")) + "prune_max_unused = 10%\nprune_max_repack_size = 1G\n")
	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful")

	// restic accepts the limits and prunes the repository
	e.resticara("--config="+config, "prune", repo).expect(t, 0, "Pruning repository "+repo, "unused size after prune")
}

func TestPruneFailureNotification(t *testing.T) {
	t.Parallel()
	matrix := newMatrixServer(t)
	e := newEnv(t)
	missing := e.path("not-a-repo")
	config := e.writeConfig(matrix.config("problems") + dirJob("site", missing, e.path("data")))

	e.resticara("--config="+config, "prune", "all").expect(t, 1, "Prune failed for "+missing, "Matrix message sent!")
	sent := matrix.sent()
	if len(sent) != 1 || !strings.Contains(sent[0], "❌ [server-1] PRUNE FAILED: "+missing) || !strings.Contains(sent[0], "<pre><code>Pruning repository "+missing) {
		t.Errorf("messages = %q", sent)
	}
}

func TestPruneSuccessIsQuiet(t *testing.T) {
	t.Parallel()
	matrix := newMatrixServer(t)
	e := newEnv(t)
	repo := e.initRepo("repo")
	config := e.writeConfig(matrix.config("always") + dirJob("site", repo, e.path("data")))

	e.resticara("--config="+config, "prune", "all").expect(t, 0, "Pruning repository "+repo)
	if sent := matrix.sent(); len(sent) != 0 {
		t.Errorf("a successful prune sent %q", sent)
	}
}
