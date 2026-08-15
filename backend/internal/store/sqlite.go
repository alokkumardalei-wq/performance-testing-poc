package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go sqlite, no cgo
)

// ErrNotFound is returned when a run id does not exist.
var ErrNotFound = errors.New("run not found")

const maxRawOutput = 512 * 1024

// SQLiteStore persists runs in a single SQLite database file. By default the
// file lives on an emptyDir (ephemeral, per mentor guidance: no PVC unless
// the user opts in via chart values). Pass ":memory:" for tests.
type SQLiteStore struct {
	db *sql.DB
}

func NewSQLite(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// The modernc driver serializes writes; a single connection avoids
	// SQLITE_BUSY between the API goroutines and the run watcher.
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS runs (
	id            TEXT PRIMARY KEY,
	created_at    TEXT NOT NULL,
	started_at    TEXT,
	finished_at   TEXT,
	namespace     TEXT NOT NULL,
	instance_name TEXT NOT NULL,
	engine        TEXT NOT NULL,
	driver        TEXT NOT NULL,
	profile       TEXT NOT NULL,
	status        TEXT NOT NULL,
	message       TEXT NOT NULL DEFAULT '',
	job_name      TEXT NOT NULL DEFAULT '',
	spec          TEXT NOT NULL,
	result        TEXT,
	fingerprint   TEXT,
	raw_output    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_runs_instance ON runs(namespace, instance_name, created_at DESC);`)
	return err
}

func (s *SQLiteStore) CreateRun(r *Run) error {
	spec, _ := json.Marshal(r.Spec)
	_, err := s.db.Exec(`INSERT INTO runs
		(id, created_at, namespace, instance_name, engine, driver, profile, status, message, job_name, spec)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.CreatedAt.Format(time.RFC3339Nano), r.Namespace, r.InstanceName,
		r.Engine, r.Driver, r.Profile, string(r.Status), r.Message, r.JobName, string(spec))
	return err
}

func (s *SQLiteStore) UpdateRun(r *Run) error {
	spec, _ := json.Marshal(r.Spec)
	var result, fingerprint any
	if r.Result != nil {
		b, _ := json.Marshal(r.Result)
		result = string(b)
	}
	if r.Fingerprint != nil {
		b, _ := json.Marshal(r.Fingerprint)
		fingerprint = string(b)
	}
	raw := r.RawOutput
	if len(raw) > maxRawOutput {
		raw = raw[:maxRawOutput] + "\n...[truncated]"
	}
	res, err := s.db.Exec(`UPDATE runs SET
		started_at=?, finished_at=?, status=?, message=?, job_name=?, spec=?, result=?, fingerprint=?,
		raw_output = CASE WHEN ?='' THEN raw_output ELSE ? END
		WHERE id=?`,
		nullableTime(r.StartedAt), nullableTime(r.FinishedAt), string(r.Status), r.Message,
		r.JobName, string(spec), result, fingerprint, raw, raw, r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) GetRun(id string) (*Run, error) {
	rows, err := s.db.Query(selectCols+` FROM runs WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	return scanRun(rows)
}

func (s *SQLiteStore) GetRawOutput(id string) (string, error) {
	var raw string
	err := s.db.QueryRow(`SELECT raw_output FROM runs WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return raw, err
}

func (s *SQLiteStore) ListRuns(f ListFilter) ([]*Run, error) {
	q := selectCols + ` FROM runs`
	var args []any
	var where []string
	if f.Namespace != "" {
		where = append(where, "namespace=?")
		args = append(args, f.Namespace)
	}
	if f.InstanceName != "" {
		where = append(where, "instance_name=?")
		args = append(args, f.InstanceName)
	}
	for i, w := range where {
		if i == 0 {
			q += " WHERE " + w
		} else {
			q += " AND " + w
		}
	}
	q += " ORDER BY created_at DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []*Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

func (s *SQLiteStore) DeleteRun(id string) error {
	res, err := s.db.Exec(`DELETE FROM runs WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

const selectCols = `SELECT id, created_at, started_at, finished_at, namespace, instance_name,
	engine, driver, profile, status, message, job_name, spec, result, fingerprint`

func scanRun(rows *sql.Rows) (*Run, error) {
	var r Run
	var createdAt string
	var startedAt, finishedAt, result, fingerprint sql.NullString
	var spec string
	var status string
	if err := rows.Scan(&r.ID, &createdAt, &startedAt, &finishedAt, &r.Namespace, &r.InstanceName,
		&r.Engine, &r.Driver, &r.Profile, &status, &r.Message, &r.JobName, &spec, &result, &fingerprint); err != nil {
		return nil, err
	}
	r.Status = RunStatus(status)
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if startedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, startedAt.String)
		r.StartedAt = &t
	}
	if finishedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, finishedAt.String)
		r.FinishedAt = &t
	}
	_ = json.Unmarshal([]byte(spec), &r.Spec)
	if result.Valid && result.String != "" {
		_ = json.Unmarshal([]byte(result.String), &r.Result)
	}
	if fingerprint.Valid && fingerprint.String != "" {
		_ = json.Unmarshal([]byte(fingerprint.String), &r.Fingerprint)
	}
	return &r, nil
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339Nano)
}
