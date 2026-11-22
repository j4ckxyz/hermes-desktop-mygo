//go:build live

package hermes_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"hermes-desktop/internal/hermes"
)

// TestLiveCleanup finds the chats the live tests created (by their titles,
// started today) and, with HERMES_CLEANUP=delete, deletes them.
func TestLiveCleanup(t *testing.T) {
	v := liveVals(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pa := &hermes.PasswordAuth{Username: v["HERMES_USERNAME"], Password: v["HERMES_PASSWORD"]}
	c, _ := hermes.New(v["HERMES_URL"], pa)
	if err := pa.SignIn(ctx, c); err != nil {
		t.Fatal(err)
	}
	page, err := c.AllSessions(ctx, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().Add(-6 * time.Hour)
	var hits []hermes.Session
	for _, s := range page.Sessions {
		mine := s.Title == "Hermes Desktop live test" || strings.HasPrefix(strings.ToLower(s.Title), "run echo hermes-desktop-live")
		if mine && s.StartedAt.After(cutoff) {
			hits = append(hits, s)
			t.Logf("match: %s %q profile=%s started=%s messages=%d", s.ID, s.Title, s.Profile, s.StartedAt.Format("15:04"), s.MessageCount)
		}
	}
	t.Logf("%d matching chats", len(hits))
	if os.Getenv("HERMES_CLEANUP") != "delete" {
		return
	}
	for _, s := range hits {
		if err := c.DeleteSession(ctx, s.Profile, s.ID); err != nil {
			t.Errorf("delete %s: %v", s.ID, err)
		} else {
			t.Logf("deleted %s", s.ID)
		}
	}
}
