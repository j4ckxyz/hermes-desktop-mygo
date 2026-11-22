package transcript

import (
	"fmt"
	"strings"
)

// Markdown renders a chat for "Export as Markdown" and "Share".
func Markdown(title string, turns []*Turn) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	for _, t := range turns {
		if t.User != "" {
			b.WriteString("## You")
			if !t.UserTime.IsZero() {
				fmt.Fprintf(&b, " · %s", t.UserTime.Format("2 Jan 2006 15:04"))
			}
			b.WriteString("\n\n")
			b.WriteString(t.User)
			b.WriteString("\n\n")
		}
		if len(t.Tools) == 0 && t.Text == "" && t.Error == "" {
			continue
		}
		b.WriteString("## Hermes\n\n")
		if len(t.Tools) > 0 {
			if s := t.Summary(); s != "" {
				fmt.Fprintf(&b, "<details><summary>%s</summary>\n\n", s)
			}
			for _, tl := range t.Tools {
				l := LabelOf(tl)
				if l.Detail == "" {
					fmt.Fprintf(&b, "- **%s**\n", l.Title)
				} else if l.Mono {
					fmt.Fprintf(&b, "- **%s** `%s`\n", l.Title, strings.ReplaceAll(l.Detail, "`", "'"))
				} else {
					fmt.Fprintf(&b, "- **%s** %s\n", l.Title, l.Detail)
				}
			}
			b.WriteString("\n</details>\n\n")
		}
		if t.Text != "" {
			b.WriteString(t.Text)
			b.WriteString("\n\n")
		}
		if t.Error != "" {
			fmt.Fprintf(&b, "> **Error:** %s\n\n", t.Error)
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
