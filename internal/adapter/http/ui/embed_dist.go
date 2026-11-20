//go:build webui

package ui

import (
	"embed"
	"fmt"
	"io/fs"
)

// Built by `make web`, which copies web/dist here. Absent without that step,
// so this file compiles only with the webui build tag.
//
//go:embed all:dist
var distFS embed.FS

// Embedded reports whether the real editor is compiled in.
const Embedded = true

func assets() (fs.FS, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("opening embedded dist: %w", err)
	}
	return sub, nil
}
