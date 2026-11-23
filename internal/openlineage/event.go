// Package openlineage defines the events published by Baleia.
package openlineage

import "time"

// Dataset identifies a dataset within its source namespace.
type Dataset struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// RunEvent is an OpenLineage run event.
type RunEvent struct {
	EventType string    `json:"eventType"`
	EventTime time.Time `json:"eventTime"`
	Run       Run       `json:"run"`
	Job       Job       `json:"job"`
	Inputs    []Dataset `json:"inputs,omitempty"`
	Outputs   []Dataset `json:"outputs,omitempty"`
	Producer  string    `json:"producer"`
	SchemaURL string    `json:"schemaURL"`
}

// Run identifies an execution.
type Run struct {
	RunID string `json:"runId"`
}

// Job identifies the operation that produced the event.
type Job struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}
