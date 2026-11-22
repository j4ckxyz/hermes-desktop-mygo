//go:build live

package hermes_test

import (
	"context"
	"testing"
	"time"

	"hermes-desktop/internal/hermes"
)

func TestLiveProbePaging(t *testing.T) {
	v := liveVals(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pa := &hermes.PasswordAuth{Username: v["HERMES_USERNAME"], Password: v["HERMES_PASSWORD"]}
	c, _ := hermes.New(v["HERMES_URL"], pa)
	t0 := time.Now()
	if err := pa.SignIn(ctx, c); err != nil {
		t.Fatal(err)
	}
	t.Logf("sign-in %v", time.Since(t0).Round(time.Millisecond))
	for _, lim := range []int{100, 500} {
		t1 := time.Now()
		p, err := c.AllSessions(ctx, lim, 0)
		t.Logf("AllSessions limit=%d: got %d total %d in %v err=%v", lim, len(p.Sessions), p.Total, time.Since(t1).Round(time.Millisecond), err)
	}
	t2 := time.Now()
	p, err := c.AllSessions(ctx, 500, 500)
	t.Logf("AllSessions offset=500: got %d in %v err=%v", len(p.Sessions), time.Since(t2).Round(time.Millisecond), err)
	t3 := time.Now()
	p, err = c.AllSessions(ctx, 500, 8500)
	t.Logf("AllSessions offset=8500: got %d in %v err=%v", len(p.Sessions), time.Since(t3).Round(time.Millisecond), err)
}
