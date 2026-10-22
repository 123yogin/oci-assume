package shell

import (
	"fmt"
	"time"
)

// PromptState classifies remaining session lifetime.
type PromptState string

const (
	StateOK      PromptState = "ok"
	StateWarn    PromptState = "warn"
	StateExpired PromptState = "expired"
)

// Prompt formats the prompt text; a negative warnBelow marks an API-key profile.
func Prompt(profile string, remaining, warnBelow time.Duration) (string, PromptState) {
	if profile == "" {
		return "", ""
	}
	if warnBelow < 0 {
		return profile, StateOK
	}
	st := StateOK
	switch {
	case remaining <= 0:
		st = StateExpired
	case remaining < warnBelow:
		st = StateWarn
	}
	return profile + " " + FormatRemaining(remaining), st
}

// FormatRemaining renders a session lifetime as "52m", "1h05m" or "expired".
func FormatRemaining(d time.Duration) string {
	if d <= 0 {
		return "expired"
	}
	m := int(d / time.Minute)
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh%02dm", m/60, m%60)
}
