package notify

import (
	"errors"
	"strings"
	"testing"

	"resticara/internal/config"
	"resticara/internal/report"
	"resticara/notifiers/email"
	"resticara/notifiers/matrix"
	"resticara/notifiers/telegram"
)

type fakeEmail struct {
	sent []email.EmailConfig
	err  error
}

func (f *fakeEmail) Send(cfg email.EmailConfig) error {
	f.sent = append(f.sent, cfg)
	return f.err
}

type fakeMatrix struct {
	sent []matrix.MatrixConfig
	err  error
}

func (f *fakeMatrix) Send(cfg matrix.MatrixConfig) error {
	f.sent = append(f.sent, cfg)
	return f.err
}

type fakeTelegram struct {
	sent []telegram.TelegramConfig
	err  error
}

func (f *fakeTelegram) Send(cfg telegram.TelegramConfig) error {
	f.sent = append(f.sent, cfg)
	return f.err
}

var messages = Messages{Mail: "mail body", Matrix: "<b>matrix</b>", Telegram: "<b>telegram</b>"}

func TestSendAllChannels(t *testing.T) {
	cfg := config.Config{
		SMTP:     config.SMTP{Enabled: true, From: "bot@example.com", To: "admin@example.com", Server: "mail.example.com", Port: "587", Username: "bot", Pass: "pw"},
		Matrix:   config.Matrix{Enabled: true, Server: "https://matrix.example.com", RoomID: "!r:x", AccessToken: "tok"},
		Telegram: config.Telegram{Enabled: true, BotToken: "123:abc", ChatID: 42},
	}
	mail, mx, tg := &fakeEmail{}, &fakeMatrix{}, &fakeTelegram{}
	var out strings.Builder

	ok := Notifiers{Email: mail, Matrix: mx, Telegram: tg}.Send(cfg, report.Report{StatusMessage: report.StatusSuccess}, messages, &out)
	if !ok {
		t.Errorf("Send returned false")
	}
	if out.String() != "Email sent!\nMatrix message sent!\nTelegram message sent!\n" {
		t.Errorf("output = %q", out.String())
	}

	if len(mail.sent) != 1 || mail.sent[0].Body != "mail body" || mail.sent[0].SmtpServer != "mail.example.com" || mail.sent[0].Password != "pw" ||
		!strings.HasPrefix(mail.sent[0].Subject, report.StatusSuccess+"---") {
		t.Errorf("email = %+v", mail.sent)
	}
	if len(mx.sent) != 1 || mx.sent[0].Message != "<b>matrix</b>" || mx.sent[0].AccessToken != "tok" || mx.sent[0].RoomID != "!r:x" {
		t.Errorf("matrix = %+v", mx.sent)
	}
	if len(tg.sent) != 1 || tg.sent[0] != (telegram.TelegramConfig{BotToken: "123:abc", ChatID: 42, Message: "<b>telegram</b>"}) {
		t.Errorf("telegram = %+v", tg.sent)
	}
}

func TestSendDisabledAndFailing(t *testing.T) {
	mail, mx, tg := &fakeEmail{}, &fakeMatrix{err: errors.New("failed to send message: M_FORBIDDEN")}, &fakeTelegram{}
	n := Notifiers{Email: mail, Matrix: mx, Telegram: tg}

	var out strings.Builder
	if !n.Send(config.Config{}, report.Report{}, messages, &out) {
		t.Error("Send with all channels disabled returned false")
	}
	if out.String() != "SMTP is disabled, not sending email.\nMatrix is disabled, not sending message.\nTelegram is disabled, not sending message.\n" {
		t.Errorf("output = %q", out.String())
	}

	out.Reset()
	cfg := config.Config{Matrix: config.Matrix{Enabled: true}, Telegram: config.Telegram{Enabled: true}}
	if n.Send(cfg, report.Report{}, messages, &out) {
		t.Error("Send with a failing channel returned true")
	}
	// a failing channel does not stop the others
	if !strings.Contains(out.String(), "M_FORBIDDEN") || !strings.Contains(out.String(), "Telegram message sent!") || len(mail.sent) != 0 {
		t.Errorf("output = %q, emails = %d", out.String(), len(mail.sent))
	}
}
