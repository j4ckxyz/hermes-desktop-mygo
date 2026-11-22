package theme

import (
	"embed"
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
		for _, e := range entries {
			name := e.Name()
			data, err := fontFiles.ReadFile("fonts/" + name)
			if err != nil {
				registerErr = err
				return
			}
			family := "Nunito"
			if strings.HasPrefix(name, "JetBrainsMono") {
				family = "JetBrains Mono"
			}
			if err := ui.RegisterFont(data, family); err != nil {
				registerErr = fmt.Errorf("register %s: %w", name, err)
				return
			}
		}
	})
	return registerErr
}
