//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// matrixServer records the formatted bodies of the messages sent to it.
type matrixServer struct {
	*httptest.Server
	mu       sync.Mutex
	messages []string
}

func newMatrixServer(t *testing.T) *matrixServer {
	m := &matrixServer{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/send/m.room.message/") {
			var msg map[string]string
			json.NewDecoder(r.Body).Decode(&msg)
			m.mu.Lock()
			m.messages = append(m.messages, msg["formatted_body"])
			m.mu.Unlock()
		}
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *matrixServer) sent() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.messages...)
}

func (m *matrixServer) config(notify string) string {
	return `[general]
hostID = server-1
notify = ` + notify + `

[smtp]
enabled = false

[matrix]
enabled = true
server = ` + m.URL + `
access_token = test-token
room_id = !room:example.com

`
}

func TestPerJobSummary(t *testing.T) {
	t.Parallel()
	matrix := newMatrixServer(t)
	e := newEnv(t)
	okRepo := e.initRepo("repo-ok")
	e.writeFile("x", "data", "file.txt")
	e.stub("mysqldump", "exit 2\n")
	config := e.writeConfig(matrix.config("always") + dirJob("site", okRepo, e.path("data")) + mysqlJob("site", e.initRepo("repo-db"), "website_db"))

	res := e.resticara("--config="+config, "run")
	res.expect(t, 1, "Status: BACKUP FAILED!", "Matrix message sent!")
	for _, line := range []string{`(?m)^  OK      dir:site \(\d+s\)$`, `(?m)^  FAILED  mysql:site \(\d+s\)$`} {
		if !regexp.MustCompile(line).MatchString(res.output) {
			t.Errorf("no summary line matching %s in:\n%s", line, res.output)
		}
	}

	sent := matrix.sent()
	if len(sent) != 1 {
		t.Fatalf("got %d messages, want 1", len(sent))
	}
	if !strings.Contains(sent[0], "❌ [server-1] BACKUP FAILED: mysql:site</b>") || !strings.Contains(sent[0], "✅ <b>dir:site</b> OK") {
		t.Errorf("message:\n%s", sent[0])
	}
}

func TestNotifyProblems(t *testing.T) {
	t.Parallel()
	matrix := newMatrixServer(t)
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("x", "data", "file.txt")
	config := e.writeConfig(matrix.config("problems") + dirJob("site", repo, e.path("data")))

	// a successful run is only logged
	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful", "not sending notifications (notify = problems)")
	if n := len(matrix.sent()); n != 0 {
		t.Fatalf("sent %d messages for a successful run", n)
	}

	// a failing run is reported
	config = e.writeConfig(matrix.config("problems") + dirJob("site", e.path("not-a-repo"), e.path("data")))
	e.resticara("--config="+config, "run").expect(t, 1, "Status: BACKUP FAILED!", "Matrix message sent!")
	if sent := matrix.sent(); len(sent) != 1 || !strings.Contains(sent[0], "BACKUP FAILED: dir:site") {
		t.Errorf("messages = %q", sent)
	}
}
