package matrix

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newHomeserver(t *testing.T, calls *[]string, sent *map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.Method+" "+strings.SplitN(r.URL.EscapedPath(), "/m.room.message/", 2)[0])
		if r.URL.Path != "/_matrix/client/v3/login" && r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"errcode":"M_UNKNOWN_TOKEN","error":"bad token"}`))
			return
		}
		switch {
		case r.URL.Path == "/_matrix/client/v3/login":
			w.Write([]byte(`{"access_token":"tok"}`))
		case strings.Contains(r.URL.Path, "/send/"):
			json.NewDecoder(r.Body).Decode(sent)
			w.Write([]byte(`{"event_id":"$1"}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
}

func TestSendWithPasswordLogsInAndOut(t *testing.T) {
	var calls []string
	var sent map[string]string
	srv := newHomeserver(t, &calls, &sent)
	defer srv.Close()

	err := ClientAPINotifier{}.Send(MatrixConfig{
		Homeserver: srv.URL + "/", Username: "@bot:example.com", Password: "pw",
		RoomID: "!room:example.com", Message: "<b>Backup successful</b><br/>a &amp; b",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /_matrix/client/v3/login",
		"POST /_matrix/client/v3/join/%21room:example.com",
		"PUT /_matrix/client/v3/rooms/%21room:example.com/send",
		"POST /_matrix/client/v3/logout",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	if sent["body"] != "Backup successful\na & b" || sent["format"] != "org.matrix.custom.html" {
		t.Errorf("unexpected message: %v", sent)
	}
}

func TestSendWithAccessTokenSkipsLogin(t *testing.T) {
	var calls []string
	var sent map[string]string
	srv := newHomeserver(t, &calls, &sent)
	defer srv.Close()

	if err := (ClientAPINotifier{}).Send(MatrixConfig{Homeserver: srv.URL, AccessToken: "tok", RoomID: "!r:x", Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || strings.Contains(strings.Join(calls, " "), "login") {
		t.Errorf("expected only join and send, got %v", calls)
	}
}

func TestSendReportsAPIError(t *testing.T) {
	var calls []string
	var sent map[string]string
	srv := newHomeserver(t, &calls, &sent)
	defer srv.Close()

	err := ClientAPINotifier{}.Send(MatrixConfig{Homeserver: srv.URL, AccessToken: "wrong", RoomID: "!r:x", Message: "hi"})
	if err == nil || !strings.Contains(err.Error(), "M_UNKNOWN_TOKEN") {
		t.Fatalf("expected M_UNKNOWN_TOKEN error, got %v", err)
	}
}
