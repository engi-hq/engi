package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits of tag names and variable keys.
const (
	MaxTagLength         = 50
	MaxVariableKeyLength = 100
)

// Tag labels workflows. Its name is what workflows reference.
type Tag struct {
	ID        TagID
	Name      string
	CreatedAt time.Time
}

// ValidateTagName checks a (trimmed) tag name.
func ValidateTagName(name string) error {
	switch {
	case name == "":
		return &ValidationError{Issues: []Issue{{Path: "name", Message: "must not be empty"}}}
	case utf8.RuneCountInString(name) > MaxTagLength:
		return &ValidationError{Issues: []Issue{{Path: "name", Message: "is too long"}}}
	case strings.ContainsFunc(name, func(r rune) bool { return r < ' ' || r == 0x7f }):
		return &ValidationError{Issues: []Issue{{Path: "name", Message: "must not contain control characters"}}}
	}
	return nil
}

// Variable is an instance-wide key/value read as $vars.KEY. Value holds the
// plaintext when Secret is false and the ciphertext (sealed under KeyID) when
// it is true.
type Variable struct {
	ID        VariableID
	Key       string
	Secret    bool
	KeyID     string
	Value     []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

var variableKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateVariableKey checks that key is an identifier usable as $vars.KEY.
func ValidateVariableKey(key string) error {
	if len(key) > MaxVariableKeyLength || !variableKeyPattern.MatchString(key) {
		return &ValidationError{Issues: []Issue{{Path: "key", Message: "must be an identifier (letters, digits, underscore; not starting with a digit)"}}}
	}
	return nil
}
