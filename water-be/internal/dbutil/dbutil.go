package dbutil

import "time"

func BoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func WithDefault(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func WithDefaultInt(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func FormatTime(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

func ParseTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}
