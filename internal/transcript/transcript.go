// Package transcript turns a session's stored messages and its live
// socket events into Turns: what the chat view draws. A Turn is one user
// message and everything the agent did about it: tool calls, commentary,
// reasoning and the reply. It is pure data, with no UI and no I/O, so the
// streaming logic is tested against recorded event fixtures.
package transcript

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"hermes-desktop/internal/hermes"
)

// ToolStatus is where a tool call is.
type ToolStatus int

const (
	ToolGenerating ToolStatus = iota // the model is writing its arguments
	ToolRunning
	ToolDone
	ToolFailed
)

// Tool is one tool call.
type Tool struct {
	ID       string
	Name     string
	Args     json.RawMessage // the arguments object, or nil
	ArgsText string          // arguments as the model wrote them, when not JSON
	Context  string          // the server's short preview of the call
	Status   ToolStatus
	Progress string
	Result   string
	Duration time.Duration
	Started  time.Time
}

// TurnStatus is how a turn ended.
type TurnStatus string

const (
	Running     TurnStatus = "running"
	Complete    TurnStatus = "complete"
	Errored     TurnStatus = "error"
	Interrupted TurnStatus = "interrupted"
)

// Turn is a user message and the agent's work on it.
type Turn struct {
	ID       string // stable key: the user row id, or the first agent row's
	User     string // "" for a turn the agent started (cron, a resumed run)
	UserTime time.Time
	Tools    []*Tool
	// Interim is the commentary the agent wrote beside its tool calls.
	Interim   []string
	Reasoning string
	Text      string // the reply
	Status    TurnStatus
	Error     string
	Thinking  string // spinner text while running ("" when none)
	Started   time.Time
	Ended     time.Time
	Prompt    *hermes.Prompt // a pending approval or clarify card
	Usage     *Usage
	// Version changes whenever the turn does, so views can cache by it.
	Version int
}

// Usage is the token count of a finished turn.
type Usage struct {
	Model  string `json:"model"`
	Input  int    `json:"input"`
	Output int    `json:"output"`
	Total  int    `json:"total"`
}

// Running reports whether the agent is still working on the turn.
func (t *Turn) Running() bool { return t.Status == Running }

// Counts returns how many of the turn's tool calls ran commands and how
// many were other tools, for "Ran 11 commands and made 7 tool calls".
func (t *Turn) Counts() (commands, tools int) {
	for _, tl := range t.Tools {
		if IsCommand(tl.Name) {
			commands++
		} else {
			tools++
		}
	}
	return
}

// Summary is the label of a finished turn's collapsed tool group.
func (t *Turn) Summary() string {
	cmds, tools := t.Counts()
	plural := func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	}
	switch {
	case cmds > 0 && tools > 0:
		return fmt.Sprintf("Ran %d %s and made %d tool %s", cmds, plural(cmds, "command", "commands"), tools, plural(tools, "call", "calls"))
	case cmds > 0:
		return fmt.Sprintf("Ran %d %s", cmds, plural(cmds, "command", "commands"))
	case tools > 0:
		return fmt.Sprintf("Made %d tool %s", tools, plural(tools, "call", "calls"))
	}
	return ""
}

// IsCommand reports whether a tool runs shell commands.
func IsCommand(name string) bool {
	switch strings.ToLower(name) {
	case "terminal", "shell", "bash", "run_command", "execute_command", "exec", "shell.exec", "process":
		return true
	}
	return false
}

// FromMessages folds stored rows (oldest first) into turns.
func FromMessages(msgs []hermes.Message) []*Turn {
	var turns []*Turn
	var cur *Turn
	byCall := map[string]*Tool{}
	var lastAssistant []string // assistant texts of the current turn, in order
	flush := func() {
		if cur == nil {
			return
		}
		// The last assistant text is the reply; earlier ones were
		// commentary beside tool calls.
		if n := len(lastAssistant); n > 0 {
			cur.Text = lastAssistant[n-1]
			cur.Interim = append(cur.Interim, lastAssistant[:n-1]...)
		}
		lastAssistant = nil
	}
	start := func(id string, at time.Time) {
		flush()
		cur = &Turn{ID: id, Status: Complete, Started: at, Ended: at}
		turns = append(turns, cur)
	}
	for _, m := range msgs {
		if m.Compacted {
			continue
		}
		at := m.Timestamp.Time
		switch m.Role {
		case "user":
			text := m.Text()
			if strings.HasPrefix(text, "[System:") && m.DisplayKind == "" {
				continue // scaffold row the display projection hides
			}
			start(fmt.Sprintf("u%d", m.ID), at)
			cur.User, cur.UserTime = text, at
		case "assistant":
			if cur == nil {
				start(fmt.Sprintf("a%d", m.ID), at)
			}
			cur.Ended = at
			if m.Reasoning != "" {
				cur.Reasoning = joinNonEmpty(cur.Reasoning, m.Reasoning)
			}
			if text := strings.TrimSpace(m.Text()); text != "" {
				lastAssistant = append(lastAssistant, text)
			}
			for _, tc := range m.ToolCalls {
				tl := &Tool{ID: tc.ID, Name: tc.Function.Name, Status: ToolDone, Started: at}
				setArgs(tl, tc.Function.Arguments)
				cur.Tools = append(cur.Tools, tl)
				byCall[tc.ID] = tl
			}
		case "tool":
			if cur == nil {
				start(fmt.Sprintf("t%d", m.ID), at)
			}
			cur.Ended = at
			if tl := byCall[m.ToolCallID]; tl != nil {
				tl.Result = m.Text()
				if !tl.Started.IsZero() && at.After(tl.Started) {
					tl.Duration = at.Sub(tl.Started)
				}
			} else if m.ToolName != "" {
				cur.Tools = append(cur.Tools, &Tool{ID: m.ToolCallID, Name: m.ToolName, Status: ToolDone, Result: m.Text()})
			}
		}
	}
	flush()
	return turns
}

func setArgs(tl *Tool, s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	if json.Valid([]byte(s)) {
		tl.Args = json.RawMessage(s)
	} else {
		tl.ArgsText = s
	}
}

func joinNonEmpty(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n\n" + b
}

// Live folds a session's socket events into its turns as they arrive.
type Live struct {
	Turns []*Turn
	// LastSeq is the highest event sequence applied; replays and duplicate
	// deliveries at or below it are ignored.
	LastSeq int64
	// Generation is the execution generation of the running turn, which
	// interrupting it needs (canonical wire).
	Generation int64
	segment    strings.Builder
}

// Current returns the running turn, or nil.
func (l *Live) Current() *Turn {
	if n := len(l.Turns); n > 0 && l.Turns[n-1].Running() {
		return l.Turns[n-1]
	}
	return nil
}

// Running reports whether the agent is working.
func (l *Live) Running() bool { return l.Current() != nil }

// AddUser appends the user's message as a new running turn, before the
// server has echoed anything, so the bubble shows at once.
func (l *Live) AddUser(id, text string, at time.Time) *Turn {
	l.finishSegment()
	t := &Turn{ID: id, User: text, UserTime: at, Status: Running, Started: at}
	l.Turns = append(l.Turns, t)
	return t
}

// ensure returns the running turn, starting one when the agent began a
// turn this client did not submit.
func (l *Live) ensure(at time.Time) *Turn {
	if t := l.Current(); t != nil {
		return t
	}
	t := &Turn{ID: fmt.Sprintf("live%d", len(l.Turns)), Status: Running, Started: at}
	l.Turns = append(l.Turns, t)
	return t
}

// finishSegment ends the reply text being streamed, turning it into
// commentary (it came before a tool call).
func (l *Live) finishSegment() {
	t := l.Current()
	if t == nil {
		l.segment.Reset()
		return
	}
	if s := strings.TrimSpace(t.Text); s != "" {
		t.Interim = append(t.Interim, s)
	}
	t.Text = ""
	l.segment.Reset()
}

// Apply folds one event in, reporting whether anything visible changed.
// now stamps the times the events themselves do not carry.
func (l *Live) Apply(ev hermes.Event, now time.Time) bool {
	if ev.Seq > 0 {
		if ev.Seq <= l.LastSeq {
			return false
		}
		l.LastSeq = ev.Seq
	}
	if ev.ExecutionGeneration > 0 {
		l.Generation = ev.ExecutionGeneration
	}
	switch ev.Type {
	case "message.start":
		t := l.ensure(now)
		t.Thinking = ""
		t.Version++
		return true

	case "message.delta":
		var p struct {
			Text *string `json:"text"`
		}
		ev.Decode(&p)
		t := l.ensure(now)
		if p.Text == nil { // a segment boundary
			l.finishSegment()
		} else {
			l.segment.WriteString(*p.Text)
			t.Text = l.segment.String()
			t.Thinking = ""
		}
		t.Version++
		return true

	case "message.interim":
		var p struct {
			Text            string `json:"text"`
			AlreadyStreamed bool   `json:"already_streamed"`
		}
		ev.Decode(&p)
		if p.AlreadyStreamed || strings.TrimSpace(p.Text) == "" {
			return false
		}
		t := l.ensure(now)
		t.Interim = append(t.Interim, strings.TrimSpace(p.Text))
		t.Version++
		return true

	case "reasoning.delta", "reasoning.available":
		var p struct {
			Text string `json:"text"`
		}
		ev.Decode(&p)
		t := l.ensure(now)
		if ev.Type == "reasoning.available" {
			t.Reasoning = joinNonEmpty(t.Reasoning, p.Text)
		} else {
			t.Reasoning += p.Text
		}
		t.Version++
		return true

	case "thinking.delta":
		var p struct {
			Text string `json:"text"`
		}
		ev.Decode(&p)
		t := l.ensure(now)
		t.Thinking = p.Text
		t.Version++
		return true

	case "tool.generating":
		var p struct {
			Name string `json:"name"`
		}
		ev.Decode(&p)
		t := l.ensure(now)
		l.finishSegment()
		for _, tl := range t.Tools {
			if tl.Status == ToolGenerating && tl.Name == p.Name {
				return false
			}
		}
		t.Tools = append(t.Tools, &Tool{Name: p.Name, Status: ToolGenerating, Started: now})
		t.Version++
		return true

	case "tool.start":
		var p struct {
			ToolID  string          `json:"tool_id"`
			Name    string          `json:"name"`
			Context string          `json:"context"`
			Args    json.RawMessage `json:"args"`
			ArgsTxt string          `json:"args_text"`
		}
		ev.Decode(&p)
		t := l.ensure(now)
		l.finishSegment()
		tl := findTool(t, p.ToolID)
		if tl == nil {
			for _, g := range t.Tools {
				if g.Status == ToolGenerating && g.Name == p.Name && g.ID == "" {
					tl = g
					break
				}
			}
		}
		if tl == nil {
			tl = &Tool{Started: now}
			t.Tools = append(t.Tools, tl)
		}
		tl.ID, tl.Name, tl.Context, tl.Status = p.ToolID, p.Name, p.Context, ToolRunning
		if len(p.Args) > 0 && string(p.Args) != "null" {
			tl.Args = p.Args
		} else if p.ArgsTxt != "" {
			setArgs(tl, p.ArgsTxt)
		}
		t.Version++
		return true

	case "tool.progress":
		var p struct {
			ToolID  string   `json:"tool_id"`
			Message string   `json:"message"`
			Percent *float64 `json:"percent"`
		}
		ev.Decode(&p)
		t := l.ensure(now)
		tl := findTool(t, p.ToolID)
		if tl == nil {
			return false
		}
		switch {
		case p.Message != "":
			tl.Progress = p.Message
		case p.Percent != nil:
			tl.Progress = fmt.Sprintf("%.0f%%", *p.Percent)
		}
		t.Version++
		return true

	case "tool.complete":
		var p struct {
			ToolID     string          `json:"tool_id"`
			Name       string          `json:"name"`
			Args       json.RawMessage `json:"args"`
			DurationS  float64         `json:"duration_s"`
			Result     json.RawMessage `json:"result"`
			ResultText string          `json:"result_text"`
			Summary    string          `json:"summary"`
			IsError    bool            `json:"is_error"`
		}
		ev.Decode(&p)
		t := l.ensure(now)
		tl := findTool(t, p.ToolID)
		if tl == nil {
			tl = &Tool{ID: p.ToolID, Name: p.Name, Started: now}
			t.Tools = append(t.Tools, tl)
		}
		if tl.Args == nil && len(p.Args) > 0 && string(p.Args) != "null" {
			tl.Args = p.Args
		}
		tl.Status = ToolDone
		if p.IsError {
			tl.Status = ToolFailed
		}
		tl.Progress = ""
		switch {
		case p.ResultText != "":
			tl.Result = p.ResultText
		case len(p.Result) > 0:
			tl.Result = hermes.ContentText(p.Result)
		default:
			tl.Result = p.Summary
		}
		if p.DurationS > 0 {
			tl.Duration = time.Duration(p.DurationS * float64(time.Second))
		} else if !tl.Started.IsZero() {
			tl.Duration = now.Sub(tl.Started)
		}
		t.Version++
		return true

	case "message.complete":
		var p struct {
			Text          string `json:"text"`
			Content       string `json:"content"`
			Status        string `json:"status"`
			Outcome       string `json:"outcome"`
			Error         any    `json:"error"`
			FailureReason string `json:"failure_reason"`
			Reasoning     string `json:"reasoning"`
			Usage         *Usage `json:"usage"`
		}
		ev.Decode(&p)
		t := l.Current()
		if t == nil {
			return false // a replayed completion of a turn already settled
		}
		if text := firstNonEmpty(p.Text, p.Content); text != "" {
			t.Text = text
		}
		if t.Reasoning == "" && p.Reasoning != "" {
			t.Reasoning = p.Reasoning
		}
		switch {
		case p.Status == "interrupted" || p.Outcome == "cancelled":
			t.Status = Interrupted
		case p.Status == "error":
			t.Status = Errored
			t.Error = firstNonEmpty(errorText(p.Error), p.FailureReason, "The agent stopped with an error.")
		default:
			t.Status = Complete
		}
		for _, tl := range t.Tools { // a stop leaves calls unsettled
			if tl.Status == ToolRunning || tl.Status == ToolGenerating {
				tl.Status = ToolFailed
			}
		}
		t.Usage = p.Usage
		t.Thinking, t.Prompt, t.Ended = "", nil, now
		l.segment.Reset()
		t.Version++
		return true

	case "error":
		var p struct {
			Message string `json:"message"`
		}
		ev.Decode(&p)
		t := l.Current()
		if t == nil {
			return false
		}
		t.Error = p.Message
		t.Version++
		return true

	case "approval.request", "clarify.request":
		var pr hermes.Prompt
		if ev.Decode(&pr) != nil {
			return false
		}
		if pr.Kind == "" {
			pr.Kind = strings.TrimSuffix(ev.Type, ".request")
		}
		t := l.ensure(now)
		t.Prompt = &pr
		t.Version++
		return true

	case "request.cancel": // the server withdrew a request (timeout, interrupt…)
		var p struct {
			ID string `json:"id"`
		}
		ev.Decode(&p)
		t := l.Current()
		if t == nil || t.Prompt == nil || t.Prompt.RequestID == "" || t.Prompt.RequestID != p.ID {
			return false
		}
		t.Prompt = nil
		t.Version++
		return true

	case "approval.settled", "clarify.settled", "approval.cancelled":
		t := l.Current()
		if t == nil || t.Prompt == nil {
			return false
		}
		var p struct {
			PromptID string `json:"prompt_id"`
		}
		ev.Decode(&p)
		if p.PromptID != "" && p.PromptID != t.Prompt.PromptID {
			return false
		}
		t.Prompt = nil
		t.Version++
		return true
	}
	return false
}

// SetPrompt shows a prompt that arrived as a server request (legacy).
func (l *Live) SetPrompt(p hermes.Prompt, now time.Time) {
	t := l.ensure(now)
	t.Prompt = &p
	t.Version++
}

func findTool(t *Turn, id string) *Tool {
	if id == "" {
		return nil
	}
	for _, tl := range t.Tools {
		if tl.ID == id {
			return tl
		}
	}
	return nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func errorText(v any) string {
	switch e := v.(type) {
	case string:
		return e
	case map[string]any:
		if m, ok := e["message"].(string); ok {
			return m
		}
	}
	return ""
}

// Merge lays the live turns over a stored transcript: the turns this
// client watched stream (running, or finished but not yet in a fresh read
// of the store) follow the stored ones, each replacing a stored copy of
// the same message.
func Merge(stored []*Turn, live *Live) []*Turn {
	if len(live.Turns) == 0 {
		return stored
	}
	out := append([]*Turn(nil), stored...)
	for _, lt := range live.Turns {
		if n := len(out); n > 0 {
			last := out[n-1]
			switch {
			case lt.User != "" && last.User == lt.User:
				out = out[:n-1] // the store has persisted this message already
			case lt.User == "" && last.User != "" && last.Text == "" && len(last.Tools) == 0:
				// A turn this client did not submit (another device, or one
				// running when the app opened) answers the last stored message.
				lt.User, lt.UserTime, lt.ID = last.User, last.UserTime, last.ID
				out = out[:n-1]
			}
		}
		out = append(out, lt)
	}
	return out
}

// Prune forgets the finished live turns that ended before a read of the
// store began: that read has them, so the stored copy takes over.
func (l *Live) Prune(readStarted time.Time) {
	kept := l.Turns[:0]
	for _, t := range l.Turns {
		if t.Running() || t.Ended.After(readStarted) || t.Ended.IsZero() {
			kept = append(kept, t)
		}
	}
	clear(l.Turns[len(kept):])
	l.Turns = kept
}

// SortTools orders a finished turn's tools by start time (stable).
func SortTools(t *Turn) {
	sort.SliceStable(t.Tools, func(i, j int) bool { return t.Tools[i].Started.Before(t.Tools[j].Started) })
}
