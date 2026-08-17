// Package driver defines the benchmark-driver abstraction.
//
// A Driver knows how to turn a (connection, workload spec) pair into a shell
// script that runs inside a short-lived Kubernetes Job container, and how to
// parse the tool's stdout back into a normalized Result. The plugin core never
// talks to sysbench/go-ycsb directly - only through this interface - so new
// tools (pgbench, HammerDB, ...) can be added without touching the runner, the
// store, the API, or the UI.
package driver

import "fmt"

// Engine identifies a database engine type managed by OpenEverest.
type Engine string

const (
	EnginePostgreSQL Engine = "postgresql"
	EngineMySQL      Engine = "mysql"
	EngineMongoDB    Engine = "mongodb"
)

// Connection holds everything a driver needs to reach the target database.
// Credentials are fetched at run start and injected into the Job via a
// short-lived Secret; they are never persisted in the run record.
type Connection struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"-"`
	Database string `json:"database"`
	// TLS indicates the server requires TLS (drivers add the right flag).
	TLS bool `json:"tls,omitempty"`
}

// RunSpec is the engine-independent description of a workload. Profiles are
// resolved into a RunSpec before the driver is invoked, so drivers only see
// concrete numbers.
type RunSpec struct {
	// Profile is kept for reference/fingerprinting only.
	Profile string `json:"profile"`

	Threads         int `json:"threads"`
	DurationSeconds int `json:"durationSeconds"`

	// ReadPercent + WritePercent describe the operation mix (0-100).
	ReadPercent  int `json:"readPercent"`
	WritePercent int `json:"writePercent"`

	// Dataset sizing.
	Tables    int `json:"tables"`    // relational: number of tables
	TableSize int `json:"tableSize"` // relational: rows per table
	Records   int `json:"records"`   // document stores: number of documents

	// SkipPrepare reuses an existing dataset (repeat runs against same DB).
	SkipPrepare bool `json:"skipPrepare,omitempty"`
	// SkipCleanup leaves the dataset in place after the run.
	SkipCleanup bool `json:"skipCleanup,omitempty"`

	// Extra is a driver-specific escape hatch for the "custom" profile.
	// Keys/values are passed through to the tool as CLI flags.
	Extra map[string]string `json:"extra,omitempty"`
}

// Result is the normalized outcome of a benchmark run. Every driver maps its
// tool's output onto this schema; the raw output is retained separately as an
// artifact so nothing is lost in translation.
type Result struct {
	// ThroughputOPS is total operations (or transactions) per second.
	ThroughputOPS float64 `json:"throughputOps"`
	// QPS is queries per second where the tool distinguishes it (sysbench).
	QPS float64 `json:"qps,omitempty"`

	LatencyAvgMs float64 `json:"latencyAvgMs"`
	LatencyP95Ms float64 `json:"latencyP95Ms"`
	LatencyP99Ms float64 `json:"latencyP99Ms,omitempty"`
	LatencyMaxMs float64 `json:"latencyMaxMs,omitempty"`

	TotalOps int64 `json:"totalOps"`
	Errors   int64 `json:"errors"`

	// PerOperation breaks throughput/latency down by operation type where the
	// tool reports it (e.g. go-ycsb READ vs UPDATE).
	PerOperation map[string]OpStats `json:"perOperation,omitempty"`
}

// OpStats holds per-operation-type metrics (go-ycsb READ/UPDATE/INSERT...).
type OpStats struct {
	OPS       float64 `json:"ops"`
	Count     int64   `json:"count"`
	AvgMs     float64 `json:"avgMs"`
	P99Ms     float64 `json:"p99Ms"`
	ErrorRate float64 `json:"errorRate,omitempty"`
}

// Driver is the narrow contract every benchmarking tool implements.
type Driver interface {
	// Name is the stable identifier stored with each run (e.g. "sysbench").
	Name() string
	// Supports reports whether this driver can benchmark the given engine.
	Supports(engine Engine) bool
	// DefaultImage is the container image used for the benchmark Job. It can
	// be overridden per-run and via chart values (air-gapped installs).
	DefaultImage() string
	// BuildScript renders the shell script executed in the Job container.
	// The script must exit non-zero on failure and print the tool's summary
	// to stdout between the markers emitted by MarkBegin/MarkEnd.
	BuildScript(engine Engine, conn Connection, spec RunSpec) (string, error)
	// ParseOutput extracts a normalized Result from the container log.
	ParseOutput(log string) (*Result, error)
}

// Output markers let ParseOutput find the measurement section of the log even
// when prepare/load phases print noise before it.
const (
	MarkBegin = "===EVEREST-PERF-RESULT-BEGIN==="
	MarkEnd   = "===EVEREST-PERF-RESULT-END==="
)

// Registry maps driver names to implementations.
type Registry struct {
	drivers map[string]Driver
}

func NewRegistry(drivers ...Driver) *Registry {
	r := &Registry{drivers: map[string]Driver{}}
	for _, d := range drivers {
		r.drivers[d.Name()] = d
	}
	return r
}

// Get returns the named driver, or an error listing what is available.
func (r *Registry) Get(name string) (Driver, error) {
	if d, ok := r.drivers[name]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("unknown driver %q (available: %v)", name, r.Names())
}

// ForEngine picks the preferred driver for an engine when the caller did not
// specify one: sysbench for relational engines, go-ycsb otherwise.
func (r *Registry) ForEngine(engine Engine) (Driver, error) {
	if d, ok := r.drivers["sysbench"]; ok && d.Supports(engine) {
		return d, nil
	}
	for _, d := range r.drivers {
		if d.Supports(engine) {
			return d, nil
		}
	}
	return nil, fmt.Errorf("no driver supports engine %q", engine)
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.drivers))
	for n := range r.drivers {
		names = append(names, n)
	}
	return names
}
