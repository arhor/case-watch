// Package config parses startup environment without I/O or network access.
package config

import (
	"errors"
	"regexp"
	"strings"
)

type Config struct {
	TelegramBotToken string
	DBPath           string
}

var tokenPattern = regexp.MustCompile(`^[1-9][0-9]*:[A-Za-z0-9_-]{35}$`)

// Load validates syntax only. Lifecycle startup separately validates the database.
// Errors contain variable names only, never their supplied values.
func Load(lookup func(string) (string, bool)) (Config, error) {
	var c Config
	get := func(name string) string { v, _ := lookup(name); return v }
	c.TelegramBotToken = get("CASE_WATCH_TELEGRAM_BOT_TOKEN")
	if !tokenPattern.MatchString(c.TelegramBotToken) {
		return Config{}, errors.New("CASE_WATCH_TELEGRAM_BOT_TOKEN: invalid or missing token syntax")
	}
	c.DBPath = get("CASE_WATCH_DB_PATH")
	if strings.TrimSpace(c.DBPath) == "" || strings.ContainsAny(c.DBPath, "\x00\r\n") {
		return Config{}, errors.New("CASE_WATCH_DB_PATH: invalid or missing path")
	}
	return c, nil
}
