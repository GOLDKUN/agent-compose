package schedulers

import "time"

type SchedulerRunPageFilter struct {
	SchedulerIDs    []string
	RequireTrigger  bool
	TriggerID       string
	Status          string
	BeforeStartedAt time.Time
	BeforeLoaderID  string
	BeforeRunID     string
	Offset          int
	Limit           int
}

type SchedulerRunKey struct {
	SchedulerID string
	RunID       string
}

type SchedulerEventPageFilter struct {
	SchedulerIDs     []string
	RequireTrigger   bool
	TriggerID        string
	RunID            string
	BeforeCreatedAt  time.Time
	BeforeLoaderID   string
	BeforeEventID    string
	AfterCreatedAt   time.Time
	AfterLoaderID    string
	AfterEventID     string
	FromCreatedAt    time.Time
	FromLoaderID     string
	FromEventID      string
	ThroughCreatedAt time.Time
	ThroughLoaderID  string
	ThroughEventID   string
	Ascending        bool
	Offset           int
	Limit            int
}
