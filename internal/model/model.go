package model

import "time"

type Check struct {
	NodeID     string    `json:"node_id"`
	CheckedAt  time.Time `json:"checked_at"`
	Available  bool      `json:"available"`
	LatencyMS  int64     `json:"latency_ms"`
	StatusCode int       `json:"status_code,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type NodeStatus struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Address     string     `json:"address"`
	Port        int        `json:"port"`
	Available   *bool      `json:"available"`
	LatencyMS   *int64     `json:"latency_ms"`
	StatusCode  int        `json:"status_code,omitempty"`
	LastChecked *time.Time `json:"last_checked,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type Summary struct {
	Total   int `json:"total"`
	Up      int `json:"up"`
	Down    int `json:"down"`
	Unknown int `json:"unknown"`
}

type SubscriptionStatus struct {
	LastAttempt *time.Time `json:"last_attempt,omitempty"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type Status struct {
	GeneratedAt  time.Time          `json:"generated_at"`
	XrayRunning  bool               `json:"xray_running"`
	Subscription SubscriptionStatus `json:"subscription"`
	Summary      Summary            `json:"summary"`
	Nodes        []NodeStatus       `json:"nodes"`
}
