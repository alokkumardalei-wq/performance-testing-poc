package store

import (
	"errors"
	"testing"
	"time"

	"github.com/openeverest/plugin-performance/backend/internal/driver"
	"k8s.io/utils/ptr"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleRun(id string) *Run {
	return &Run{
		ID:           id,
		CreatedAt:    time.Now().UTC(),
		Namespace:    "default",
		InstanceName: "pg-demo",
		Engine:       "postgresql",
		Driver:       "sysbench",
		Profile:      "smoke",
		Status:       StatusPending,
		Spec:         driver.RunSpec{Profile: "smoke", Threads: 4, DurationSeconds: 60, ReadPercent: 70, WritePercent: 30, Tables: 2, TableSize: 1000, Records: 1000},
	}
}

func TestRoundTrip(t *testing.T) {
	s := newTestStore(t)
	run := sampleRun("run-1")
	if err := s.CreateRun(run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	run.Status = StatusSucceeded
	run.JobName = "perf-run-run1"
	run.Result = &driver.Result{ThroughputOPS: 412.08, LatencyP95Ms: 41.85, TotalOps: 12366, PerOperation: map[string]driver.OpStats{"READ": {OPS: 400, Count: 12000, AvgMs: 1.2, P99Ms: 3.4}}}
	run.Fingerprint = &Fingerprint{Engine: "postgresql", EngineVersion: "16.1", Driver: "sysbench", DriverImage: "img:1", Isolated: ptr.To(true), Spec: run.Spec}
	run.RawOutput = "raw tool output"
	now := time.Now().UTC()
	run.FinishedAt = &now
	if err := s.UpdateRun(run); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}

	got, err := s.GetRun("run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Status != StatusSucceeded || got.Result == nil || got.Fingerprint == nil {
		t.Fatalf("roundtrip lost data: %+v", got)
	}
	if got.Result.ThroughputOPS != 412.08 {
		t.Errorf("result throughput = %v", got.Result.ThroughputOPS)
	}
	if got.Fingerprint.Isolated == nil || !*got.Fingerprint.Isolated {
		t.Error("fingerprint isolation lost")
	}
	if got.Result.PerOperation["READ"].Count != 12000 {
		t.Error("per-op stats lost")
	}
	if got.FinishedAt == nil {
		t.Error("finishedAt lost")
	}

	raw, err := s.GetRawOutput("run-1")
	if err != nil || raw != "raw tool output" {
		t.Errorf("GetRawOutput = %q, %v", raw, err)
	}
}

func TestListFilterAndDelete(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"a", "b"} {
		r := sampleRun(id)
		if err := s.CreateRun(r); err != nil {
			t.Fatal(err)
		}
	}
	other := sampleRun("c")
	other.InstanceName = "mongo-demo"
	if err := s.CreateRun(other); err != nil {
		t.Fatal(err)
	}

	runs, err := s.ListRuns(ListFilter{Namespace: "default", InstanceName: "pg-demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Errorf("filtered list = %d runs, want 2", len(runs))
	}

	all, _ := s.ListRuns(ListFilter{})
	if len(all) != 3 {
		t.Errorf("unfiltered list = %d runs, want 3", len(all))
	}

	if err := s.DeleteRun("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRun("a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRun after delete: %v, want ErrNotFound", err)
	}
	if err := s.DeleteRun("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteRun missing: %v, want ErrNotFound", err)
	}
}

func TestUpdateMissing(t *testing.T) {
	s := newTestStore(t)
	r := sampleRun("ghost")
	if err := s.UpdateRun(r); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateRun missing: %v, want ErrNotFound", err)
	}
}
