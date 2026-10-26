package healthcheck

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type request struct {
	method, path, rid, query, body string
}

// server answers with the given status and body and records the requests.
type server struct {
	*httptest.Server
	mu       sync.Mutex
	requests []request
	answers  []int // status per request; 200 when the list runs out
	text     string
}

func newServer(t *testing.T, answers ...int) *server {
	s := &server{answers: answers, text: "OK"}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.requests = append(s.requests, request{r.Method, r.URL.Path, r.URL.Query().Get("rid"), r.URL.RawQuery, string(body)})
		status := http.StatusOK
		if len(s.answers) > 0 {
			status, s.answers = s.answers[0], s.answers[1:]
		}
		s.mu.Unlock()
		w.WriteHeader(status)
		w.Write([]byte(s.text))
	}))
	t.Cleanup(s.Close)
	return s
}

func testPinger(url string) *Pinger {
	p := New(url)
	p.RetryDelay = time.Millisecond
	return p
}

func TestStartAndFinish(t *testing.T) {
	s := newServer(t)
	p := testPinger(s.URL + "/check-uuid")

	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	if err := p.Finish(1, "report"); err != nil {
		t.Fatal(err)
	}

	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(p.RunID) {
		t.Errorf("RunID %q is not a version 4 UUID", p.RunID)
	}
	want := []request{
		{"GET", "/check-uuid/start", p.RunID, "rid=" + p.RunID, ""},
		{"POST", "/check-uuid/1", p.RunID, "rid=" + p.RunID, "report"},
	}
	if len(s.requests) != 2 || s.requests[0] != want[0] || s.requests[1] != want[1] {
		t.Errorf("requests:\n%+v\nwant:\n%+v", s.requests, want)
	}
	if New("x").RunID == New("x").RunID {
		t.Error("run IDs repeat")
	}
}

func TestSlugURLKeepsQuery(t *testing.T) {
	s := newServer(t)
	p := testPinger(s.URL + "/ping-key/website-backup/?create=1")
	if err := p.Finish(0, ""); err != nil {
		t.Fatal(err)
	}
	if r := s.requests[0]; r.path != "/ping-key/website-backup/0" || r.query != "create=1&rid="+p.RunID {
		t.Errorf("request %+v", r)
	}
}

func TestRetries(t *testing.T) {
	// a server error is retried
	s := newServer(t, 502, 503)
	if err := testPinger(s.URL).Start(); err != nil || len(s.requests) != 3 {
		t.Errorf("error %v after %d requests, want success after 3", err, len(s.requests))
	}

	// but only as often as configured
	s = newServer(t, 500, 500, 500, 500)
	err := testPinger(s.URL).Start()
	if err == nil || len(s.requests) != 3 || !strings.Contains(err.Error(), "healthcheck start ping failed: HTTP 500") {
		t.Errorf("error %v after %d requests", err, len(s.requests))
	}

	// a client error is not retried
	s = newServer(t, 404)
	if err := testPinger(s.URL).Finish(0, ""); err == nil || len(s.requests) != 1 {
		t.Errorf("error %v after %d requests, want an error after 1", err, len(s.requests))
	}

	// neither is an unreachable server forever
	s = newServer(t)
	s.Close()
	if err := testPinger(s.URL).Finish(0, ""); err == nil || !strings.Contains(err.Error(), "healthcheck 0 ping failed") {
		t.Errorf("error = %v", err)
	}
}

func TestOKAnswersThatAreErrors(t *testing.T) {
	for text, want := range map[string]string{
		"OK (not found)":    "no check found for this URL",
		"OK (rate limited)": "rate limited",
	} {
		s := newServer(t)
		s.text = text
		err := testPinger(s.URL).Start()
		if err == nil || !strings.Contains(err.Error(), want) || len(s.requests) != 1 {
			t.Errorf("%q: error %v after %d requests", text, err, len(s.requests))
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate short = %q", got)
	}
	// "ä" is two bytes; cutting through it drops it
	if got := truncate("abä", 3); got != "ab" {
		t.Errorf("truncate through a character = %q", got)
	}
	if got := truncate("abä", 4); got != "abä" {
		t.Errorf("truncate at a boundary = %q", got)
	}

	s := newServer(t)
	long := strings.Repeat("€", MaxBody) // 3 bytes each
	if err := testPinger(s.URL).Finish(0, long); err != nil {
		t.Fatal(err)
	}
	if body := s.requests[0].body; len(body) > MaxBody || len(body) < MaxBody-3 || !strings.HasPrefix(long, body) {
		t.Errorf("body has %d bytes", len(body))
	}
}
