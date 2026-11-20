package domain

import (
	"fmt"
	"math"
	"unicode/utf8"
)

// NoteColor names a colour from the fixed sticky-note palette. The editor maps
// each name to theme tokens; the domain only knows the names.
type NoteColor string

// The sticky-note palette. The empty value means NoteColorYellow.
const (
	NoteColorYellow NoteColor = "yellow"
	NoteColorBlue   NoteColor = "blue"
	NoteColorGreen  NoteColor = "green"
	NoteColorRed    NoteColor = "red"
	NoteColorPurple NoteColor = "purple"
	NoteColorGray   NoteColor = "gray"
)

// NoteColors lists the palette in display order.
func NoteColors() []NoteColor {
	return []NoteColor{NoteColorYellow, NoteColorBlue, NoteColorGreen, NoteColorRed, NoteColorPurple, NoteColorGray}
}

// Valid reports whether c is in the palette (the empty value counts as yellow).
func (c NoteColor) Valid() bool {
	if c == "" {
		return true
	}
	for _, p := range NoteColors() {
		if c == p {
			return true
		}
	}
	return false
}

// NoteID identifies a note within one workflow. Clients choose it.
type NoteID string

// maxNoteIDLength bounds the length of a NoteID.
const maxNoteIDLength = 64

// Validate reports whether the ID is non-empty, short and made of letters,
// digits, '-' and '_'.
func (id NoteID) Validate() error {
	if id == "" {
		return fmt.Errorf("must not be empty")
	}
	if len(id) > maxNoteIDLength {
		return fmt.Errorf("must be at most %d characters", maxNoteIDLength)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("may only contain letters, digits, '-' and '_'")
		}
	}
	return nil
}

// Size is a width and height on the canvas.
type Size struct {
	Width, Height float64
}

// Note is a sticky note on the canvas: an annotation for people. The engine,
// node registry and run data never see notes.
type Note struct {
	ID       NoteID
	Text     string // markdown
	Position Position
	Size     Size
	Color    NoteColor
}

// NoteLimits bound the notes of one workflow. A zero field means no limit.
type NoteLimits struct {
	MaxNotes     int
	MaxTextBytes int
}

func (n *Note) validate(v *validator, path string) {
	if err := n.ID.Validate(); err != nil {
		v.add(path+".id", err.Error())
	}
	if !n.Color.Valid() {
		v.add(path+".color", fmt.Sprintf("unknown colour %q", n.Color))
	}
	if !utf8.ValidString(n.Text) {
		v.add(path+".text", "must be valid UTF-8")
	}
	if !finite(n.Position.X) || !finite(n.Position.Y) {
		v.add(path+".position", "must be finite numbers")
	}
	if !finite(n.Size.Width) || !finite(n.Size.Height) || n.Size.Width <= 0 || n.Size.Height <= 0 {
		v.add(path+".size", "width and height must be positive")
	}
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// ValidateNoteLimits checks the notes against the configured limits. The
// result is nil or a *ValidationError.
func (w *Workflow) ValidateNoteLimits(l NoteLimits) error {
	v := &validator{}
	if l.MaxNotes > 0 && len(w.Notes) > l.MaxNotes {
		v.add("notes", fmt.Sprintf("a workflow holds at most %d notes, got %d", l.MaxNotes, len(w.Notes)))
	}
	if l.MaxTextBytes > 0 {
		for i := range w.Notes {
			if len(w.Notes[i].Text) > l.MaxTextBytes {
				v.add(fmt.Sprintf("notes[%d].text", i), fmt.Sprintf("must be at most %d bytes", l.MaxTextBytes))
			}
		}
	}
	return v.err()
}
