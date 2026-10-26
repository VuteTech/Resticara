package telegram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestSend(t *testing.T) {
	var got sendMessageRequest
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	n := BotAPINotifier{APIEndpoint: srv.URL}
	if err := n.Send(TelegramConfig{BotToken: "123:abc", ChatID: 42, Message: "<b>ok</b>"}); err != nil {
		t.Fatal(err)
	}
	if path != "/bot123:abc/sendMessage" || got.ChatID != 42 || got.ParseMode != "HTML" || got.Text != "<b>ok</b>" {
		t.Errorf("unexpected request %s %+v", path, got)
	}

	// a report over the limit is sent as truncated plain text
	got = sendMessageRequest{}
	long := "<b>Backup failed</b>\n<pre>" + strings.Repeat("😀 output line\n", 1000) + "</pre>"
	if err := n.Send(TelegramConfig{BotToken: "123:abc", ChatID: 42, Message: long}); err != nil {
		t.Fatal(err)
	}
	if got.ParseMode != "" || !strings.HasPrefix(got.Text, "Backup failed") || !strings.HasSuffix(got.Text, truncatedSuffix) {
		t.Errorf("unexpected long message: parse_mode=%q text=%.40q...", got.ParseMode, got.Text)
	}
	if l := len(utf16.Encode([]rune(got.Text))); l > maxMessageLength {
		t.Errorf("message length %d exceeds %d", l, maxMessageLength)
	}
}

func TestSendErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer srv.Close()

	err := BotAPINotifier{APIEndpoint: srv.URL}.Send(TelegramConfig{BotToken: "123:secret", ChatID: 1, Message: "x"})
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("expected API error, got %v", err)
	}

	srv.Close()
	err = BotAPINotifier{APIEndpoint: srv.URL}.Send(TelegramConfig{BotToken: "123:secret", ChatID: 1, Message: "x"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("expected transport error without the token, got %v", err)
	}
}
