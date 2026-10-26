package telegram

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
	"unicode/utf16"

	"resticara/notifiers/htmltext"
)

// Telegram limits a message to 4096 characters (UTF-16 code units) after entities parsing.
const maxMessageLength = 4096

const truncatedSuffix = "\n… (truncated)"

type TelegramConfig struct {
	BotToken string
	ChatID   int64
	Message  string
}

type TelegramNotifier interface {
	Send(cfg TelegramConfig) error
}

// BotAPINotifier sends messages through the Telegram Bot API.
type BotAPINotifier struct {
	APIEndpoint string // defaults to https://api.telegram.org
	HTTPClient  *http.Client
}

type sendMessageRequest struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

func (s BotAPINotifier) Send(cfg TelegramConfig) error {
	endpoint := s.APIEndpoint
	if endpoint == "" {
		endpoint = "https://api.telegram.org"
	}
	httpClient := s.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	msg := sendMessageRequest{ChatID: cfg.ChatID, Text: cfg.Message, ParseMode: "HTML"}
	// HTML can't be cut safely, so a report over the limit is sent as truncated plain text
	if plain := htmltext.PlainText(cfg.Message); len(utf16.Encode([]rune(plain))) > maxMessageLength {
		msg = sendMessageRequest{ChatID: cfg.ChatID, Text: truncate(plain, maxMessageLength-len(utf16.Encode([]rune(truncatedSuffix)))) + truncatedSuffix}
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	resp, err := httpClient.Post(endpoint+"/bot"+cfg.BotToken+"/sendMessage", "application/json", bytes.NewReader(payload))
	if err != nil {
		// the request URL contains the bot token, keep it out of the error
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("failed to send message: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to send message: HTTP %d", resp.StatusCode)
	}
	if !result.OK {
		return fmt.Errorf("failed to send message: %s", result.Description)
	}
	return nil
}

// truncate shortens s to at most limit UTF-16 code units without splitting a character.
func truncate(s string, limit int) string {
	n := 0
	for i, r := range s {
		n += len(utf16.Encode([]rune{r}))
		if n > limit {
			return s[:i]
		}
	}
	return s
}
