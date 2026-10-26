package email

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type EmailConfig struct {
	From       string
	Username   string
	Password   string
	To         []string
	SmtpServer string
	SmtpPort   string
	Subject    string
	Body       string
}

type EmailNotifier interface {
	Send(emailConfig EmailConfig) error
}

type SmtpEmailNotifier struct{}

func (s SmtpEmailNotifier) Send(emailConfig EmailConfig) error {
	message, err := buildMessage(emailConfig, time.Now())
	if err != nil {
		return fmt.Errorf("building the message: %w", err)
	}

	addr := net.JoinHostPort(emailConfig.SmtpServer, emailConfig.SmtpPort)
	auth := smtp.PlainAuth("", emailConfig.Username, emailConfig.Password, emailConfig.SmtpServer)

	// port 465 uses implicit TLS (SMTPS); other ports use STARTTLS when the server offers it
	if emailConfig.SmtpPort == "465" {
		err = sendMailTLS(addr, emailConfig.SmtpServer, auth, emailConfig.From, emailConfig.To, message)
	} else {
		err = smtp.SendMail(addr, auth, emailConfig.From, emailConfig.To, message)
	}
	if err != nil {
		return err
	}

	return nil
}

func buildMessage(emailConfig EmailConfig, now time.Time) ([]byte, error) {
	domain := "localhost"
	if at := strings.LastIndex(emailConfig.From, "@"); at >= 0 {
		domain = emailConfig.From[at+1:]
	}

	var msg bytes.Buffer
	headers := []string{
		"From: " + emailConfig.From,
		"To: " + strings.Join(emailConfig.To, ", "),
		"Subject: " + mime.QEncoding.Encode("utf-8", emailConfig.Subject),
		"Date: " + now.Format(time.RFC1123Z),
		fmt.Sprintf("Message-ID: <%d.resticara@%s>", now.UnixNano(), domain),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable",
	}
	msg.WriteString(strings.Join(headers, "\r\n") + "\r\n\r\n")

	body := quotedprintable.NewWriter(&msg)
	if _, err := body.Write([]byte(emailConfig.Body)); err != nil {
		return nil, err
	}
	if err := body.Close(); err != nil {
		return nil, err
	}
	return msg.Bytes(), nil
}

func sendMailTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 30 * time.Second}, "tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()

	if err := c.Auth(auth); err != nil {
		return err
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
