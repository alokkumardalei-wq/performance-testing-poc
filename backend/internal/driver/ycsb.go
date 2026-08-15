package driver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// YCSB drives go-ycsb (https://github.com/pingcap/go-ycsb), a single Go binary
// that speaks MongoDB, MySQL and PostgreSQL. It is the cross-engine driver:
// one implementation satisfies the relational + non-relational requirement,
// and having a second driver alongside sysbench keeps the Driver interface
// honest.
type YCSB struct {
	Image string
}

func NewYCSB(image string) *YCSB {
	if image == "" {
		// Built from Dockerfile.ycsb in this repo; overridable per-run and
		// via chart values for air-gapped installs.
		image = "ghcr.io/openeverest/perf-driver-ycsb:0.1.0"
	}
	return &YCSB{Image: image}
}

func (y *YCSB) Name() string        { return "go-ycsb" }
func (y *YCSB) DefaultImage() string { return y.Image }

func (y *YCSB) Supports(engine Engine) bool {
	switch engine {
	case EngineMongoDB, EngineMySQL, EnginePostgreSQL:
		return true
	}
	return false
}

func (y *YCSB) BuildScript(engine Engine, conn Connection, spec RunSpec) (string, error) {
	var dbName, connProps string
	switch engine {
	case EngineMongoDB:
		dbName = "mongodb"
		// Credentials go through dedicated properties, not the URL, so
		// passwords never need URL-encoding.
		connProps = fmt.Sprintf(
			`-p mongodb.url="mongodb://%s:%d/%s" -p mongodb.authdb=admin -p mongodb.username=%s -p mongodb.password="$DB_PASSWORD"`,
			conn.Host, conn.Port, orDefault(conn.Database, "ycsb"), conn.User)
	case EngineMySQL:
		dbName = "mysql"
		connProps = fmt.Sprintf(
			`-p mysql.host=%s -p mysql.port=%d -p mysql.user=%s -p mysql.password="$DB_PASSWORD" -p mysql.db=%s`,
			conn.Host, conn.Port, conn.User, orDefault(conn.Database, "ycsb"))
	case EnginePostgreSQL:
		dbName = "pg"
		connProps = fmt.Sprintf(
			`-p pg.host=%s -p pg.port=%d -p pg.user=%s -p pg.password="$DB_PASSWORD" -p pg.db=%s`,
			conn.Host, conn.Port, conn.User, orDefault(conn.Database, "ycsb"))
	default:
		return "", fmt.Errorf("go-ycsb does not support engine %q", engine)
	}

	read := float64(spec.ReadPercent) / 100
	write := float64(spec.WritePercent) / 100
	records := spec.Records
	if records == 0 {
		records = 100000
	}
	workloadProps := fmt.Sprintf(
		"-p recordcount=%d -p operationcount=2000000000 -p maxexecutiontime=%d "+
			"-p threadcount=%d -p readproportion=%.2f -p updateproportion=%.2f "+
			"-p insertproportion=0 -p scanproportion=0 -p requestdistribution=uniform",
		records, spec.DurationSeconds, spec.Threads, read, write)

	extra := ""
	for k, v := range spec.Extra {
		extra += fmt.Sprintf(" -p %s=%s", k, v)
	}

	var b strings.Builder
	b.WriteString("set -e\n")
	if !spec.SkipPrepare {
		b.WriteString("echo '[everest-perf] loading dataset'\n")
		b.WriteString(fmt.Sprintf("go-ycsb load %s -P /workloads/workloada %s -p recordcount=%d -p threadcount=%d%s\n",
			dbName, connProps, records, spec.Threads, extra))
	}
	// go-ycsb declares a maxexecutiontime property but its client never
	// enforces it (verified against master: the worker loop only checks
	// operationcount / context cancellation). Bound the run ourselves with a
	// SIGINT watchdog — go-ycsb traps INT, cancels its context, and still
	// prints the final summary, exiting 0.
	b.WriteString(fmt.Sprintf("echo '%s'\n", MarkBegin))
	b.WriteString(fmt.Sprintf("go-ycsb run %s -P /workloads/workloada %s %s%s &\n",
		dbName, connProps, workloadProps, extra))
	b.WriteString("YPID=$!\n")
	b.WriteString(fmt.Sprintf("( sleep %d; kill -INT $YPID 2>/dev/null ) &\n", spec.DurationSeconds))
	b.WriteString("WPID=$!\n")
	b.WriteString("wait $YPID\n")
	b.WriteString("kill $WPID 2>/dev/null || true\n")
	b.WriteString(fmt.Sprintf("echo '%s'\n", MarkEnd))
	return b.String(), nil
}

// go-ycsb summary lines look like:
//   READ   - Takes(s): 60.0, Count: 123456, OPS: 2057.6, Avg(us): 485, Min(us): 120,
//            Max(us): 10234, 50th(us): 430, 90th(us): 720, 95th(us): 890, 99th(us): 1500, ...
var reYCSBLine = regexp.MustCompile(`(?m)^([A-Z_]+)\s+- Takes\(s\): [\d.]+, Count: (\d+), OPS: ([\d.]+), Avg\(us\): (\d+),.*?99th\(us\): (\d+)`)
var reYCSB95 = regexp.MustCompile(`95th\(us\): (\d+)`)

func (y *YCSB) ParseOutput(log string) (*Result, error) {
	section := extractSection(log)
	// go-ycsb prints periodic progress lines in the same format as the final
	// summary; the final block follows "Run finished". Parse only after it,
	// falling back to the last occurrences otherwise.
	if idx := strings.LastIndex(section, "Run finished"); idx >= 0 {
		section = section[idx:]
	}

	matches := reYCSBLine.FindAllStringSubmatch(section, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("go-ycsb summary not found in output")
	}

	res := &Result{PerOperation: map[string]OpStats{}}
	var latWeighted float64
	var p99Weighted float64
	var p95Weighted float64
	lines := strings.Split(section, "\n")

	for _, m := range matches {
		op := m[1]
		count, _ := strconv.ParseInt(m[2], 10, 64)
		ops, _ := strconv.ParseFloat(m[3], 64)
		avgUs, _ := strconv.ParseFloat(m[4], 64)
		p99Us, _ := strconv.ParseFloat(m[5], 64)

		var p95Us float64
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), op) {
				if mm := reYCSB95.FindStringSubmatch(l); mm != nil {
					p95Us, _ = strconv.ParseFloat(mm[1], 64)
				}
			}
		}

		if op == "TOTAL" {
			continue
		}
		isErr := strings.HasSuffix(op, "_ERROR")
		if isErr {
			res.Errors += count
			continue
		}
		res.PerOperation[op] = OpStats{
			OPS:   ops,
			Count: count,
			AvgMs: avgUs / 1000,
			P99Ms: p99Us / 1000,
		}
		res.TotalOps += count
		res.ThroughputOPS += ops
		latWeighted += avgUs / 1000 * float64(count)
		p99Weighted += p99Us / 1000 * float64(count)
		p95Weighted += p95Us / 1000 * float64(count)
	}
	if res.TotalOps > 0 {
		res.LatencyAvgMs = latWeighted / float64(res.TotalOps)
		res.LatencyP99Ms = p99Weighted / float64(res.TotalOps)
		res.LatencyP95Ms = p95Weighted / float64(res.TotalOps)
	}
	return res, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
