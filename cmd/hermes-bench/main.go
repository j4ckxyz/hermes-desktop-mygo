// Command hermes-bench measures Hermes Desktop: cold launch (process start
// to first frame, and to the cached sidebar), switching chats and
// streaming a reply. Launches run the real binary on the current display
// (DISPLAY, or a private Xvfb on Linux).
//
//	go run ./cmd/hermes-bench [-n 10] [-bin path]
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"hermes-desktop/internal/bench"
	"hermes-desktop/internal/cache"
	"hermes-desktop/internal/hermes"
	"hermes-desktop/internal/hermes/hermestest"
	"hermes-desktop/internal/settings"
)

func main() {
	n := flag.Int("n", 10, "launches per scenario")
	bin := flag.String("bin", "", "app binary (built if empty)")
	switches := flag.Int("switches", 200, "chat switches")
	tokens := flag.Int("tokens", 600, "streamed tokens")
	flag.Parse()
	log.SetFlags(0)

	if *bin == "" {
		*bin = filepath.Join(os.TempDir(), "hermes-desktop-bench")
		if runtime.GOOS == "windows" {
			*bin += ".exe"
		}
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", *bin, "./cmd/hermes-desktop")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("Hermes Desktop benchmarks · %s/%s · %s\n\n", runtime.GOOS, runtime.GOARCH, time.Now().Format("2006-01-02 15:04"))

	// 1. A first launch: no instances, the connect screen.
	first, _ := launches(*bin, *n, func(dir string) {})
	fmt.Printf("| Launch: first run (connect screen), process start → first frame | %s |\n", first)

	// 2. A returning user: a saved instance with a warm cache, the
	// instance slow to answer (3 s), so the sidebar can only come from disk.
	srv := hermestest.New(hermestest.Options{Token: "t", Latency: 3 * time.Second})
	defer srv.Close()
	ff, ready := launches(*bin, *n, func(dir string) { seedHome(dir, srv.URL) })
	fmt.Printf("| Launch: returning user, process start → first frame | %s |\n", ff)
	fmt.Printf("| Launch: returning user, process start → sidebar drawn from the cache | %s |\n", ready)

	idle, err := bench.IdleFrame(100)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("| Baseline: a frame with nothing changed (headless, CPU-rendered) | %s |\n", idle)
	sw, err := bench.ChatSwitch(*switches)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("| Switch chats: select → frame showing the chat (headless, CPU-rendered) | %s |\n", sw)
	st, err := bench.Streaming(*tokens)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("| Stream a reply: token → frame showing it (headless, CPU-rendered) | %s |\n", st)
}

// seedHome writes a saved instance and a warm cache into dir.
func seedHome(dir, url string) {
	in := settings.Instance{ID: "bench", Name: "Main", URL: url, Auth: settings.AuthToken}
	settings.SaveInstances(filepath.Join(dir, "instances.json"), settings.Instances{List: []settings.Instance{in}})
	s := settings.Default()
	s.ActiveInstance, s.Appearance, s.DarkCanvas = "bench", "Dark", "Midnight"
	settings.Save(filepath.Join(dir, "settings.json"), s)
	c, err := cache.Open(filepath.Join(dir, "cache.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	d := hermestest.DesignSeed(time.Now())
	var ps []hermes.Profile
	var ss []hermes.Session
	b, _ := json.Marshal(d.Profiles)
	json.Unmarshal(b, &ps)
	b, _ = json.Marshal(d.Sessions)
	json.Unmarshal(b, &ss)
	ctx := context.Background()
	c.PutProfiles(ctx, "bench", ps)
	c.PutSessions(ctx, "bench", ss, true)
}

// launches runs the app n times and returns the stats of its marks.
func launches(bin string, n int, prepare func(dir string)) (first, ready bench.Stats) {
	var fs, rs []time.Duration
	for i := 0; i < n+1; i++ { // the first run warms the disk cache
		dir, _ := os.MkdirTemp("", "hermes-launch")
		prepare(dir)
		cmd := exec.Command(bin)
		cmd.Env = append(os.Environ(), "HERMES_DESKTOP_HOME="+dir, "HERMES_DESKTOP_BENCH=launch", "NO_AT_BRIDGE=1")
		out, _ := cmd.StdoutPipe()
		cmd.Stderr = nil
		t0 := time.Now()
		if err := cmd.Start(); err != nil {
			log.Fatal(err)
		}
		var f, r time.Duration
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) != 2 {
				continue
			}
			us, _ := strconv.Atoi(fields[1])
			// The app's own clock starts after exec and runtime start-up;
			// use the wall clock from exec for the first mark too.
			switch fields[0] {
			case "first-frame":
				f = time.Since(t0)
				_ = us
			case "sidebar-ready":
				r = time.Since(t0)
			}
		}
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			cmd.Process.Kill()
		}
		os.RemoveAll(dir)
		if i == 0 {
			continue
		}
		if f > 0 {
			fs = append(fs, f)
		}
		if r > 0 {
			rs = append(rs, r)
		}
	}
	return bench.Summarise(fs), bench.Summarise(rs)
}
