package driver

import (
	"strings"
	"testing"
)

const sysbenchSample = `
[everest-perf] preparing dataset
Creating table 'sbtest1'...
===EVEREST-PERF-RESULT-BEGIN===
sysbench 1.0.20 (using bundled LuaJIT 2.1.0-beta2)

Running the test with following options:
Number of threads: 8

SQL statistics:
    queries performed:
        read:                            173124
        write:                           49464
        other:                           24732
        total:                           247320
    transactions:                        12366  (412.08 per sec.)
    queries:                             247320 (8241.57 per sec.)
    ignored errors:                      2      (0.07 per sec.)
    reconnects:                          0      (0.00 per sec.)

General statistics:
    total time:                          30.0069s
    total number of events:              12366

Latency (ms):
         min:                                    2.87
         avg:                                   19.40
         max:                                  213.34
         95th percentile:                       41.85
         sum:                               239933.99

Threads fairness:
    events (avg/stddev):           1545.7500/24.71
    execution time (avg/stddev):   29.9917/0.00
===EVEREST-PERF-RESULT-END===
Dropping table 'sbtest1'...
`

func TestSysbenchParseOutput(t *testing.T) {
	s := NewSysbench("")
	res, err := s.ParseOutput(sysbenchSample)
	if err != nil {
		t.Fatalf("ParseOutput: %v", err)
	}
	if res.TotalOps != 12366 {
		t.Errorf("TotalOps = %d, want 12366", res.TotalOps)
	}
	if res.ThroughputOPS != 412.08 {
		t.Errorf("ThroughputOPS = %v, want 412.08", res.ThroughputOPS)
	}
	if res.QPS != 8241.57 {
		t.Errorf("QPS = %v, want 8241.57", res.QPS)
	}
	if res.Errors != 2 {
		t.Errorf("Errors = %d, want 2", res.Errors)
	}
	if res.LatencyAvgMs != 19.40 {
		t.Errorf("LatencyAvgMs = %v, want 19.40", res.LatencyAvgMs)
	}
	if res.LatencyP95Ms != 41.85 {
		t.Errorf("LatencyP95Ms = %v, want 41.85", res.LatencyP95Ms)
	}
	if res.LatencyMaxMs != 213.34 {
		t.Errorf("LatencyMaxMs = %v, want 213.34", res.LatencyMaxMs)
	}
}

func TestSysbenchParseOutputNoSummary(t *testing.T) {
	s := NewSysbench("")
	if _, err := s.ParseOutput("connection refused"); err == nil {
		t.Fatal("expected error for output without summary")
	}
}

func TestSysbenchBuildScript(t *testing.T) {
	s := NewSysbench("")
	conn := Connection{Host: "db.ns.svc", Port: 5432, User: "postgres", Password: "secret", Database: "postgres"}
	spec := RunSpec{Threads: 8, DurationSeconds: 60, ReadPercent: 95, WritePercent: 5, Tables: 4, TableSize: 1000}

	script, err := s.BuildScript(EnginePostgreSQL, conn, spec)
	if err != nil {
		t.Fatalf("BuildScript: %v", err)
	}
	for _, want := range []string{
		"--db-driver=pgsql",
		"--pgsql-host=db.ns.svc",
		`--pgsql-password="$DB_PASSWORD"`,
		"--tables=4 --table-size=1000",
		"--threads=8 --time=60",
		"oltp_read_write", // 5% writes -> mixed script
		MarkBegin, MarkEnd,
		"prepare", "cleanup",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "secret") {
		t.Error("plaintext password leaked into script")
	}
	// Regression: $DB_PASSWORD must sit in a shell *function*, not a
	// single-quoted variable - parameter expansion does not happen inside
	// text produced by expanding another variable, so `SB='... $DB_PASSWORD'`
	// sends the literal string to the database.
	if !strings.Contains(script, "sb() {") {
		t.Error("connection args must be wrapped in a shell function")
	}
	if strings.Contains(script, "'sysbench") {
		t.Error("connection args must not be captured in a single-quoted variable")
	}

	// Read-only mix picks the read-only workload.
	spec.ReadPercent, spec.WritePercent = 100, 0
	script, _ = s.BuildScript(EnginePostgreSQL, conn, spec)
	if !strings.Contains(script, "oltp_read_only") {
		t.Error("expected oltp_read_only for 100% reads")
	}

	// SkipPrepare / SkipCleanup drop those phases.
	spec.SkipPrepare, spec.SkipCleanup = true, true
	script, _ = s.BuildScript(EnginePostgreSQL, conn, spec)
	if strings.Contains(script, "prepare") || strings.Contains(script, "cleanup") {
		t.Error("expected no prepare/cleanup phases")
	}

	if _, err := s.BuildScript(EngineMongoDB, conn, spec); err == nil {
		t.Error("expected error for unsupported engine")
	}
}
