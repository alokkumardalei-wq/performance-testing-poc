package driver

import (
	"strings"
	"testing"
)

const ycsbSample = `
[everest-perf] loading dataset
INSERT - Takes(s): 9.7, Count: 100000, OPS: 10309.2, Avg(us): 951, Min(us): 401, Max(us): 41023, 50th(us): 810, 90th(us): 1400, 95th(us): 1749, 99th(us): 3200, 99.9th(us): 12000, 99.99th(us): 32000
Load finished, takes 9.723412s
===EVEREST-PERF-RESULT-BEGIN===
READ   - Takes(s): 10.0, Count: 18102, OPS: 1810.9, Avg(us): 1500, Min(us): 500, Max(us): 30000, 50th(us): 1300, 90th(us): 2400, 95th(us): 2900, 99th(us): 5100, 99.9th(us): 12000, 99.99th(us): 29000
UPDATE - Takes(s): 10.0, Count: 981, OPS: 98.2, Avg(us): 2100, Min(us): 700, Max(us): 25000, 50th(us): 1800, 90th(us): 3300, 95th(us): 4000, 99th(us): 7200, 99.9th(us): 15000, 99.99th(us): 25000
Run finished, takes 1m0.01s
READ   - Takes(s): 60.0, Count: 109238, OPS: 1820.5, Avg(us): 1450, Min(us): 480, Max(us): 31000, 50th(us): 1280, 90th(us): 2350, 95th(us): 2870, 99th(us): 5000, 99.9th(us): 11800, 99.99th(us): 28000
READ_ERROR - Takes(s): 60.0, Count: 12, OPS: 0.2, Avg(us): 900, Min(us): 500, Max(us): 2000, 50th(us): 800, 90th(us): 1500, 95th(us): 1700, 99th(us): 2000, 99.9th(us): 2000, 99.99th(us): 2000
UPDATE - Takes(s): 60.0, Count: 5763, OPS: 96.0, Avg(us): 2050, Min(us): 690, Max(us): 26000, 50th(us): 1750, 90th(us): 3250, 95th(us): 3900, 99th(us): 7000, 99.9th(us): 14800, 99.99th(us): 26000
===EVEREST-PERF-RESULT-END===
`

func TestYCSBParseOutput(t *testing.T) {
	y := NewYCSB("")
	res, err := y.ParseOutput(ycsbSample)
	if err != nil {
		t.Fatalf("ParseOutput: %v", err)
	}
	// Only the post-"Run finished" summary counts, not progress lines or load.
	if got, want := res.TotalOps, int64(109238+5763); got != want {
		t.Errorf("TotalOps = %d, want %d", got, want)
	}
	if got := res.ThroughputOPS; got < 1916 || got > 1917 {
		t.Errorf("ThroughputOPS = %v, want ~1916.5", got)
	}
	if res.Errors != 12 {
		t.Errorf("Errors = %d, want 12", res.Errors)
	}
	read, ok := res.PerOperation["READ"]
	if !ok {
		t.Fatal("missing READ op stats")
	}
	if read.Count != 109238 {
		t.Errorf("READ count = %d", read.Count)
	}
	if read.AvgMs != 1.45 {
		t.Errorf("READ avgMs = %v, want 1.45", read.AvgMs)
	}
	if read.P99Ms != 5.0 {
		t.Errorf("READ p99Ms = %v, want 5.0", read.P99Ms)
	}
	if _, ok := res.PerOperation["READ_ERROR"]; ok {
		t.Error("error pseudo-op should not appear in PerOperation")
	}
	if res.LatencyAvgMs <= 0 || res.LatencyP99Ms <= 0 {
		t.Error("aggregated latencies not computed")
	}
}

func TestYCSBBuildScript(t *testing.T) {
	y := NewYCSB("")
	conn := Connection{Host: "mongo.ns.svc", Port: 27017, User: "admin", Password: "s3cr3t"}
	spec := RunSpec{Threads: 16, DurationSeconds: 120, ReadPercent: 95, WritePercent: 5, Records: 50000, Tables: 1, TableSize: 1}

	script, err := y.BuildScript(EngineMongoDB, conn, spec)
	if err != nil {
		t.Fatalf("BuildScript: %v", err)
	}
	for _, want := range []string{
		"go-ycsb load mongodb",
		"go-ycsb run mongodb",
		`-p mongodb.url="mongodb://mongo.ns.svc:27017/ycsb"`,
		"-p mongodb.username=admin",
		`-p mongodb.password="$DB_PASSWORD"`,
		"-p recordcount=50000",
		"-p maxexecutiontime=120",
		"-p threadcount=16",
		"-p readproportion=0.95",
		"-p updateproportion=0.05",
		MarkBegin, MarkEnd,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "s3cr3t") {
		t.Error("plaintext password leaked into script")
	}
	// Regression: go-ycsb does not enforce maxexecutiontime itself, so the
	// script must carry the SIGINT watchdog that bounds the run duration.
	for _, want := range []string{"YPID=$!", "sleep 120; kill -INT $YPID", "wait $YPID"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing watchdog fragment %q", want)
		}
	}

	// Engine coverage: relational engines are also supported (cross-engine driver).
	if _, err := y.BuildScript(EnginePostgreSQL, conn, spec); err != nil {
		t.Errorf("postgresql: %v", err)
	}
	if _, err := y.BuildScript(EngineMySQL, conn, spec); err != nil {
		t.Errorf("mysql: %v", err)
	}
}
