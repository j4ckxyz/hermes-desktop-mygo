package hermestest

import (
	"fmt"
	"time"
)

// DesignSeed is the content of design/Main.dc.html: three agents, the
// sessions in its sidebar (with 234 more under Hermes and 3 under
// sidequest) and the transcript it shows.
func DesignSeed(now time.Time) Data {
	ago := func(d time.Duration) float64 { return float64(now.Add(-d).Unix()) }
	d := Data{
		Profiles: []map[string]any{
			{"name": "default", "display_name": "Hermes", "is_default": true, "model": "anthropic/claude-sonnet-4.6", "provider": "openrouter", "has_avatar": false,
				"ui_meta": map[string]any{"hermes-bots": map[string]any{"color": "#B9566F"}}},
			{"name": "sidequest", "display_name": "sidequest", "model": "openai/gpt-5.2", "provider": "openrouter",
				"ui_meta": map[string]any{"hermes-bots": map[string]any{"color": "#8C6E52"}}},
			{"name": "helix", "display_name": "helix", "model": "anthropic/claude-opus-4.6", "provider": "anthropic",
				"ui_meta": map[string]any{"hermes-bots": map[string]any{"color": "#93CBF1", "shape": "bot"}}},
		},
		Messages: map[string][]map[string]any{},
	}
	add := func(id, profile, title string, last time.Duration, extra map[string]any) {
		row := map[string]any{"id": id, "title": title, "profile": profile, "source": "gui", "model": "anthropic/claude-sonnet-4.6",
			"started_at": ago(last + time.Hour), "last_active": ago(last), "message_count": 4, "tool_call_count": 0,
			"pinned": false, "archived": false, "unread": false, "is_active": false, "preview": ""}
		for k, v := range extra {
			row[k] = v
		}
		d.Sessions = append(d.Sessions, row)
	}
	main := "20261122_080205_fb120f"
	add(main, "default", "Shorten morning news and surf links", 30*time.Second, map[string]any{"is_active": true, "pinned": true, "message_count": 22, "tool_call_count": 18})
	add("20261122_071000_a1b2c3", "default", "Plot key findings on AI benchmarks", 3*time.Minute, map[string]any{"is_active": true, "unread": true})
	add("20261122_070000_d4e5f6", "default", "Call Notion MCP search for fieldwork pages", 2*time.Minute+30*time.Second, map[string]any{"is_active": true})
	add("20261122_060000_0a0b0c", "default", "Hey #2", 25*time.Minute, nil)
	add("20261122_010000_0d0e0f", "default", "Connect Discogs account integration", 7*time.Hour, nil)
	for i := range 234 {
		add(fmt.Sprintf("20261%03d_000000_%06x", 100+i, i), "default", fmt.Sprintf("Older chat %d", 234-i), 8*time.Hour+time.Duration(i)*7*time.Hour, map[string]any{"message_count": 2 + i%9})
	}
	add("20261121_230000_5a5b5c", "sidequest", "", 8*time.Hour, map[string]any{"preview": ""})
	for i := range 3 {
		add(fmt.Sprintf("20261120_0%d0000_5d5e5f", i), "sidequest", fmt.Sprintf("Side quest %d", i+1), 30*time.Hour+time.Duration(i)*time.Hour, nil)
	}
	add("20261121_220000_6a6b6c", "helix", "", 8*time.Hour, nil)
	add("20261121_130000_6d6e6f", "helix", "Summarize boss, contract, deliverables", 17*time.Hour, nil)

	t0 := time.Date(now.Year(), now.Month(), now.Day(), 8, 2, 0, 0, now.Location())
	ts := func(d time.Duration) float64 { return float64(t0.Add(d).Unix()) }
	msgs := []map[string]any{
		{"id": 1, "role": "user", "content": "The morning brief is getting long. Can you shorten the news and surf links so each is just the site name and headline?", "timestamp": ts(0)},
	}
	id := 2
	for i := range 18 {
		// 11 commands, then 7 other tools: "Ran 11 commands and made 7 tool calls".
		name, args := "terminal", fmt.Sprintf(`{"command":"hermes cron show morning-%d"}`, i)
		switch {
		case i >= 11 && i%2 == 0:
			name, args = "read_file", fmt.Sprintf(`{"path":"~/.hermes/cron/job-%d.yaml"}`, i)
		case i >= 11:
			name, args = "web_search", `{"query":"surf report format"}`
		}
		call := fmt.Sprintf("call_%d", i)
		msgs = append(msgs,
			map[string]any{"id": id, "role": "assistant", "content": "", "timestamp": ts(time.Duration(id) * time.Second),
				"tool_calls": []any{map[string]any{"id": call, "type": "function", "function": map[string]any{"name": name, "arguments": args}}}},
			map[string]any{"id": id + 1, "role": "tool", "content": "ok", "tool_call_id": call, "tool_name": name, "timestamp": ts(time.Duration(id+1) * time.Second)},
		)
		id += 2
	}
	msgs = append(msgs, map[string]any{"id": id, "role": "assistant", "timestamp": ts(3 * time.Minute), "content": "Done. Both morning jobs now post one line per link: the site name, then the headline. I ran the news job once to check and it reads much more cleanly.\n\n" +
		"Also available from a shell on the VPS:\n\n" +
		"- `hermes sessions list` to see recent sessions\n" +
		"- `hermes --resume 20261122_080205_fb120f` to continue this one\n\n" +
		"https://hermes-vps.your-tailnet.ts.net/cron"})
	msgs = append(msgs, map[string]any{"id": id + 1, "role": "user", "timestamp": ts(22 * time.Minute), "content": "Nice. Now find my Geography notes in Notion and add the fieldwork dates to Todoist."})
	d.Messages[main] = msgs
	d.Messages["20261122_060000_0a0b0c"] = []map[string]any{
		{"id": 1, "role": "user", "content": "Hey", "timestamp": ago(26 * time.Minute)},
		{"id": 2, "role": "assistant", "content": "Hey! What can I help with?", "timestamp": ago(25 * time.Minute)},
	}
	return d
}
