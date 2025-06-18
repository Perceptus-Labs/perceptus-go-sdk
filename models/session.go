package models

import (
	"time"
)

const (
	SESSION_END = "<SESSION_END>"
)

type IntentionResult struct {
	HasClearIntention  bool
	IntentionType      string
	Description        string
	Confidence         float64
	EnvironmentContext string
	TranscriptAnalysis string
	Timestamp          time.Time
}

type EnvironmentContext struct {
	ID          string
	SessionID   string
	Description string
	Objects     []string
	Timestamp   time.Time
	ImageData   []byte // Optional: store the image data
}

type VideoAnalysis struct {
	SessionID          string
	Description        string
	KeyObjects         []string
	EnvironmentSummary string
	Timestamp          time.Time
}
