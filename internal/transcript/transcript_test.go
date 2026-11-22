package transcript

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hermes-desktop/internal/hermes"
	"hermes-desktop/internal/hermes/hermestest"
)

var t0 = time.Date(2026, 11, 22, 8, 2, 0, 0, time.UTC)

func replay(t *testing.T, l *Live, fixture string) {
	t.Helper()
	for i, raw := range hermestest.Fixture(fixture) {
		var ev hermes.Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatalf("%s line %d: %v", fixture, i, err)
		}
		l.Apply(ev, t0.Add(time.Duration(i)*time.Second))
	}
}

func TestCanonicalFixture(t *testing.T) {
	var l Live
	l.AddUser("u1", "what is in my project?", t0)
	replay(t, &l, "canonical_turn.jsonl")
	if len(l.Turns) != 1 {
		t.Fatalf("turns: %d", len(l.Turns))
	}
	turn := l.Turns[0]
	if turn.Status != Complete || turn.Running() {
		t.Fatalf("status %s", turn.Status)
	}
	if turn.Text != "Your project has a README and src/." {
		t.Fatalf("text %q", turn.Text)
	}
	if turn.Reasoning != "Need to list files first." {
		t.Fatalf("reasoning %q", turn.Reasoning)
	}
	if len(turn.Tools) != 1 {
		t.Fatalf("tools: %d (generating placeholder must merge into the start)", len(turn.Tools))
	}
	tl := turn.Tools[0]
	if tl.ID != "call-1" || tl.Status != ToolDone || tl.Result != "README.md\nsrc\n" {
		t.Fatalf("tool %+v", tl)
	}
	if l.LastSeq != 48 || l.Generation != 3 {
		t.Fatalf("seq %d gen %d", l.LastSeq, l.Generation)
	}
	if turn.Usage == nil || turn.Usage.Total != 1240 {
		t.Fatalf("usage %+v", turn.Usage)
	}
	lab := LabelOf(tl)
	if lab.Title != "Ran command" || lab.Detail != "ls" || !lab.Mono {
		t.Fatalf("label %+v", lab)
	}
}

func TestReplayIsIdempotent(t *testing.T) {
	var l Live
	l.AddUser("u1", "hi", t0)
	replay(t, &l, "canonical_turn.jsonl")
	v := l.Turns[0].Version
	replay(t, &l, "canonical_turn.jsonl") // a reconnect delivering the ring again
	if l.Turns[0].Version != v || len(l.Turns) != 1 || l.Turns[0].Text != "Your project has a README and src/." {
		t.Fatal("duplicate events changed the transcript")
	}
}

func TestLegacyFixtureSegmentsAndCommentary(t *testing.T) {
	var l Live
	l.AddUser("u1", "hello", t0)
	replay(t, &l, "legacy_turn.jsonl")
	turn := l.Turns[0]
	if turn.Status != Complete || turn.Text != "hi" {
		t.Fatalf("status %s text %q", turn.Status, turn.Text)
	}
	if len(turn.Interim) != 1 || turn.Interim[0] != "Let me look at the files first." {
		t.Fatalf("commentary before a tool call: %q", turn.Interim)
	}
	if len(turn.Tools) != 2 {
		t.Fatalf("tools %d", len(turn.Tools))
	}
	if turn.Tools[0].Duration != 400*time.Millisecond {
		t.Fatalf("duration %v", turn.Tools[0].Duration)
	}
	mcp := LabelOf(turn.Tools[1])
	if mcp.Title != "Mcp notion API post search" || mcp.Detail != "Geography" || mcp.Mono {
		t.Fatalf("mcp label %+v", mcp)
	}
	if !strings.Contains(turn.Tools[1].Result, "Geography fieldwork") {
		t.Fatalf("object result flattened: %q", turn.Tools[1].Result)
	}
	if turn.Summary() != "Ran 1 command and made 1 tool call" {
		t.Fatalf("summary %q", turn.Summary())
	}
}

func TestStreamingIsVisibleTokenByToken(t *testing.T) {
	var l Live
	l.AddUser("u1", "hello", t0)
	for i, tok := range []string{"Hel", "lo ", "there"} {
		changed := l.Apply(hermes.Event{Type: "message.delta", Seq: int64(i + 1), Payload: json.RawMessage(`{"text":"` + tok + `"}`)}, t0)
		if !changed {
			t.Fatal("a delta must change the view")
		}
	}
	if l.Current().Text != "Hello there" || !l.Running() {
		t.Fatalf("partial %q", l.Current().Text)
	}
}

func TestApprovalAndInterrupt(t *testing.T) {
	var l Live
	l.AddUser("u1", "clean up", t0)
	replay(t, &l, "approval_turn.jsonl")
	cur := l.Current()
	if cur == nil || cur.Prompt == nil || cur.Prompt.PromptID != "p" || cur.Prompt.Kind != "approval" || cur.Prompt.ExecutionGeneration != 4 {
		t.Fatalf("prompt %+v", cur)
	}
	if got := strings.Join(cur.Prompt.Choices, ","); got != "once,deny" {
		t.Fatal(got)
	}
	l.Apply(hermes.Event{Type: "approval.settled", Payload: json.RawMessage(`{"prompt_id":"p"}`)}, t0)
	if cur.Prompt != nil {
		t.Fatal("settled card must clear")
	}
	l.Apply(hermes.Event{Type: "message.complete", Payload: json.RawMessage(`{"text":"","status":"interrupted","outcome":"cancelled"}`)}, t0)
	if cur.Status != Interrupted || cur.Tools[0].Status != ToolFailed {
		t.Fatalf("interrupted: %s, tool %v", cur.Status, cur.Tools[0].Status)
	}
}

func TestErrorCompletion(t *testing.T) {
	var l Live
	l.AddUser("u1", "x", t0)
	l.Apply(hermes.Event{Type: "message.complete", Payload: json.RawMessage(`{"text":"Worker execution is unknown.","content":"Worker execution is unknown.","admission_id":"a","outcome":"unknown","status":"error"}`)}, t0)
	if l.Turns[0].Status != Errored || l.Turns[0].Error == "" {
		t.Fatalf("%+v", l.Turns[0])
	}
}

func TestFromMessagesGroupsTurns(t *testing.T) {
	// Verbatim from tests/gateway/test_session_api.py:529-545.
	raw := `[
	  {"role": "user", "content": "search then summarize"},
	  {"role": "assistant", "content": "Let me search for that:",
	   "tool_calls": [{"id": "call_1", "type": "function",
	                   "function": {"name": "web_search", "arguments": "{}"}}]},
	  {"role": "tool", "content": "results", "tool_call_id": "call_1", "tool_name": "web_search"},
	  {"role": "assistant", "content": "Here is the summary."}
	]`
	var msgs []hermes.Message
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		t.Fatal(err)
	}
	turns := FromMessages(msgs)
	if len(turns) != 1 {
		t.Fatalf("turns %d", len(turns))
	}
	tn := turns[0]
	if tn.User != "search then summarize" || tn.Text != "Here is the summary." {
		t.Fatalf("%+v", tn)
	}
	if len(tn.Interim) != 1 || tn.Interim[0] != "Let me search for that:" {
		t.Fatalf("interim %q", tn.Interim)
	}
	if len(tn.Tools) != 1 || tn.Tools[0].Result != "results" || tn.Tools[0].Name != "web_search" {
		t.Fatalf("tools %+v", tn.Tools)
	}
}

func TestFromMessagesContentParts(t *testing.T) {
	var msgs []hermes.Message
	json.Unmarshal([]byte(`[{"id":1,"role":"user","content":[{"type":"text","text":"what is this?"},{"type":"image_url","image_url":{"url":"[image]"}}]},{"id":2,"role":"assistant","content":"A cat."}]`), &msgs)
	turns := FromMessages(msgs)
	if turns[0].User != "what is this?\n[image]" {
		t.Fatalf("%q", turns[0].User)
	}
}

func TestDesignSeedTranscript(t *testing.T) {
	seed := hermestest.DesignSeed(t0)
	data, _ := json.Marshal(seed.Messages["20261122_080205_fb120f"])
	var msgs []hermes.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		t.Fatal(err)
	}
	turns := FromMessages(msgs)
	if len(turns) != 2 {
		t.Fatalf("turns %d", len(turns))
	}
	if got := turns[0].Summary(); got != "Ran 11 commands and made 7 tool calls" {
		t.Fatalf("summary %q", got)
	}
	// The second message has no reply yet; a live turn the agent is
	// running answers it.
	var l Live
	replay(t, &l, "design_turn.jsonl")
	merged := Merge(turns, &l)
	if len(merged) != 2 || !merged[1].Running() || !strings.HasPrefix(merged[1].User, "Nice.") || len(merged[1].Tools) != 3 {
		t.Fatalf("merge: %d turns, last %+v", len(merged), merged[len(merged)-1])
	}
	labels := []Label{LabelOf(merged[1].Tools[0]), LabelOf(merged[1].Tools[1]), LabelOf(merged[1].Tools[2])}
	if labels[0].Title != "Tool describe" || labels[0].Detail != `{ "names": [ "mcp__notion__API_post_search" ] }` {
		t.Fatalf("describe %+v", labels[0])
	}
	if labels[2].Title != "Running command" || labels[2].Detail != `grep -c '^NOTION_API_KEY=' ~/.hermes/.env` {
		t.Fatalf("command %+v", labels[2])
	}
}

func TestHumanizeAndCompactJSON(t *testing.T) {
	cases := map[string]string{"mcp__notion__API_post_search": "Mcp notion API post search", "web.search": "Web search", "": "Tool", "read_file": "Read file"}
	for in, want := range cases {
		if got := Humanize(in); got != want {
			t.Errorf("Humanize(%q) = %q", in, got)
		}
	}
	if got := CompactJSON(json.RawMessage(`{"a":1,"b":{"c":[1,2]},"d":[]}`)); got != `{ "a": 1, "b": { "c": [ 1, 2 ] }, "d": [] }` {
		t.Errorf("CompactJSON = %q", got)
	}
}

// The stream a live 0.23.3 instance sent (sanitized): a command tool call
// with reasoning and thinking, on the legacy wire.
func TestLiveRecordedStream(t *testing.T) {
	var l Live
	l.AddUser("u1", "run the command", t0)
	replay(t, &l, "live_legacy_0233.jsonl")
	turn := l.Turns[0]
	if turn.Status != Complete || turn.Text != "It printed `hermes-desktop-live-test`." {
		t.Fatalf("status %s text %q", turn.Status, turn.Text)
	}
	if len(turn.Tools) != 1 || turn.Tools[0].Status != ToolDone || !strings.Contains(turn.Tools[0].Result, "hermes-desktop-live-test") {
		t.Fatalf("tools %+v", turn.Tools)
	}
	if l := LabelOf(turn.Tools[0]); l.Title != "Ran command" || l.Detail != "echo hermes-desktop-live-test" {
		t.Fatalf("label %+v", l)
	}
	if turn.Reasoning == "" || turn.Thinking != "" {
		t.Fatalf("reasoning %q thinking %q", turn.Reasoning, turn.Thinking)
	}
}
