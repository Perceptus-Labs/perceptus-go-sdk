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
	Timestamp          time.Time
}

type EnvironmentContext struct {
	ID             string            `json:"id" optional:"true"`
	SessionID      string            `json:"session_id" optional:"true"`
	Timestamp      time.Time         `json:"timestamp" optional:"true"`
	ImageData      []byte            `json:"image_data" optional:"true"`
	Overview       string            `json:"overview" optional:"true"`
	KeyElements    []string          `json:"key_elements" optional:"true"`
	Layout         string            `json:"layout" optional:"true"`
	Activities     []string          `json:"activities" optional:"true"`
	AdditionalInfo map[string]string `json:"additional_info" optional:"true"`
}
