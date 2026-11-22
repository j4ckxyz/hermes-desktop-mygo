package theme

import (
	"embed"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

//go:embed fonts/*.ttf
var fontFiles embed.FS

var registerOnce sync.Once
var registerErr error

// RegisterFonts registers the bundled Nunito and JetBrains Mono faces under
// their family names. It is safe to call more than once and from tests.
func RegisterFonts() error {
	registerOnce.Do(func() {
		entries, err := fontFiles.ReadDir("fonts")
		if err != nil {
			registerErr = err
			return
		}
		// Each face on its own: one that fails leaves the others in use.
		var errs []error
		for _, e := range entries {
			name := e.Name()
			data, err := fontFiles.ReadFile("fonts/" + name)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			family := "Nunito"
			if strings.HasPrefix(name, "JetBrainsMono") {
				family = "JetBrains Mono"
			}
			if err := ui.RegisterFont(data, family); err != nil {
				errs = append(errs, fmt.Errorf("register %s: %w", name, err))
			}
		}
		registerErr = errors.Join(errs...)
	})
	return registerErr
}
