package transcript

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"unicode"
)

// Label is how a tool-call pill reads: a bold verb phrase, then a detail
// in the body font or in mono (design/Main.dc.html, the tool pills).
type Label struct {
	Title  string // "Running command", "Mcp notion API post search"
	Detail string // "grep -c …", "Geography", `{ "names": [ … ] }`
	Mono   bool
}

// primaryArgs are the arguments that say what a call is about, most
// telling first; when one is a string, it alone is the detail.
var primaryArgs = []string{"command", "cmd", "query", "q", "search", "url", "path", "file_path", "file", "pattern", "name", "title", "text", "prompt", "goal"}

// LabelOf describes a tool call for its pill.
func LabelOf(t *Tool) Label {
	args := map[string]any{}
	if len(t.Args) > 0 {
		json.Unmarshal(t.Args, &args)
	}
	if IsCommand(t.Name) {
		l := Label{Title: "Ran command", Mono: true}
		switch t.Status {
		case ToolGenerating, ToolRunning:
			l.Title = "Running command"
		case ToolFailed:
			l.Title = "Command failed"
		}
		for _, k := range []string{"command", "cmd", "script"} {
			if s, ok := args[k].(string); ok {
				l.Detail = oneLine(s)
				return l
			}
		}
		l.Detail = firstNonEmpty(oneLine(t.Context), oneLine(t.ArgsText))
		return l
	}
	l := Label{Title: Humanize(t.Name)}
	for _, k := range primaryArgs {
		if s, ok := args[k].(string); ok && strings.TrimSpace(s) != "" {
			l.Detail = oneLine(s)
			l.Mono = k == "path" || k == "file_path" || k == "file" || k == "pattern"
			return l
		}
	}
	switch {
	case len(args) > 0:
		l.Detail, l.Mono = CompactJSON(t.Args), true
	case t.Context != "":
		l.Detail = oneLine(t.Context)
	case t.ArgsText != "":
		l.Detail, l.Mono = oneLine(t.ArgsText), true
	}
	return l
}

// Humanize turns a tool name into words: "mcp__notion__API_post_search"
// reads "Mcp notion API post search"; "web.search" reads "Web search".
func Humanize(name string) string {
	f := strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '.' || r == '-' || r == ' ' })
	if len(f) == 0 {
		return "Tool"
	}
	r := []rune(f[0])
	r[0] = unicode.ToUpper(r[0])
	f[0] = string(r)
	return strings.Join(f, " ")
}

// CompactJSON formats JSON on one line with the design's spacing:
// { "names": [ "x" ] }. Keys keep the order they came in.
func CompactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := writeValue(&b, d); err != nil {
		return oneLine(string(raw))
	}
	return truncate(b.String(), 400)
}

func writeValue(b *bytes.Buffer, d *json.Decoder) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		enc, err := json.Marshal(tok)
		if err != nil {
			return err
		}
		b.Write(enc)
		return nil
	}
	closer := "]"
	if delim == '{' {
		closer = "}"
	}
	b.WriteString(string(delim))
	n := 0
	for d.More() {
		if n > 0 {
			b.WriteString(",")
		}
		b.WriteString(" ")
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			enc, _ := json.Marshal(key)
			b.Write(enc)
			b.WriteString(": ")
		}
		if err := writeValue(b, d); err != nil {
			return err
		}
		n++
	}
	if _, err := d.Token(); err != nil { // the closing delimiter
		return err
	}
	if n > 0 {
		b.WriteString(" ")
	}
	b.WriteString(closer)
	return nil
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return truncate(s, 300)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// SortedKeys returns an object's keys in order (for stable test output).
func SortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
