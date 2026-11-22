package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hermes-desktop/internal/hermes"
	"hermes-desktop/internal/hermes/hermestest"
)

func openTemp(t *testing.T) (*Cache, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sub", "cache.db")
	c, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, p
}

// seedSessions converts the fake server's seed into client types, as a
// sync would store them.
func seedSessions(t *testing.T) ([]hermes.Profile, []hermes.Session, []hermes.Message) {
	d := hermestest.DesignSeed(time.Now())
	var ps []hermes.Profile
	var ss []hermes.Session
	var ms []hermes.Message
	for _, v := range []struct {
		in  any
		out any
	}{{d.Profiles, &ps}, {d.Sessions, &ss}, {d.Messages["20261122_080205_fb120f"], &ms}} {
		data, _ := json.Marshal(v.in)
		if err := json.Unmarshal(data, v.out); err != nil {
			t.Fatal(err)
		}
	}
	return ps, ss, ms
}

func TestRoundTripAndReopen(t *testing.T) {
	ctx := context.Background()
	c, path := openTemp(t)
	ps, ss, ms := seedSessions(t)
	if err := c.PutProfiles(ctx, "inst1", ps); err != nil {
		t.Fatal(err)
	}
	if err := c.PutSessions(ctx, "inst1", ss, true); err != nil {
		t.Fatal(err)
	}
	fetched := time.Now().Truncate(time.Millisecond)
	if err := c.PutTranscript(ctx, "inst1", ss[0].ID, ms, fetched); err != nil {
		t.Fatal(err)
	}
	c.Close()

	// Reopen: what a cold launch reads before any network.
	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	gotP, _ := c2.Profiles(ctx, "inst1")
	if len(gotP) != 3 || gotP[0].Title() != "Hermes" || gotP[2].Name != "helix" {
		t.Fatalf("profiles %+v", gotP)
	}
	gotS, _ := c2.Sessions(ctx, "inst1")
	if len(gotS) != len(ss) {
		t.Fatalf("sessions %d want %d", len(gotS), len(ss))
	}
	for i := 1; i < len(gotS); i++ {
		if gotS[i].Activity().After(gotS[i-1].Activity()) {
			t.Fatal("not most recent first")
		}
	}
	gotM, at, ok, err := c2.Transcript(ctx, "inst1", ss[0].ID)
	if err != nil || !ok || len(gotM) != len(ms) || gotM[1].ToolCalls[0].ID != "call_0" {
		t.Fatalf("transcript ok=%v n=%d err=%v", ok, len(gotM), err)
	}
	if d := at.Sub(fetched); d > time.Millisecond || d < -time.Millisecond {
		t.Fatalf("fetched_at %v vs %v", at, fetched)
	}
	if _, _, ok, _ := c2.Transcript(ctx, "inst2", ss[0].ID); ok {
		t.Fatal("instances must not share rows")
	}
}

func TestReplaceDropsVanishedSessions(t *testing.T) {
	ctx := context.Background()
	c, _ := openTemp(t)
	_, ss, ms := seedSessions(t)
	c.PutSessions(ctx, "i", ss, true)
	c.PutTranscript(ctx, "i", ss[1].ID, ms, time.Now())
	// A full sync that no longer has ss[1] (deleted on another device).
	keep := append([]hermes.Session{ss[0]}, ss[2:]...)
	if err := c.PutSessions(ctx, "i", keep, true); err != nil {
		t.Fatal(err)
	}
	got, _ := c.Sessions(ctx, "i")
	if len(got) != len(ss)-1 {
		t.Fatalf("got %d", len(got))
	}
	if _, _, ok, _ := c.Transcript(ctx, "i", ss[1].ID); ok {
		t.Fatal("the vanished session's transcript must go too")
	}
	// A partial upsert keeps the rest.
	ss[0].Title = "Renamed"
	c.PutSessions(ctx, "i", ss[:1], false)
	got, _ = c.Sessions(ctx, "i")
	if len(got) != len(ss)-1 || got[0].Title != "Renamed" {
		t.Fatalf("upsert: %d %q", len(got), got[0].Title)
	}
	c.DeleteSession(ctx, "i", ss[0].ID)
	if got, _ = c.Sessions(ctx, "i"); len(got) != len(ss)-2 {
		t.Fatal("delete")
	}
	c.ForgetInstance(ctx, "i")
	if got, _ = c.Sessions(ctx, "i"); len(got) != 0 {
		t.Fatal("forget")
	}
}

func TestCorruptFileIsReplaced(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache.db")
	os.WriteFile(p, []byte("this is not a database, just junk bytes......................................................................................"), 0o600)
	c, err := Open(p)
	if err != nil {
		t.Fatalf("corrupt cache must be rebuilt: %v", err)
	}
	defer c.Close()
	if err := c.PutProfiles(context.Background(), "i", []hermes.Profile{{Name: "x"}}); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentWritersAndReaders(t *testing.T) {
	ctx := context.Background()
	c, _ := openTemp(t)
	_, ss, ms := seedSessions(t)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := range 10 {
				if err := c.PutTranscript(ctx, "i", fmt.Sprintf("s%d-%d", w, i), ms, time.Now()); err != nil {
					errs <- err
				}
			}
		}()
		go func() {
			defer wg.Done()
			if err := c.PutSessions(ctx, "i", ss[w*10:w*10+10], false); err != nil {
				errs <- err
			}
			if _, err := c.Sessions(ctx, "i"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got, _ := c.Sessions(ctx, "i"); len(got) != 80 {
		t.Fatalf("got %d", len(got))
	}
}

// BenchmarkColdRead is the cache's share of a cold launch: open the file
// and read the sidebar (every session of the design seed).
func BenchmarkColdRead(b *testing.B) {
	ctx := context.Background()
	p := filepath.Join(b.TempDir(), "cache.db")
	c, _ := Open(p)
	_, ss, _ := seedSessions(&testing.T{})
	c.PutSessions(ctx, "i", ss, true)
	c.Close()
	b.ResetTimer()
	for b.Loop() {
		c, err := Open(p)
		if err != nil {
			b.Fatal(err)
		}
		if got, _ := c.Sessions(ctx, "i"); len(got) != len(ss) {
			b.Fatal(len(got))
		}
		c.Close()
	}
}

func TestPruneKeepsTheMostRecentlyOpened(t *testing.T) {
	c, _ := openTemp(t)
	ctx := context.Background()
	t0 := time.Unix(1_800_000_000, 0)
	msgs := []hermes.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}}
	for i := range 10 {
		inst := "a"
		if i%2 == 1 {
			inst = "b" // across instances
		}
		if err := c.PutTranscript(ctx, inst, fmt.Sprint("s", i), msgs, t0.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	// s0, fetched first, was opened last (from the cache, offline).
	if err := c.OpenedTranscript(ctx, "a", "s0", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	n, err := c.PruneTranscripts(ctx, 4)
	if err != nil || n != 6 {
		t.Fatalf("removed %d, %v", n, err)
	}
	keep := map[string]bool{}
	for i := range 10 {
		inst := map[bool]string{true: "b", false: "a"}[i%2 == 1]
		if _, _, ok, _ := c.Transcript(ctx, inst, fmt.Sprint("s", i)); ok {
			keep[fmt.Sprint("s", i)] = true
		}
	}
	if len(keep) != 4 || !keep["s0"] || !keep["s9"] || !keep["s8"] || !keep["s7"] {
		t.Fatalf("kept %v", keep)
	}
	if n, _ := c.CountTranscripts(ctx); n != 4 {
		t.Fatalf("count %d", n)
	}
}

func TestSchemaOneIsUpgradedInPlace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache.db")
	c, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Make it a version 1 file: no opened_at, schema 1, one transcript.
	for _, q := range []string{
		`DROP TABLE transcripts`,
		`CREATE TABLE transcripts (instance TEXT NOT NULL, session TEXT NOT NULL, fetched_at REAL NOT NULL, data BLOB NOT NULL, PRIMARY KEY (instance, session))`,
		`INSERT INTO transcripts VALUES ('a','s1',1800000000,'[]')`,
		`UPDATE meta SET value='1' WHERE key='schema'`,
	} {
		if _, err := c.db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	c.Close()
	c, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, _, ok, _ := c.Transcript(ctx, "a", "s1"); !ok {
		t.Fatal("the upgrade keeps what was cached")
	}
	var opened float64
	c.db.QueryRowContext(ctx, `SELECT opened_at FROM transcripts`).Scan(&opened)
	if opened != 1800000000 {
		t.Fatalf("opened_at starts as fetched_at, got %v", opened)
	}
}
