package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"testing"

	"hermes-desktop/internal/hermes"
)

func TestRoundTrip(t *testing.T) {
	b := &MemoryBackend{}
	s := NewWithBackend(b)
	if _, err := s.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty: %v", err)
	}
	c := Credentials{Username: "jack", Password: "pw", Tokens: &hermes.Tokens{AccessToken: "at", RefreshToken: "rt"}}
	if err := s.Set("a", c); err != nil {
		t.Fatal(err)
	}
	// A new Store (a relaunch) reads it back from the backend.
	got, err := NewWithBackend(b).Get("a")
	if err != nil || got.Password != "pw" || got.Tokens.RefreshToken != "rt" {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWithBackend(b).Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted")
	}
}

func TestNoCredentialStoreFallsBackToMemory(t *testing.T) {
	b := &MemoryBackend{Fail: fmt.Errorf("no secret service: %w", errors.ErrUnsupported)}
	s := NewWithBackend(b)
	if err := s.Set("a", Credentials{Token: "tok"}); err != nil {
		t.Fatalf("Set must not fail without a keychain: %v", err)
	}
	if s.Persistent() {
		t.Fatal("must report that credentials will not survive a restart")
	}
	if got, err := s.Get("a"); err != nil || got.Token != "tok" {
		t.Fatal("kept for the session")
	}
	if len(b.M) != 0 {
		t.Fatal("nothing may be written anywhere")
	}
}

func TestOtherErrorsSurface(t *testing.T) {
	s := NewWithBackend(&MemoryBackend{Fail: errors.New("keychain locked")})
	if err := s.Set("a", Credentials{Token: "x"}); err == nil {
		t.Fatal("a locked keychain is an error the user should see")
	}
}

// The credentials are never written to the config or cache directories.
func TestNoPlaintextOnDisk(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	s := NewWithBackend(&MemoryBackend{})
	s.Set("a", Credentials{Token: "super-secret-token-value"})
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		data, _ := os.ReadFile(dir + "/" + e.Name())
		if bytes.Contains(data, []byte("super-secret")) {
			t.Fatalf("secret leaked into %s", e.Name())
		}
	}
}
