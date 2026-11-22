package settings

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// AuthMode is how an instance signs in.
type AuthMode string

const (
	AuthToken    AuthMode = "token"    // HERMES_DASHBOARD_SESSION_TOKEN ("API key")
	AuthPassword AuthMode = "password" // dashboard username and password
)

// Instance is a saved Hermes instance. Its credentials are in the OS
// keychain (package secrets), never here.
type Instance struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	URL      string   `json:"url"`
	Local    bool     `json:"local"`
	Auth     AuthMode `json:"auth"`
	Username string   `json:"username,omitempty"`
}

// Host is the address without its scheme, for display.
func (i Instance) Host() string {
	h := i.URL
	if k := strings.Index(h, "://"); k >= 0 {
		h = h[k+3:]
	}
	return strings.TrimSuffix(h, "/")
}

// AuthLabel is how the saved-instances list names the sign-in method.
func (i Instance) AuthLabel() string {
	if i.Auth == AuthPassword {
		return "Dashboard login"
	}
	return "API key"
}

// Instances is the list of saved instances, persisted as instances.json
// next to settings.json.
type Instances struct {
	List []Instance `json:"instances"`
}

// NewInstanceID returns a random id.
func NewInstanceID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Find returns the instance with id.
func (is *Instances) Find(id string) (Instance, bool) {
	for _, i := range is.List {
		if i.ID == id {
			return i, true
		}
	}
	return Instance{}, false
}

// Put adds or replaces an instance.
func (is *Instances) Put(in Instance) {
	for k, i := range is.List {
		if i.ID == in.ID {
			is.List[k] = in
			return
		}
	}
	is.List = append(is.List, in)
}

// Remove deletes an instance.
func (is *Instances) Remove(id string) {
	is.List = slices.DeleteFunc(is.List, func(i Instance) bool { return i.ID == id })
}

// InstancesPath is instances.json beside the settings file.
func InstancesPath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "instances.json")
}

// LoadInstances reads the saved instances; a missing file is an empty list.
func LoadInstances(path string) (Instances, error) {
	var is Instances
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return is, nil
	}
	if err != nil {
		return is, err
	}
	err = json.Unmarshal(data, &is)
	return is, err
}

// SaveInstances writes the list atomically.
func SaveInstances(path string, is Instances) error {
	data, err := json.MarshalIndent(is, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}
