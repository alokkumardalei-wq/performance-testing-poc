// Package profile defines Benchmark Workload Profiles: named, curated
// workload shapes users pick from instead of raw benchmarking-tool flags.
// A profile resolves to a driver.RunSpec; the driver translates that into
// tool-specific configuration.
package profile

import (
	"fmt"

	"github.com/openeverest/plugin-performance/backend/internal/driver"
)

// Profile is a named workload shape with sensible defaults. Every field of
// the resolved RunSpec can still be overridden per-run (threads, duration,
// dataset size), so profiles are starting points, not straitjackets.
type Profile struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Spec        driver.RunSpec `json:"spec"`
}

// Overrides carries the per-run knobs a user may tweak on top of a profile.
type Overrides struct {
	Threads         *int              `json:"threads,omitempty"`
	DurationSeconds *int              `json:"durationSeconds,omitempty"`
	Tables          *int              `json:"tables,omitempty"`
	TableSize       *int              `json:"tableSize,omitempty"`
	Records         *int              `json:"records,omitempty"`
	ReadPercent     *int              `json:"readPercent,omitempty"`
	WritePercent    *int              `json:"writePercent,omitempty"`
	SkipPrepare     *bool             `json:"skipPrepare,omitempty"`
	SkipCleanup     *bool             `json:"skipCleanup,omitempty"`
	Extra           map[string]string `json:"extra,omitempty"`
}

var profiles = []Profile{
	{
		Name:        "smoke",
		DisplayName: "Smoke Test",
		Description: "Small dataset, low concurrency, 60 seconds. Quick check that the database accepts load.",
		Spec: driver.RunSpec{
			Profile: "smoke", Threads: 4, DurationSeconds: 60,
			ReadPercent: 70, WritePercent: 30,
			Tables: 2, TableSize: 10000, Records: 10000,
		},
	},
	{
		Name:        "read_heavy",
		DisplayName: "Read Heavy",
		Description: "95% reads / 5% writes, similar to content-serving or catalog workloads.",
		Spec: driver.RunSpec{
			Profile: "read_heavy", Threads: 16, DurationSeconds: 300,
			ReadPercent: 95, WritePercent: 5,
			Tables: 8, TableSize: 100000, Records: 200000,
		},
	},
	{
		Name:        "write_heavy",
		DisplayName: "Write Heavy",
		Description: "10% reads / 90% writes, similar to ingestion or logging workloads.",
		Spec: driver.RunSpec{
			Profile: "write_heavy", Threads: 16, DurationSeconds: 300,
			ReadPercent: 10, WritePercent: 90,
			Tables: 8, TableSize: 100000, Records: 200000,
		},
	},
	{
		Name:        "mixed_oltp",
		DisplayName: "Mixed OLTP",
		Description: "70% reads / 30% writes, a balanced transactional workload.",
		Spec: driver.RunSpec{
			Profile: "mixed_oltp", Threads: 16, DurationSeconds: 300,
			ReadPercent: 70, WritePercent: 30,
			Tables: 8, TableSize: 100000, Records: 200000,
		},
	},
	{
		Name:        "stress",
		DisplayName: "Stress Test",
		Description: "High concurrency against a larger dataset to find the saturation point.",
		Spec: driver.RunSpec{
			Profile: "stress", Threads: 64, DurationSeconds: 600,
			ReadPercent: 70, WritePercent: 30,
			Tables: 16, TableSize: 500000, Records: 1000000,
		},
	},
	{
		Name:        "custom",
		DisplayName: "Custom",
		Description: "Set every parameter yourself, including extra driver-specific flags.",
		Spec: driver.RunSpec{
			Profile: "custom", Threads: 8, DurationSeconds: 120,
			ReadPercent: 70, WritePercent: 30,
			Tables: 4, TableSize: 50000, Records: 100000,
		},
	},
}

// List returns all profiles in a stable order.
func List() []Profile { return profiles }

// Resolve returns the RunSpec for a profile with overrides applied.
func Resolve(name string, o Overrides) (driver.RunSpec, error) {
	var base *Profile
	for i := range profiles {
		if profiles[i].Name == name {
			base = &profiles[i]
			break
		}
	}
	if base == nil {
		return driver.RunSpec{}, fmt.Errorf("unknown profile %q", name)
	}
	spec := base.Spec

	if o.Threads != nil {
		spec.Threads = *o.Threads
	}
	if o.DurationSeconds != nil {
		spec.DurationSeconds = *o.DurationSeconds
	}
	if o.Tables != nil {
		spec.Tables = *o.Tables
	}
	if o.TableSize != nil {
		spec.TableSize = *o.TableSize
	}
	if o.Records != nil {
		spec.Records = *o.Records
	}
	if o.ReadPercent != nil {
		spec.ReadPercent = *o.ReadPercent
	}
	if o.WritePercent != nil {
		spec.WritePercent = *o.WritePercent
	}
	if o.SkipPrepare != nil {
		spec.SkipPrepare = *o.SkipPrepare
	}
	if o.SkipCleanup != nil {
		spec.SkipCleanup = *o.SkipCleanup
	}
	if len(o.Extra) > 0 {
		spec.Extra = o.Extra
	}

	if err := validate(spec); err != nil {
		return driver.RunSpec{}, err
	}
	return spec, nil
}

func validate(s driver.RunSpec) error {
	if s.Threads < 1 || s.Threads > 1024 {
		return fmt.Errorf("threads must be 1-1024, got %d", s.Threads)
	}
	if s.DurationSeconds < 10 || s.DurationSeconds > 24*3600 {
		return fmt.Errorf("durationSeconds must be 10-86400, got %d", s.DurationSeconds)
	}
	if s.ReadPercent < 0 || s.WritePercent < 0 || s.ReadPercent+s.WritePercent != 100 {
		return fmt.Errorf("readPercent+writePercent must sum to 100, got %d+%d", s.ReadPercent, s.WritePercent)
	}
	if s.Tables < 1 || s.TableSize < 1 || s.Records < 1 {
		return fmt.Errorf("dataset sizing must be positive")
	}
	return nil
}
