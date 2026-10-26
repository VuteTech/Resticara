//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// TestMatrixNotification checks the whole reporting path: the template is found
// next to the config while running from "/", rendered, and sent to the homeserver.
func TestMatrixNotification(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var messages []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"errcode":"M_UNKNOWN_TOKEN"}`))
			return
		}
		if strings.Contains(r.URL.Path, "/send/m.room.message/") {
			var msg map[string]string
			json.NewDecoder(r.Body).Decode(&msg)
			mu.Lock()
			messages = append(messages, msg)
			mu.Unlock()
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("hello", "data", "index.html")
	// mark the template next to the config, so the test proves that this one was used
	// and not one from /etc/resticara on the machine running the tests
	template, err := os.ReadFile(e.path("templates", "matrix_template.html"))
	if err != nil {
		t.Fatal(err)
	}
	e.writeFile(string(template)+"<p>template-next-to-config</p>\n", "templates", "matrix_template.html")
	config := e.writeConfig(baseConfig + `[matrix]
enabled = true
server = ` + srv.URL + `
access_token = test-token
room_id = !room:example.com

` + dirJob("site", repo, e.path("data")))

	e.resticara("--config="+config, "run").expect(t, 0, "Matrix message sent!")

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(messages))
	}
	msg := messages[0]
	if !strings.Contains(msg["formatted_body"], "Backup successful") || !strings.Contains(msg["formatted_body"], "dir:site") || !strings.Contains(msg["formatted_body"], "template-next-to-config") {
		t.Errorf("unexpected formatted_body:\n%s", msg["formatted_body"])
	}
	if strings.Contains(msg["body"], "<") {
		t.Errorf("plain body contains HTML:\n%s", msg["body"])
	}
}

func TestNotificationFailureExitCode(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"errcode":"M_FORBIDDEN","error":"not in room"}`))
	}))
	defer srv.Close()

	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("hello", "data", "index.html")
	config := e.writeConfig(baseConfig + `[matrix]
enabled = true
server = ` + srv.URL + `
access_token = test-token
room_id = !room:example.com

` + dirJob("site", repo, e.path("data")))

	// the backup itself works, but nobody would hear about failures, so the run fails
	e.resticara("--config="+config, "run").expect(t, 1, "Status: Backup successful", "M_FORBIDDEN")
}
