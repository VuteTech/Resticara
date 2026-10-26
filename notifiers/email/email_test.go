package email

import (
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestBuildMessage(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	body := "Backup report\nfile: снимка.txt\n" + strings.Repeat("x", 200) + "\n"
	raw, err := buildMessage(EmailConfig{From: "bot@example.com", To: []string{"admin@example.com", "ops@example.com"}, Subject: "Backup successful ✅", Body: body}, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ReplaceAll(string(raw), "\r\n", ""), "\n") {
		t.Error("message contains bare LF line endings")
	}

	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if subject != "Backup successful ✅" {
		t.Errorf("subject = %q", subject)
	}
	if d, err := msg.Header.Date(); err != nil || !d.Equal(now) {
		t.Errorf("date = %v, %v", d, err)
	}
	if msg.Header.Get("To") != "admin@example.com, ops@example.com" {
		t.Errorf("To = %q", msg.Header.Get("To"))
	}
	if !strings.HasSuffix(msg.Header.Get("Message-ID"), "@example.com>") || msg.Header.Get("Content-Type") != "text/plain; charset=UTF-8" {
		t.Errorf("unexpected headers: %v", msg.Header)
	}
	decoded, _ := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if strings.ReplaceAll(string(decoded), "\r\n", "\n") != body {
		t.Errorf("body = %q", decoded)
	}
}
