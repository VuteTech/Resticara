// Package notify sends a report through the enabled notification channels.
package notify

import (
	"fmt"
	"io"

	"resticara/internal/config"
	"resticara/internal/report"
	"resticara/notifiers/email"
	"resticara/notifiers/matrix"
	"resticara/notifiers/telegram"
)

// Messages are the rendered reports per channel.
type Messages struct {
	Mail     string
	Matrix   string
	Telegram string
}

type Notifiers struct {
	Email    email.EmailNotifier
	Matrix   matrix.MatrixNotifier
	Telegram telegram.TelegramNotifier
}

// Default returns the notifiers that talk to the real services.
func Default() Notifiers {
	return Notifiers{
		Email:    email.SmtpEmailNotifier{},
		Matrix:   matrix.ClientAPINotifier{},
		Telegram: telegram.BotAPINotifier{},
	}
}

// Send delivers the messages through every enabled channel and reports progress to out.
// It returns false when any channel failed.
func (n Notifiers) Send(cfg config.Config, r report.Report, m Messages, out io.Writer) bool {
	ok := true

	if cfg.SMTP.Enabled {
		err := n.Email.Send(email.EmailConfig{
			From:       cfg.SMTP.From,
			Username:   cfg.SMTP.Username,
			Password:   cfg.SMTP.Pass,
			To:         cfg.SMTP.To,
			SmtpServer: cfg.SMTP.Server,
			SmtpPort:   cfg.SMTP.Port,
			Subject:    r.Subject,
			Body:       m.Mail,
		})
		if err != nil {
			fmt.Fprintln(out, err)
			ok = false
		} else {
			fmt.Fprintln(out, "Email sent!")
		}
	} else {
		fmt.Fprintln(out, "SMTP is disabled, not sending email.")
	}

	if cfg.Matrix.Enabled {
		err := n.Matrix.Send(matrix.MatrixConfig{
			Homeserver:  cfg.Matrix.Server,
			Username:    cfg.Matrix.Username,
			Password:    cfg.Matrix.Pass,
			RoomID:      cfg.Matrix.RoomID,
			AccessToken: cfg.Matrix.AccessToken,
			Message:     m.Matrix,
		})
		if err != nil {
			fmt.Fprintln(out, err)
			ok = false
		} else {
			fmt.Fprintln(out, "Matrix message sent!")
		}
	} else {
		fmt.Fprintln(out, "Matrix is disabled, not sending message.")
	}

	if cfg.Telegram.Enabled {
		err := n.Telegram.Send(telegram.TelegramConfig{
			BotToken: cfg.Telegram.BotToken,
			ChatID:   cfg.Telegram.ChatID,
			Message:  m.Telegram,
		})
		if err != nil {
			fmt.Fprintln(out, err)
			ok = false
		} else {
			fmt.Fprintln(out, "Telegram message sent!")
		}
	} else {
		fmt.Fprintln(out, "Telegram is disabled, not sending message.")
	}

	return ok
}
