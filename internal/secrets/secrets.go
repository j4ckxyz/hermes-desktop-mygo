// Package secrets keeps sign-in credentials in the OS credential store:
// the login Keychain on macOS, the Credential Manager on Windows and the
// Secret Service (libsecret) on Linux. Nothing is ever written in plain
// text. Where the system has no store (minimal or headless Linux), the
// credentials live in memory for the session only and the user signs in
// again on the next launch; Persistent reports that.
package secrets

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/egoist/mygo"

	"hermes-desktop/internal/hermes"
)

// Credentials are what an instance needs to sign in.
type Credentials struct {
	// Token is HERMES_DASHBOARD_SESSION_TOKEN ("API key" mode).
	Token string `json:"token,omitempty"`
	// Username and Password are the dashboard login.
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// Tokens are the bearer tokens the login was exchanged for.
	Tokens *hermes.Tokens `json:"tokens,omitempty"`
}

// ErrNotFound is returned for an instance with no stored credentials.
var ErrNotFound = errors.New("secrets: no credentials stored")

// Backend is a raw key/value credential store.
type Backend interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

// Store keeps credentials per instance. It is safe for concurrent use;
// its methods block on the OS store, so call them off the UI thread.
type Store struct {
	backend Backend
	mu      sync.Mutex
	memory  map[string]Credentials // the fallback, and a read cache
	// persistent is false once the OS store said it is unavailable.
	persistent bool
}

// New returns a Store on the OS credential store.
func New() *Store { return NewWithBackend(keychain{mygo.Secrets.Service("Hermes Desktop")}) }

// NewWithBackend returns a Store on b (tests pass a fake).
func NewWithBackend(b Backend) *Store {
	return &Store{backend: b, memory: map[string]Credentials{}, persistent: true}
}

// Persistent reports whether credentials survive a restart.
func (s *Store) Persistent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistent
}

func key(instanceID string) string { return "instance:" + instanceID }

// Get returns an instance's credentials.
func (s *Store) Get(instanceID string) (Credentials, error) {
	s.mu.Lock()
	if c, ok := s.memory[instanceID]; ok {
		s.mu.Unlock()
		return c, nil
	}
	persistent := s.persistent
	s.mu.Unlock()
	if !persistent {
		return Credentials{}, ErrNotFound
	}
	data, err := s.backend.Get(key(instanceID))
	switch {
	case errors.Is(err, mygo.ErrSecretNotFound):
		return Credentials{}, ErrNotFound
	case errors.Is(err, errors.ErrUnsupported):
		s.markVolatile()
		return Credentials{}, ErrNotFound
	case err != nil:
		return Credentials{}, err
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return Credentials{}, ErrNotFound
	}
	s.mu.Lock()
	s.memory[instanceID] = c
	s.mu.Unlock()
	return c, nil
}

// Set stores an instance's credentials.
func (s *Store) Set(instanceID string, c Credentials) error {
	s.mu.Lock()
	s.memory[instanceID] = c
	persistent := s.persistent
	s.mu.Unlock()
	if !persistent {
		return nil
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := s.backend.Set(key(instanceID), data); err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			s.markVolatile()
			return nil
		}
		return err
	}
	return nil
}

// Delete forgets an instance's credentials.
func (s *Store) Delete(instanceID string) error {
	s.mu.Lock()
	delete(s.memory, instanceID)
	persistent := s.persistent
	s.mu.Unlock()
	if !persistent {
		return nil
	}
	err := s.backend.Delete(key(instanceID))
	if errors.Is(err, errors.ErrUnsupported) {
		s.markVolatile()
		return nil
	}
	return err
}

func (s *Store) markVolatile() {
	s.mu.Lock()
	s.persistent = false
	s.mu.Unlock()
}

type keychain struct{ m mygo.SecretsModule }

func (k keychain) Get(key string) ([]byte, error) { return k.m.Get(key) }
func (k keychain) Set(key string, v []byte) error { return k.m.Set(key, v) }
func (k keychain) Delete(key string) error        { return k.m.Delete(key) }

// MemoryBackend is an in-memory Backend for tests.
type MemoryBackend struct {
	mu   sync.Mutex
	M    map[string][]byte
	Fail error // returned by every call when set
}

func (m *MemoryBackend) Get(k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	v, ok := m.M[k]
	if !ok {
		return nil, mygo.ErrSecretNotFound
	}
	return v, nil
}

func (m *MemoryBackend) Set(k string, v []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	if m.M == nil {
		m.M = map[string][]byte{}
	}
	m.M[k] = append([]byte(nil), v...)
	return nil
}

func (m *MemoryBackend) Delete(k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	delete(m.M, k)
	return nil
}
