// Package config parses startup environment without I/O or network access.
package config

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	TelegramBotToken string
	DBPath           string
	KeyFile          string
	AllowAllUsers    bool
	AllowedUserIDs   []int64
}

var tokenPattern = regexp.MustCompile(`^[1-9][0-9]*:[A-Za-z0-9_-]{35}$`)

// Load validates syntax only. Lifecycle startup must separately validate the key
// file, database, and stored ciphertext before enabling any network work.
// Errors contain variable names only, never their supplied values.
func Load(lookup func(string) (string, bool)) (Config, error) {
	var c Config
	get := func(name string) string { v, _ := lookup(name); return v }
	c.TelegramBotToken = get("CASE_WATCH_TELEGRAM_BOT_TOKEN")
	if !tokenPattern.MatchString(c.TelegramBotToken) {
		return Config{}, errors.New("CASE_WATCH_TELEGRAM_BOT_TOKEN: invalid or missing token syntax")
	}
	c.DBPath = get("CASE_WATCH_DB_PATH")
	c.KeyFile = get("CASE_WATCH_KEY_FILE")
	for _, field := range []struct{ name, value string }{{"CASE_WATCH_DB_PATH", c.DBPath}, {"CASE_WATCH_KEY_FILE", c.KeyFile}} {
		if strings.TrimSpace(field.value) == "" || strings.ContainsAny(field.value, "\x00\r\n") {
			return Config{}, errors.New(field.name + ": invalid or missing path")
		}
	}
	if raw, present := lookup("CASE_WATCH_ALLOW_ALL_USERS"); present {
		switch strings.TrimSpace(raw) {
		case "true":
			c.AllowAllUsers = true
		case "false":
		default:
			return Config{}, errors.New("CASE_WATCH_ALLOW_ALL_USERS: expected true or false")
		}
	}
	raw := strings.TrimSpace(get("CASE_WATCH_ALLOWED_USER_IDS"))
	seen := map[int64]bool{}
	if raw != "" {
		for _, entry := range strings.Split(raw, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" || strings.IndexFunc(entry, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
				return Config{}, errors.New("CASE_WATCH_ALLOWED_USER_IDS: invalid user ID list")
			}
			id, err := strconv.ParseInt(entry, 10, 64)
			if err != nil || id <= 0 {
				return Config{}, errors.New("CASE_WATCH_ALLOWED_USER_IDS: invalid user ID list")
			}
			if !seen[id] {
				c.AllowedUserIDs = append(c.AllowedUserIDs, id)
				seen[id] = true
			}
		}
	}
	if !c.AllowAllUsers && len(c.AllowedUserIDs) == 0 {
		return Config{}, errors.New("CASE_WATCH_ALLOWED_USER_IDS: restricted mode requires users")
	}
	return c, nil
}
