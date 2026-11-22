package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Resume is what the app was showing when it restarted itself for an
// update: the next launch opens the same instance and chat, with the
// unsent text of each composer.
type Resume struct {
	Instance string `json:"instance,omitempty"`
	// Chat is the open session, "" for a new chat.
	Chat string `json:"chat,omitempty"`
	// NewProfile and NewDraft are the new chat's agent and text.
	NewProfile string `json:"newProfile,omitempty"`
	NewDraft   string `json:"newDraft,omitempty"`
	// Drafts are the unsent texts of open chats, by session.
	Drafts map[string]string `json:"drafts,omitempty"`
}

// ResumePath is resume.json next to the settings.
func ResumePath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "resume.json")
}

// SaveResume writes r to path, readable by the user alone (drafts are
// the user's words).
func SaveResume(path string, r Resume) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// TakeResume reads the resume file and removes it, so it applies to one
// launch only. ok is false when there is none.
func TakeResume(path string) (r Resume, ok bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Resume{}, false, nil
	}
	if err != nil {
		return Resume{}, false, err
	}
	os.Remove(path)
	if err := json.Unmarshal(data, &r); err != nil {
		return Resume{}, false, err
	}
	return r, true, nil
}
