//go:build !webui

package ui

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed placeholder
var placeholderFS embed.FS

// Embedded reports whether the real editor is compiled in.
const Embedded = false

func assets() (fs.FS, error) {
	sub, err := fs.Sub(placeholderFS, "placeholder")
	if err != nil {
		return nil, fmt.Errorf("opening embedded placeholder: %w", err)
	}
	return sub, nil
}
