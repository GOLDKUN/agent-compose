package schedulers

import "time"

type SchedulerRunPageFilter struct {
	SchedulerIDs      []string
	RequireTrigger    bool
	TriggerID         string
	Status            string
	BeforeStartedAt   time.Time
	BeforeSchedulerID string
	BeforeRunID       string
	Offset            int
	Limit             int
}

type SchedulerRunKey struct {
	SchedulerID string
	RunID       string
}

type SchedulerEventPageFilter struct {
	SchedulerIDs       []string
	RequireTrigger     bool
	TriggerID          string
	RunID              string
	BeforeCreatedAt    time.Time
	BeforeSchedulerID  string
	BeforeEventID      string
	AfterCreatedAt     time.Time
	AfterSchedulerID   string
	AfterEventID       string
	FromCreatedAt      time.Time
	FromSchedulerID    string
	FromEventID        string
	ThroughCreatedAt   time.Time
	ThroughSchedulerID string
	ThroughEventID     string
	Ascending          bool
	Offset             int
	Limit              int
}
