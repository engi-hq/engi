package kafka

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Trigger modes.
const (
	modePerMessage = "perMessage"
	modeBatch      = "batch"
	modeOrdered    = "ordered"
)

// Start offsets for a group without committed offsets.
const (
	offsetEarliest = "earliest"
	offsetLatest   = "latest"
)

// Value formats.
const (
	formatJSON = "json"
	formatText = "text"
)

// Defaults of the trigger parameters.
const (
	defaultBatchSize    = 100
	defaultBatchWaitMs  = 1000
	defaultGroupPrefix  = "engi"
	defaultValueFormat  = formatJSON
	defaultStartOffset  = offsetLatest
	defaultTriggerModes = modePerMessage
)

// TriggerLimits are the host settings the trigger obeys (the top-level kafka config section).
type TriggerLimits struct {
	BatchMaxMessages int
	BatchMaxWait     time.Duration
	SessionTimeout   time.Duration
	RebalanceTimeout time.Duration
	MaxPollRecords   int
	// PollInterval is how often the consume loop wakes to check backpressure
	// and open batches while no records arrive.
	PollInterval time.Duration
}

// settings are the validated parameters of one Kafka trigger node.
type settings struct {
	topics      []string
	group       string
	startOffset string
	mode        string
	batchSize   int
	batchWait   time.Duration
	format      string
	deadLetter  string
}

// parseSettings validates the saved parameters. workflowID and node build the
// default consumer group.
func parseSettings(params map[string]any, lim TriggerLimits, workflowID, node string) (settings, error) {
	s := settings{
		topics:      splitList(params["topics"]),
		group:       strings.TrimSpace(paramString(params, "groupId", "")),
		startOffset: paramString(params, "startOffset", defaultStartOffset),
		mode:        paramString(params, "mode", defaultTriggerModes),
		format:      paramString(params, "valueFormat", defaultValueFormat),
		deadLetter:  strings.TrimSpace(paramString(params, "deadLetterTopic", "")),
	}
	var errs []error
	if len(s.topics) == 0 {
		errs = append(errs, errors.New("topics must name at least one topic"))
	}
	if s.group == "" {
		s.group = defaultGroup(workflowID, node)
	}
	switch s.startOffset {
	case offsetEarliest, offsetLatest:
	default:
		errs = append(errs, fmt.Errorf("startOffset %q is not %s or %s", s.startOffset, offsetEarliest, offsetLatest))
	}
	switch s.format {
	case formatJSON, formatText:
	default:
		errs = append(errs, fmt.Errorf("valueFormat %q is not %s or %s", s.format, formatJSON, formatText))
	}
	switch s.mode {
	case modePerMessage, modeOrdered:
	case modeBatch:
		errs = append(errs, s.parseBatch(params, lim)...)
	default:
		errs = append(errs, fmt.Errorf("mode %q is not %s, %s or %s", s.mode, modePerMessage, modeBatch, modeOrdered))
	}
	if s.deadLetter != "" {
		for _, t := range s.topics {
			if t == s.deadLetter {
				errs = append(errs, errors.New("deadLetterTopic must differ from the consumed topics"))
				break
			}
		}
	}
	if len(errs) > 0 {
		return settings{}, errors.Join(errs...)
	}
	return s, nil
}

func (s *settings) parseBatch(params map[string]any, lim TriggerLimits) []error {
	var errs []error
	size, err := paramInt(params, "batchSize", defaultBatchSize)
	if err != nil {
		errs = append(errs, err)
	}
	waitMs, err := paramInt(params, "batchWaitMs", defaultBatchWaitMs)
	if err != nil {
		errs = append(errs, err)
	}
	if err == nil && (size < 1 || size > lim.BatchMaxMessages) {
		errs = append(errs, fmt.Errorf("batchSize must be between 1 and %d (kafka.batchMaxMessages)", lim.BatchMaxMessages))
	}
	s.batchSize = size
	s.batchWait = time.Duration(waitMs) * time.Millisecond
	if err == nil && (s.batchWait < time.Millisecond || s.batchWait > lim.BatchMaxWait) {
		errs = append(errs, fmt.Errorf("batchWaitMs must be between 1 and %d (kafka.batchMaxWait)", lim.BatchMaxWait.Milliseconds()))
	}
	return errs
}

// defaultGroup is engi-<workflowId>-<node>, with blanks in the node name
// replaced so the id stays easy to type in Kafka tooling.
func defaultGroup(workflowID, node string) string {
	return defaultGroupPrefix + "-" + workflowID + "-" + strings.Join(strings.Fields(node), "-")
}
