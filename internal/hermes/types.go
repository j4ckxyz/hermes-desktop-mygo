// Package hermes is a client for a Hermes Agent instance: the dashboard
// REST API under /api/* and the JSON-RPC 2.0 WebSocket at /api/ws, as
// documented in docs/hermes-api.md. It does no UI work and never blocks
// on the caller's thread beyond the call it is asked to make.
package hermes

import (
	"encoding/json"
	"math"
	"strings"
	"time"
)

// Health is GET /api/health (docs §3).
type Health struct {
	OK             bool   `json:"ok"`
	Version        string `json:"version"`
	DisplayVersion string `json:"displayVersion"`
	Commit         string `json:"commit"`
	AuthRequired   bool   `json:"auth_required"`
}

// Status is the public part of GET /api/status (docs §2.1).
type Status struct {
	Version       string   `json:"version"`
	AuthRequired  bool     `json:"auth_required"`
	AuthProviders []string `json:"auth_providers"`
	AuthFlows     []string `json:"auth_flows"`
	Overall       string   `json:"overall"`
}

// AuthProvider is a row of GET /api/auth/providers.
type AuthProvider struct {
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	SupportsPassword bool   `json:"supports_password"`
}

// Me is GET /api/auth/me.
type Me struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Provider    string `json:"provider"`
	ExpiresAt   *Time  `json:"expires_at"`
}

// Profile is an agent: a row of GET /api/profiles (docs §4).
type Profile struct {
	Name        string          `json:"name"`
	DisplayName string          `json:"display_name"`
	IsDefault   bool            `json:"is_default"`
	Model       string          `json:"model"`
	Provider    string          `json:"provider"`
	Description string          `json:"description"`
	HasAvatar   bool            `json:"has_avatar"`
	UIMeta      json.RawMessage `json:"ui_meta,omitempty"`
}

// Title is the name to show for the agent.
func (p Profile) Title() string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	if p.IsDefault && p.Name == "default" {
		return "Hermes" // the default profile is the agent itself
	}
	return p.Name
}

// Color returns the colour Hermes's own bot roster stored for the agent
// (ui_meta["hermes-bots"].color), or "" (docs §4.1: plugin-owned, optional).
func (p Profile) Color() string {
	if len(p.UIMeta) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(p.UIMeta, &m) != nil {
		return ""
	}
	var bots struct {
		Color string `json:"color"`
	}
	if json.Unmarshal(m["hermes-bots"], &bots) != nil {
		return ""
	}
	return bots.Color
}

// Session is a row of GET /api/sessions or /api/profiles/sessions (docs §5.1).
type Session struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	Source          string  `json:"source"`
	Model           string  `json:"model"`
	StartedAt       Time    `json:"started_at"`
	EndedAt         *Time   `json:"ended_at"`
	LastActive      Time    `json:"last_active"`
	MessageCount    int     `json:"message_count"`
	ToolCallCount   int     `json:"tool_call_count"`
	Pinned          bool    `json:"pinned"`
	Archived        bool    `json:"archived"`
	Unread          bool    `json:"unread"`
	Preview         string  `json:"preview"`
	IsActive        bool    `json:"is_active"`
	Profile         string  `json:"profile"`
	ParentSessionID *string `json:"parent_session_id"`
}

// DisplayTitle is the title, the preview, or "Untitled".
func (s Session) DisplayTitle() string {
	if t := strings.TrimSpace(s.Title); t != "" {
		return t
	}
	if p := strings.TrimSpace(s.Preview); p != "" {
		return firstLine(p, 80)
	}
	return "Untitled"
}

// Activity is when the session was last active, or when it started.
func (s Session) Activity() time.Time {
	if !s.LastActive.IsZero() {
		return s.LastActive.Time
	}
	return s.StartedAt.Time
}

// SessionPage is a page of sessions.
type SessionPage struct {
	Sessions []Session `json:"sessions"`
	Total    int       `json:"total"`
	Limit    int       `json:"limit"`
	Offset   int       `json:"offset"`
}

// ToolCall is an OpenAI-style tool call on an assistant row.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Message is a stored transcript row (docs §5.2, _WIRE_ROW_KEYS).
type Message struct {
	ID           int64           `json:"id"`
	SessionID    string          `json:"session_id"`
	Role         string          `json:"role"` // user, assistant, tool, system
	Content      json.RawMessage `json:"content"`
	ToolCallID   string          `json:"tool_call_id,omitempty"`
	ToolCalls    []ToolCall      `json:"tool_calls,omitempty"`
	ToolName     string          `json:"tool_name,omitempty"`
	Timestamp    Time            `json:"timestamp"`
	FinishReason string          `json:"finish_reason,omitempty"`
	Reasoning    string          `json:"reasoning,omitempty"`
	DisplayKind  string          `json:"display_kind,omitempty"`
	Compacted    Flag            `json:"compacted,omitempty"`
	Summary      Flag            `json:"_compressed_summary,omitempty"`
}

// Text flattens the content: a string, or the text parts of a list of
// content parts (images become "[image]").
func (m Message) Text() string { return ContentText(m.Content) }

// ContentText flattens a message content value.
func ContentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			switch {
			case p.Text != "":
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(p.Text)
			case strings.Contains(p.Type, "image"):
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString("[image]")
			}
		}
		return b.String()
	}
	return string(raw)
}

// MessagePage is GET /api/sessions/{id}/messages.
type MessagePage struct {
	SessionID string    `json:"session_id"`
	Profile   string    `json:"profile"`
	Messages  []Message `json:"messages"`
}

// SearchResult is a row of GET /api/sessions/search (docs §5.5).
type SearchResult struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Snippet   string `json:"snippet"`
	Role      string `json:"role"`
	Profile   string `json:"profile"`
	Preview   string `json:"preview"`
	Last      Time   `json:"last_active"`
}

// Time is a Unix time in (fractional) seconds, as Hermes sends them.
type Time struct{ time.Time }

// UnixTime returns the Time for Unix seconds.
func UnixTime(sec float64) Time {
	if sec == 0 {
		return Time{}
	}
	s, f := math.Modf(sec)
	return Time{time.Unix(int64(s), int64(f*1e9))}
}

// Seconds is the time as Unix seconds.
func (t Time) Seconds() float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixNano()) / 1e9
}

func (t *Time) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" || s == `""` {
		*t = Time{}
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err == nil {
		*t = UnixTime(f)
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02 15:04:05"} {
		if v, err := time.Parse(layout, str); err == nil {
			*t = Time{v}
			return nil
		}
	}
	*t = Time{}
	return nil
}

func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.Seconds())
}

func firstLine(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// Flag is a boolean that also accepts 0/1 and null: Hermes 0.23.3 sends
// some stored columns (compacted) as integers.
type Flag bool

func (f *Flag) UnmarshalJSON(b []byte) error {
	switch strings.TrimSpace(string(b)) {
	case "true", "1":
		*f = true
	case "false", "0", "null", `""`:
		*f = false
	default:
		var n float64
		if err := json.Unmarshal(b, &n); err != nil {
			return err
		}
		*f = n != 0
	}
	return nil
}
