package config

import "errors"

// WorkflowConfig bounds what a saved workflow definition may hold.
type WorkflowConfig struct {
	// MaxNotes caps the sticky notes of one workflow.
	MaxNotes int `yaml:"maxNotes"`
	// MaxNoteTextBytes caps the markdown text of one sticky note.
	MaxNoteTextBytes int `yaml:"maxNoteTextBytes"`
}

func defaultWorkflow() WorkflowConfig {
	return WorkflowConfig{MaxNotes: 200, MaxNoteTextBytes: 16 << 10}
}

// Validate reports every problem with the workflow limits.
func (c WorkflowConfig) Validate() error {
	var errs []error
	errs = appendPositive(errs, "workflow.maxNotes", int64(c.MaxNotes))
	errs = appendPositive(errs, "workflow.maxNoteTextBytes", int64(c.MaxNoteTextBytes))
	return errors.Join(errs...)
}

func workflowSettings() []setting {
	return []setting{
		{"ENGI_WORKFLOW_MAX_NOTES", "workflow-max-notes", "most sticky notes per workflow",
			intSetting(func(c *Config) *int { return &c.Workflow.MaxNotes })},
		{"ENGI_WORKFLOW_MAX_NOTE_TEXT_BYTES", "workflow-max-note-text-bytes", "largest sticky note text in bytes",
			intSetting(func(c *Config) *int { return &c.Workflow.MaxNoteTextBytes })},
	}
}
