package utils

import "strings"

func IsEmpty(s string) bool { return s == "" }
func IsBlank(s string) bool { return IsEmpty(strings.TrimSpace(s)) }
