package store

import "time"

// TimeLayout is the one timestamp text format: UTC, fixed width, lexically sortable, valid RFC 3339.
const TimeLayout = "2006-01-02T15:04:05.000Z"

// FormatTime formats t (converted to UTC) in TimeLayout.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

// ParseTime parses a string produced by FormatTime.
func ParseTime(s string) (time.Time, error) { return time.Parse(TimeLayout, s) }
