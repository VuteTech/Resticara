package telegramsender

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type TelegramConfig struct {
	BotToken string
	ChatID   int64
	Message  string
}

type TelegramSender interface {
	Send(cfg TelegramConfig) error
}

type BotAPISender struct{}

func (s BotAPISender) Send(cfg TelegramConfig) error {
	bot, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		return fmt.Errorf("failed to create bot: %w", err)
	}
	msg := tgbotapi.NewMessage(cfg.ChatID, cfg.Message)
	if _, err := bot.Send(msg); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	return nil
}
