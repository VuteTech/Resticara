package emailsender

import (
	"fmt"
	"net/smtp"
)

type EmailConfig struct {
	From       string
	Username   string
	Password   string
	To         string
	SmtpServer string
	SmtpPort   string
	Subject    string
	Body       string
}

type EmailSender interface {
	Send(emailConfig EmailConfig) error
}

type SmtpEmailSender struct{}

func (s SmtpEmailSender) Send(emailConfig EmailConfig) error {
	message := "From: " + emailConfig.From + "\n" +
		"To: " + emailConfig.To + "\n" +
		"Subject: " + emailConfig.Subject + "\n\n" +
		emailConfig.Body

	err := smtp.SendMail(emailConfig.SmtpServer+":"+emailConfig.SmtpPort,
		smtp.PlainAuth("", emailConfig.Username, emailConfig.Password, emailConfig.SmtpServer),
		emailConfig.From, []string{emailConfig.To}, []byte(message))

	if err != nil {
		return fmt.Errorf("Error sending email: %v", err)
	}

	return nil
}
