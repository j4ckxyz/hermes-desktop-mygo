package hermes

import (
	"context"
	"encoding/json"
)

// Snapshot is what creating or resuming a session returns, folded from the
// canonical CanonicalSnapshot and the legacy LiveSessionSnapshot (docs §6.4).
type Snapshot struct {
	SessionID           string            `json:"session_id"` // runtime id
	StoredSessionID     string            `json:"stored_session_id"`
	Running             bool              `json:"running"`
	MessageCount        int               `json:"message_count"`
	Revision            int64             `json:"revision"`
	ExecutionGeneration int64             `json:"execution_generation"`
	LastSequence        int64             `json:"last_sequence"`
	ReplayEpoch         string            `json:"replay_epoch"`
	SubscriptionID      string            `json:"subscription_id"`
	Prompts             []Prompt          `json:"prompts"`
	OpenRequests        []json.RawMessage `json:"open_requests"`
	// Legacy: the partial turn in flight.
	Inflight *struct {
		Assistant string `json:"assistant"`
		Streaming bool   `json:"streaming"`
		User      string `json:"user"`
	} `json:"inflight,omitempty"`
	Info SessionInfo `json:"info"`
}

// Stored is the stored id, which REST and the app use.
func (s Snapshot) Stored() string {
	if s.StoredSessionID != "" {
		return s.StoredSessionID
	}
	return s.SessionID
}

// SessionInfo is the live settings of a session (SessionLiveInfo).
type SessionInfo struct {
	Model       string `json:"model"`
	Provider    string `json:"provider"`
	Title       string `json:"title"`
	Running     bool   `json:"running"`
	Personality string `json:"personality"`
}

// Prompt is a pending approval or clarify card (CanonicalPendingPrompt,
// docs §7.3). Legacy approvals arrive as server requests and are folded
// into the same shape with RequestID set.
type Prompt struct {
	// Kind is "approval" or "clarify" (cards on either wire), or a server
	// request's: "sudo", "secret" or "questions" (a clarify batch).
	Kind                string   `json:"kind"`
	PromptID            string   `json:"prompt_id"`
	ExecutionGeneration int64    `json:"execution_generation"`
	Choices             []string `json:"choices"`
	Command             string   `json:"command"`
	Description         string   `json:"description"`
	Question            string   `json:"question"`
	MultiSelect         bool     `json:"multi_select"`
	TimeoutS            float64  `json:"timeout_s"`
	// Legacy server request id, answered with Respond.
	RequestID string `json:"-"`
	ToolName  string `json:"tool_name,omitempty"`
	// EnvVar is the variable a secret prompt asks for.
	EnvVar string `json:"env_var,omitempty"`
	// Questions are a clarify batch's (1–5 questions).
	Questions []Question `json:"questions,omitempty"`
}

// Question is one question of a clarify batch.
type Question struct {
	QID         string   `json:"qid"`
	Question    string   `json:"question"`
	Choices     []string `json:"choices"`
	MultiSelect bool     `json:"multi_select"`
}

// PromptFromRequest folds a server request into a prompt card: a legacy
// approval, a sudo password, a secret or a clarify batch (docs §7.3). It
// reports false for requests the app does not show (desktop-only ones).
func PromptFromRequest(r ServerRequest) (Prompt, bool) {
	switch r.Method {
	case "approval":
		return LegacyApproval(r)
	case "sudo":
		var p struct {
			Command string `json:"command"`
		}
		json.Unmarshal(r.Params, &p)
		return Prompt{Kind: "sudo", PromptID: r.ID, RequestID: r.ID, Command: p.Command}, true
	case "secret":
		var p struct {
			EnvVar string `json:"env_var"`
			Prompt string `json:"prompt"`
		}
		if json.Unmarshal(r.Params, &p) != nil || p.EnvVar == "" {
			return Prompt{}, false
		}
		return Prompt{Kind: "secret", PromptID: r.ID, RequestID: r.ID, EnvVar: p.EnvVar, Question: p.Prompt}, true
	case "clarify":
		var p struct {
			Questions []Question `json:"questions"`
		}
		if json.Unmarshal(r.Params, &p) != nil || len(p.Questions) == 0 {
			return Prompt{}, false
		}
		return Prompt{Kind: "questions", PromptID: r.ID, RequestID: r.ID, Questions: p.Questions}, true
	}
	return Prompt{}, false
}

// AnswerQuestions answers a clarify batch: an answer per question id, nil
// for one skipped. A nil map cancels them all.
func (c *Conn) AnswerQuestions(p Prompt, answers map[string]*string) error {
	if answers == nil {
		return c.Respond(p.RequestID, map[string]any{"outcome": "cancelled"})
	}
	return c.Respond(p.RequestID, map[string]any{"answers": answers, "outcome": "submitted"})
}

// CreateSession starts a new chat with an agent.
func (c *Conn) CreateSession(ctx context.Context, profile, title string) (Snapshot, error) {
	params := map[string]any{"source": "gui"}
	if profile != "" {
		params["profile"] = profile
	}
	if title != "" {
		params["title"] = title
	}
	if c.Canonical() {
		params["request_id"] = randomUUID()
		params["client"] = map[string]any{"name": "hermes-desktop-go"}
	} else {
		params["idempotency_key"] = randomUUID()
	}
	var s Snapshot
	if err := c.Call(ctx, "session.create", params, &s); err != nil {
		return s, err
	}
	c.bind(s.SessionID, s.Stored())
	return s, nil
}

// Resume attaches this socket to a stored session, so its live events
// arrive here; the transcript itself is read over REST.
func (c *Conn) Resume(ctx context.Context, profile, stored string) (Snapshot, error) {
	params := map[string]any{"session_id": stored, "omit_messages": true}
	if profile != "" {
		params["profile"] = profile
	}
	if !c.Canonical() {
		params["inline_images"] = false
		params["source"] = "gui"
	}
	var s Snapshot
	if err := c.Call(ctx, "session.resume", params, &s); err != nil {
		return s, err
	}
	if s.StoredSessionID == "" {
		s.StoredSessionID = stored
	}
	c.bind(s.SessionID, s.Stored())
	return s, nil
}

// Admission is the answer to prompt.submit: the reply streams as events.
type Admission struct {
	AdmissionID         string `json:"admission_id"`
	Status              string `json:"status"` // canonical: queued|started|…; legacy: streaming|queued|…
	ExecutionGeneration int64  `json:"execution_generation"`
}

// Submit sends a user message to the session (by stored id).
func (c *Conn) Submit(ctx context.Context, profile, stored, text string, queued bool) (Admission, error) {
	sid := c.RuntimeID(stored)
	if sid == "" {
		sid = stored
	}
	params := map[string]any{"session_id": sid, "text": text}
	if queued {
		params["queued"] = true
	}
	if c.Canonical() {
		params["submission_id"] = randomUUID()
		if profile != "" {
			params["profile"] = profile
		}
	} else {
		params["surface"] = "gui"
	}
	var a Admission
	err := c.Call(ctx, "prompt.submit", params, &a)
	return a, err
}

// Interrupt stops the session's running turn. generation is the
// execution generation from the snapshot or the events (canonical only).
func (c *Conn) Interrupt(ctx context.Context, stored string, generation int64) error {
	sid := c.RuntimeID(stored)
	if sid == "" {
		sid = stored
	}
	params := map[string]any{"session_id": sid}
	if c.Canonical() {
		params["execution_generation"] = generation
	}
	return c.Call(ctx, "session.interrupt", params, nil)
}

// AnswerPrompt answers an approval (choice) or clarify (answer) card.
func (c *Conn) AnswerPrompt(ctx context.Context, stored string, p Prompt, answer string) error {
	sid := c.RuntimeID(stored)
	if sid == "" {
		sid = stored
	}
	if p.RequestID != "" { // a server request
		switch p.Kind {
		case "sudo", "secret": // "" skips
			return c.Respond(p.RequestID, map[string]any{"value": answer})
		case "questions":
			if len(p.Questions) == 1 {
				return c.AnswerQuestions(p, map[string]*string{p.Questions[0].QID: &answer})
			}
		}
		return c.Respond(p.RequestID, map[string]any{"choice": answer})
	}
	if p.Kind == "clarify" {
		return c.Call(ctx, "clarify.respond", map[string]any{
			"session_id": sid, "execution_generation": p.ExecutionGeneration, "prompt_id": p.PromptID, "answer": answer,
		}, nil)
	}
	return c.Call(ctx, "approval.respond", map[string]any{
		"session_id": sid, "execution_generation": p.ExecutionGeneration, "prompt_id": p.PromptID, "choice": answer,
	}, nil)
}

// Replay is session.events.since: the events after a sequence number.
type Replay struct {
	Events           []Event `json:"events"`
	LatestSeq        int64   `json:"latest_seq"`
	Truncated        bool    `json:"truncated"`
	SnapshotRequired bool    `json:"snapshot_required"`
	Epoch            string  `json:"epoch"`
	ReplayEpoch      string  `json:"replay_epoch"`
}

// EventsSince fetches the events of a session after lastSeen, to catch up
// after a reconnect (docs §6.8). When Truncated or SnapshotRequired is
// set, refetch the transcript instead of applying them.
func (c *Conn) EventsSince(ctx context.Context, stored string, lastSeen int64, epoch string) (Replay, error) {
	sid := c.RuntimeID(stored)
	if sid == "" {
		sid = stored
	}
	params := map[string]any{"session_id": sid, "last_seen": lastSeen}
	if epoch != "" {
		params["replay_epoch"] = epoch
	}
	var r Replay
	err := c.Call(ctx, "session.events.since", params, &r)
	for i := range r.Events {
		r.Events[i].StoredID = c.StoredID(r.Events[i].SessionID)
	}
	return r, err
}

// Detach unsubscribes the socket from a session (canonical).
func (c *Conn) Detach(ctx context.Context, stored, subscriptionID string) error {
	if !c.Canonical() || subscriptionID == "" {
		return nil
	}
	sid := c.RuntimeID(stored)
	if sid == "" {
		sid = stored
	}
	return c.Call(ctx, "session.detach", map[string]any{"session_id": sid, "subscription_id": subscriptionID}, nil)
}

// LegacyApproval folds a legacy "approval" server request into a Prompt.
func LegacyApproval(r ServerRequest) (Prompt, bool) {
	if r.Method != "approval" {
		return Prompt{}, false
	}
	var p struct {
		Command     string   `json:"command"`
		Description string   `json:"description"`
		Choices     []string `json:"choices"`
		ToolName    string   `json:"tool_name"`
	}
	if json.Unmarshal(r.Params, &p) != nil {
		return Prompt{}, false
	}
	if len(p.Choices) == 0 {
		p.Choices = []string{"once", "deny"}
	}
	return Prompt{Kind: "approval", PromptID: r.ID, RequestID: r.ID, Command: p.Command, Description: p.Description, Choices: p.Choices, ToolName: p.ToolName}, true
}
