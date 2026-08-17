package driver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Sysbench drives the classic OLTP benchmark for relational engines.
// It maps the read/write mix onto the stock oltp_* lua scripts.
type Sysbench struct {
	Image string
}

func NewSysbench(image string) *Sysbench {
	if image == "" {
		image = "severalnines/sysbench:latest"
	}
	return &Sysbench{Image: image}
}

func (s *Sysbench) Name() string        { return "sysbench" }
func (s *Sysbench) DefaultImage() string { return s.Image }

func (s *Sysbench) Supports(engine Engine) bool {
	return engine == EngineMySQL || engine == EnginePostgreSQL
}

// workloadScript picks the lua script that best matches the requested mix.
func workloadScript(spec RunSpec) string {
	switch {
	case spec.WritePercent == 0:
		return "oltp_read_only"
	case spec.WritePercent >= 90:
		return "oltp_write_only"
	default:
		return "oltp_read_write"
	}
}

func (s *Sysbench) BuildScript(engine Engine, conn Connection, spec RunSpec) (string, error) {
	if !s.Supports(engine) {
		return "", fmt.Errorf("sysbench does not support engine %q", engine)
	}
	var connArgs string
	switch engine {
	case EngineMySQL:
		connArgs = fmt.Sprintf(
			`--db-driver=mysql --mysql-host=%s --mysql-port=%d --mysql-user=%s --mysql-password="$DB_PASSWORD" --mysql-db=%s`,
			conn.Host, conn.Port, conn.User, conn.Database)
	case EnginePostgreSQL:
		connArgs = fmt.Sprintf(
			`--db-driver=pgsql --pgsql-host=%s --pgsql-port=%d --pgsql-user=%s --pgsql-password="$DB_PASSWORD" --pgsql-db=%s`,
			conn.Host, conn.Port, conn.User, conn.Database)
	}

	sizing := fmt.Sprintf("--tables=%d --table-size=%d", spec.Tables, spec.TableSize)
	extra := ""
	for k, v := range spec.Extra {
		extra += fmt.Sprintf(" --%s=%s", k, v)
	}
	workload := workloadScript(spec)

	// A shell function (not a variable) so "$DB_PASSWORD" is expanded - with
	// correct quoting - each time the command actually runs.
	var b strings.Builder
	b.WriteString("set -e\n")
	b.WriteString(fmt.Sprintf("sb() { sysbench %s %s \"$@\"; }\n", connArgs, sizing))
	if !spec.SkipPrepare {
		b.WriteString(fmt.Sprintf("echo '[everest-perf] preparing dataset'\nsb %s prepare\n", workload))
	}
	b.WriteString(fmt.Sprintf("echo '%s'\n", MarkBegin))
	b.WriteString(fmt.Sprintf("sb --threads=%d --time=%d --report-interval=10%s %s run\n",
		spec.Threads, spec.DurationSeconds, extra, workload))
	b.WriteString(fmt.Sprintf("echo '%s'\n", MarkEnd))
	if !spec.SkipCleanup {
		b.WriteString(fmt.Sprintf("sb %s cleanup || true\n", workload))
	}
	return b.String(), nil
}

var (
	reTransactions = regexp.MustCompile(`transactions:\s+(\d+)\s+\(([\d.]+) per sec\.\)`)
	reQueries      = regexp.MustCompile(`queries:\s+(\d+)\s+\(([\d.]+) per sec\.\)`)
	reIgnoredErrs  = regexp.MustCompile(`ignored errors:\s+(\d+)`)
	reLatAvg       = regexp.MustCompile(`avg:\s+([\d.]+)`)
	reLatMax       = regexp.MustCompile(`max:\s+([\d.]+)`)
	reLat95        = regexp.MustCompile(`95th percentile:\s+([\d.]+)`)
)

func (s *Sysbench) ParseOutput(log string) (*Result, error) {
	section := extractSection(log)
	res := &Result{}

	m := reTransactions.FindStringSubmatch(section)
	if m == nil {
		return nil, fmt.Errorf("sysbench summary not found in output")
	}
	res.TotalOps, _ = strconv.ParseInt(m[1], 10, 64)
	res.ThroughputOPS, _ = strconv.ParseFloat(m[2], 64)

	if m := reQueries.FindStringSubmatch(section); m != nil {
		res.QPS, _ = strconv.ParseFloat(m[2], 64)
	}
	if m := reIgnoredErrs.FindStringSubmatch(section); m != nil {
		res.Errors, _ = strconv.ParseInt(m[1], 10, 64)
	}
	// Latency block: sysbench prints min/avg/max/95th under "Latency (ms):".
	if m := reLatAvg.FindStringSubmatch(section); m != nil {
		res.LatencyAvgMs, _ = strconv.ParseFloat(m[1], 64)
	}
	if m := reLatMax.FindStringSubmatch(section); m != nil {
		res.LatencyMaxMs, _ = strconv.ParseFloat(m[1], 64)
	}
	if m := reLat95.FindStringSubmatch(section); m != nil {
		res.LatencyP95Ms, _ = strconv.ParseFloat(m[1], 64)
	}
	return res, nil
}

// extractSection returns the text between the result markers, or the whole
// log if markers are absent (direct tool output, tests).
func extractSection(log string) string {
	start := strings.Index(log, MarkBegin)
	end := strings.LastIndex(log, MarkEnd)
	if start >= 0 && end > start {
		return log[start+len(MarkBegin) : end]
	}
	return log
}
