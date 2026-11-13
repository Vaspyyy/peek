package main

import (
	"fmt"
	"strconv"
	"strings"
)

// count writes n with thousands separators: 12,345.
func count(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

var plurals = map[string]string{"match": "matches", "entry": "entries"}

// plural is "1 line", "12,345 lines".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	p, ok := plurals[word]
	if !ok {
		p = word + "s"
	}
	return count(n) + " " + p
}

// less is a budget with n lines taken, at least 1; 0 (no limit) stays 0.
func less(budget, n int) int {
	if budget <= 0 {
		return 0
	}
	return max(budget-n, 1)
}

// sizeText is a byte count for people: 512 B, 48 KB, 1.2 MB.
func sizeText(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
}

// shellQuote quotes s for a shell when it needs it.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:@%+,=") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
