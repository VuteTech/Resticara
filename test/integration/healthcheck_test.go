//go:build integration

package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type ping struct {
	method, path, rid, body string
}

// healthchecksServer records pings like a Healthchecks instance.
type healthchecksServer struct {
	*httptest.Server
	mu    sync.Mutex
	pings []ping
}

func newHealthchecksServer(t *testing.T, status int) *healthchecksServer {
	h := &healthchecksServer{}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.pings = append(h.pings, ping{r.Method, r.URL.Path, r.URL.Query().Get("rid"), string(body)})
		h.mu.Unlock()
		w.WriteHeader(status)
		w.Write([]byte("OK"))
	}))
	t.Cleanup(h.Close)
	return h
}

func (h *healthchecksServer) received() []ping {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]ping(nil), h.pings...)
}

// expectRun checks for a start ping and an end ping of the same run with the exit code.
func expectRun(t *testing.T, pings []ping, check, exitCode string) ping {
	t.Helper()
	if len(pings) != 2 || pings[0].method != "GET" || pings[0].path != "/"+check+"/start" ||
		pings[1].method != "POST" || pings[1].path != "/"+check+"/"+exitCode || pings[0].rid == "" || pings[0].rid != pings[1].rid {
		t.Fatalf("pings = %+v, want start and %s for %s with one run ID", pings, exitCode, check)
	}
	return pings[1]
}

func healthcheckConfig(h *healthchecksServer, notify string) string {
	return `[general]
hostID = server-1
notify = ` + notify + `

[smtp]
enabled = false

[healthcheck]
url = ` + h.URL + `/backup-check
prune_url = ` + h.URL + `/prune-check

`
}

func TestHealthcheckPings(t *testing.T) {
	t.Parallel()
	h := newHealthchecksServer(t, http.StatusOK)
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("x", "data", "file.txt")
	// successful runs are pinged even when notifications are only sent for problems
	config := e.writeConfig(healthcheckConfig(h, "problems") + dirJob("site", repo, e.path("data")))

	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful")
	end := expectRun(t, h.received(), "backup-check", "0")
	if !strings.Contains(end.body, "[server-1] Backup successful") || !strings.Contains(end.body, "OK      dir:site") {
		t.Errorf("report body:\n%s", end.body)
	}
}

func TestHealthcheckFailedRun(t *testing.T) {
	t.Parallel()
	h := newHealthchecksServer(t, http.StatusOK)
	e := newEnv(t)
	e.writeFile("x", "data", "file.txt")
	config := e.writeConfig(healthcheckConfig(h, "always") + dirJob("site", e.path("not-a-repo"), e.path("data")))

	e.resticara("--config="+config, "run").expect(t, 1, "Status: BACKUP FAILED!")
	end := expectRun(t, h.received(), "backup-check", "1")
	if !strings.Contains(end.body, "BACKUP FAILED: dir:site") {
		t.Errorf("report body:\n%s", end.body)
	}
}

// TestHealthcheckEarlyError checks that a run failing before any backup still ends its ping.
func TestHealthcheckEarlyError(t *testing.T) {
	t.Parallel()
	h := newHealthchecksServer(t, http.StatusOK)
	e := newEnv(t)
	config := e.writeConfig(healthcheckConfig(h, "always") + dirJob("site", e.path("repo"), e.path("data")))

	e.resticara("--config="+config, "--mail_template="+e.path("missing.txt"), "run").expect(t, 1, "Error parsing template")
	if end := expectRun(t, h.received(), "backup-check", "1"); !strings.Contains(end.body, "Error parsing template") {
		t.Errorf("report body: %q", end.body)
	}
}

func TestHealthcheckSkipsSingleJobRuns(t *testing.T) {
	t.Parallel()
	h := newHealthchecksServer(t, http.StatusOK)
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("x", "data", "file.txt")
	config := e.writeConfig(healthcheckConfig(h, "always") + dirJob("site", repo, e.path("data")))

	e.resticara("--config="+config, "run", "dir:site").expect(t, 0, "Status: Backup successful")
	if pings := h.received(); len(pings) != 0 {
		t.Errorf("a single job run sent pings: %+v", pings)
	}
}

func TestHealthcheckPrune(t *testing.T) {
	t.Parallel()
	h := newHealthchecksServer(t, http.StatusOK)
	e := newEnv(t)
	repo := e.initRepo("repo")
	config := e.writeConfig(healthcheckConfig(h, "always") + dirJob("site", repo, e.path("data")))

	e.resticara("--config="+config, "prune", repo).expect(t, 0, "Pruning repository "+repo)
	if end := expectRun(t, h.received(), "prune-check", "0"); !strings.Contains(end.body, "Pruning repository "+repo) {
		t.Errorf("prune body:\n%s", end.body)
	}
}

// TestHealthcheckUnreachable checks that a broken healthcheck does not hide anything:
// the backup still runs, and the run fails so the problem gets noticed.
func TestHealthcheckUnreachable(t *testing.T) {
	t.Parallel()
	h := newHealthchecksServer(t, http.StatusNotFound)
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("x", "data", "file.txt")
	config := e.writeConfig(healthcheckConfig(h, "always") + dirJob("site", repo, e.path("data")))

	e.resticara("--config="+config, "run").expect(t, 1,
		"Warning: healthcheck start ping failed: HTTP 404",
		"Status: Backup successful",
		"Warning: healthcheck 0 ping failed: HTTP 404",
	)
	if n := len(e.snapshots(repo)); n != 1 {
		t.Errorf("got %d snapshots, want 1", n)
	}
}

func TestHealthcheckInvalidURL(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	config := e.writeConfig(baseConfig + "[healthcheck]\nurl = hc-ping.com/abc\n")

	e.resticara("--config="+config, "validate").expect(t, 1, `'url' in [healthcheck] must be an http or https URL, got "hc-ping.com/abc"`)
}
