package store

import (
	"time"

	"github.com/openeverest/plugin-performance/backend/internal/driver"
)

// RunStatus is the lifecycle of a benchmark run.
type RunStatus string

const (
	StatusPending   RunStatus = "pending"   // accepted, Job not yet created
	StatusRunning   RunStatus = "running"   // Job active (includes prepare phase)
	StatusSucceeded RunStatus = "succeeded" // Job completed, results parsed
	StatusFailed    RunStatus = "failed"
	StatusCanceled  RunStatus = "canceled"
)

// Fingerprint captures the conditions a result was measured under. Two runs
// are only honestly comparable when their fingerprints match; the UI and CLI
// surface any differences instead of silently drawing a line between numbers
// measured under different conditions.
type Fingerprint struct {
	Engine        string `json:"engine"`
	EngineVersion string `json:"engineVersion,omitempty"`
	Replicas      int32  `json:"replicas,omitempty"`
	CPULimit      string `json:"cpuLimit,omitempty"`
	MemoryLimit   string `json:"memoryLimit,omitempty"`
	StorageClass  string `json:"storageClass,omitempty"`
	StorageSize   string `json:"storageSize,omitempty"`

	Driver      string `json:"driver"`
	DriverImage string `json:"driverImage"`

	// Isolation records whether the load generator actually ran on a
	// different node than the database. A benchmark sharing CPU with the
	// process it measures produces a number not worth trusting.
	GeneratorNode string `json:"generatorNode,omitempty"`
	DatabaseNodes []string `json:"databaseNodes,omitempty"`
	Isolated      *bool  `json:"isolated,omitempty"`

	Spec driver.RunSpec `json:"spec"`
}

// Run is one benchmark execution against one database instance.
type Run struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`

	Namespace    string `json:"namespace"`
	InstanceName string `json:"instanceName"`
	Engine       string `json:"engine"`
	Driver       string `json:"driver"`
	Profile      string `json:"profile"`

	Status  RunStatus `json:"status"`
	Message string    `json:"message,omitempty"` // failure reason / progress note
	JobName string    `json:"jobName,omitempty"`

	Spec        driver.RunSpec `json:"spec"`
	Result      *driver.Result `json:"result,omitempty"`
	Fingerprint *Fingerprint   `json:"fingerprint,omitempty"`

	// RawOutput is the tool's stdout (truncated to a sane size) kept as the
	// artifact behind the normalized result.
	RawOutput string `json:"-"`
}

// ListFilter narrows ListRuns.
type ListFilter struct {
	Namespace    string
	InstanceName string
	Limit        int
}

// Store is the persistence contract. SQLite backs it today; the interface is
// the migration path to external PostgreSQL or a plugin CRD later without
// touching the runner, API, or UI.
type Store interface {
	CreateRun(r *Run) error
	UpdateRun(r *Run) error
	GetRun(id string) (*Run, error)
	GetRawOutput(id string) (string, error)
	ListRuns(f ListFilter) ([]*Run, error)
	DeleteRun(id string) error
	Close() error
}
