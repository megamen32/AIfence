package catalog

// The journal is shared by the existing per-store workers. It is local-only,
// stores no credentials or request headers, and has one durable URL/task row
// rather than creating a second crawler. SQLite WAL supports independent hosts.
import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

const MaxAttempts = 5
const maxArchiveBody = 32 << 20

type Journal struct {
	db   *sql.DB
	root string
}
type Evidence struct {
	ID          int64     `json:"id"`
	Source      string    `json:"source"`
	URL         string    `json:"url"`
	ResponseURL string    `json:"response_url"`
	At          time.Time `json:"captured_at"`
	Status      int       `json:"http_status"`
	Digest      string    `json:"sha256"`
}
type Failure struct {
	Source      string    `json:"source"`
	URL         string    `json:"url"`
	Kind        string    `json:"kind"`
	Generation  string    `json:"generation"`
	ErrorType   string    `json:"error_type"`
	Attempts    int       `json:"attempts"`
	NextAttempt time.Time `json:"next_attempt"`
	EvidenceID  int64     `json:"evidence_id"`
	State       string    `json:"state"`
}
type journalKey struct{}
type jobContext struct {
	j                *Journal
	generation, kind string
}

func WithJournal(ctx context.Context, j *Journal, generation, kind string) context.Context {
	if j == nil {
		return ctx
	}
	return context.WithValue(ctx, journalKey{}, jobContext{j, generation, kind})
}
func JobJournal(ctx context.Context) (*Journal, string, string) {
	x, _ := ctx.Value(journalKey{}).(jobContext)
	return x.j, x.generation, x.kind
}
func OpenJournal(root string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Join(root, "raw-html"), 0700); err != nil {
		return nil, err
	}
	file := filepath.Join(root, "crawl.sqlite")
	// Explicit file permissions even on a shared development checkout.
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(file, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", file+"?_pragma=busy_timeout(15000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	j := &Journal{db: db, root: root}
	_, err = db.Exec(`
 CREATE TABLE IF NOT EXISTS http_responses(
 id INTEGER PRIMARY KEY, source TEXT NOT NULL, url TEXT NOT NULL,
 response_url TEXT NOT NULL, captured_at INTEGER NOT NULL,
 status INTEGER NOT NULL, sha256 TEXT NOT NULL, location TEXT NOT NULL DEFAULT '',
 bytes INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS responses_lookup ON http_responses(source,url,status,id DESC);
 CREATE TABLE IF NOT EXISTS crawl_tasks(
 source TEXT NOT NULL,url TEXT NOT NULL,kind TEXT NOT NULL,generation TEXT NOT NULL,
 priority INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL DEFAULT 'queued',
 updated_at INTEGER NOT NULL,evidence_id INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(source,url,kind,generation));
 CREATE TABLE IF NOT EXISTS failed_tasks(
 source TEXT NOT NULL,url TEXT NOT NULL,kind TEXT NOT NULL,generation TEXT NOT NULL,
 error_type TEXT NOT NULL,attempts INTEGER NOT NULL,next_attempt INTEGER NOT NULL,
 evidence_id INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(source,url,kind,generation));
 CREATE INDEX IF NOT EXISTS failures_due ON failed_tasks(source,generation,state,next_attempt);
 CREATE INDEX IF NOT EXISTS failures_cluster ON failed_tasks(state,source,error_type,updated_at);
 CREATE TABLE IF NOT EXISTS repair_batches(
 id TEXT PRIMARY KEY,source TEXT NOT NULL,error_type TEXT NOT NULL,
 state TEXT NOT NULL,created_at INTEGER NOT NULL,rule_version TEXT NOT NULL DEFAULT '',
 report TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS active_rules(source TEXT PRIMARY KEY,rule_json TEXT NOT NULL,version TEXT NOT NULL,activated_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS repair_batch_examples(batch_id TEXT NOT NULL,evidence_id INTEGER NOT NULL,PRIMARY KEY(batch_id,evidence_id));
 CREATE INDEX IF NOT EXISTS repair_examples_evidence ON repair_batch_examples(evidence_id);
 `)
	if err != nil {
		db.Close()
		return nil, err
	}
	return j, nil
}
func (j *Journal) Close() error { return j.db.Close() }
func (j *Journal) archivePath(digest string) (string, error) {
	b, e := hex.DecodeString(digest)
	if e != nil || len(b) != sha256.Size {
		return "", fmt.Errorf("invalid archive hash")
	}
	return filepath.Join(j.root, "raw-html", digest[:2], digest+".html.gz"), nil
}
func (j *Journal) writeRaw(body []byte) (string, error) {
	if len(body) > maxArchiveBody {
		return "", fmt.Errorf("response exceeds archive limit")
	}
	hash := sha256.Sum256(body)
	digest := hex.EncodeToString(hash[:])
	path, e := j.archivePath(digest)
	if e != nil {
		return "", e
	}
	if _, e = os.Stat(path); e == nil {
		return digest, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	var fs unix.Statfs_t
	if e = unix.Statfs(j.root, &fs); e != nil {
		return "", e
	}
	if uint64(fs.Bavail)*uint64(fs.Bsize) < 2<<30 {
		return "", fmt.Errorf("raw archive disk reserve below 2 GiB; collection paused")
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return "", e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".raw-*")
	if e != nil {
		return "", e
	}
	defer os.Remove(f.Name())
	z, e := gzip.NewWriterLevel(f, gzip.BestSpeed)
	if e != nil {
		f.Close()
		return "", e
	}
	if _, e = z.Write(body); e == nil {
		e = z.Close()
	} else {
		_ = z.Close()
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return "", e
	}
	// A digest is immutable: simultaneous copies contain identical bytes.
	if e = os.Rename(f.Name(), path); e != nil {
		return "", e
	}
	return digest, nil
}
func (j *Journal) Archive(source, url, responseURL string, status int, location string, at time.Time, body []byte) (Evidence, error) {
	digest, e := j.writeRaw(body)
	if e != nil {
		return Evidence{}, e
	}
	res, e := j.db.Exec("INSERT INTO http_responses(source,url,response_url,captured_at,status,sha256,location,bytes) VALUES(?,?,?,?,?,?,?,?)", source, url, responseURL, at.UnixNano(), status, digest, location, len(body))
	if e != nil {
		return Evidence{}, e
	}
	id, e := res.LastInsertId()
	return Evidence{id, source, url, responseURL, at.UTC(), status, digest}, e
}
func readEvidenceRow(row interface{ Scan(...any) error }) (Evidence, error) {
	var e Evidence
	var ns int64
	err := row.Scan(&e.ID, &e.Source, &e.URL, &e.ResponseURL, &ns, &e.Status, &e.Digest)
	e.At = time.Unix(0, ns).UTC()
	return e, err
}

const evidenceCols = "id,source,url,response_url,captured_at,status,sha256"

func (j *Journal) Latest(source, url string) (Evidence, error) {
	return readEvidenceRow(j.db.QueryRow("SELECT "+evidenceCols+" FROM http_responses WHERE source=? AND url=? AND status=200 ORDER BY id DESC LIMIT 1", source, url))
}
func (j *Journal) Evidence(id int64) (Evidence, error) {
	return readEvidenceRow(j.db.QueryRow("SELECT "+evidenceCols+" FROM http_responses WHERE id=?", id))
}
func (j *Journal) Raw(e Evidence) ([]byte, error) {
	path, err := j.archivePath(e.Digest)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	b, err := io.ReadAll(io.LimitReader(z, maxArchiveBody+1))
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(b)
	if len(b) > maxArchiveBody || hex.EncodeToString(h[:]) != e.Digest {
		return nil, fmt.Errorf("invalid retained response %d", e.ID)
	}
	return b, nil
}

// Import already retained bodies with their original timestamps, exactly once.
// A checkpoint with no body is a legacy failure, not invented evidence.
func (j *Journal) Import(source, url string, at time.Time, body []byte) (Evidence, error) {
	h := sha256.Sum256(body)
	digest := hex.EncodeToString(h[:])
	e, err := readEvidenceRow(j.db.QueryRow("SELECT "+evidenceCols+" FROM http_responses WHERE source=? AND url=? AND sha256=? AND captured_at=? AND status=200 LIMIT 1", source, url, digest, at.UnixNano()))
	if err == nil {
		return e, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return e, err
	}
	return j.Archive(source, url, url, 200, "", at, body)
}
func (j *Journal) Seed(source, generation, kind string, urls []string, priority func(string) int) error {
	tx, e := j.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	stmt, e := tx.Prepare("INSERT OR IGNORE INTO crawl_tasks(source,url,kind,generation,priority,state,updated_at) VALUES(?,?,?,?,?,'queued',?)")
	if e != nil {
		return e
	}
	defer stmt.Close()
	now := time.Now().UnixNano()
	for _, u := range urls {
		p := 0
		if priority != nil {
			p = priority(u)
		}
		if _, e = stmt.Exec(source, u, kind, generation, p, now); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (j *Journal) Failure(source, url, generation, kind string) (Failure, error) {
	var f Failure
	var ns int64
	e := j.db.QueryRow("SELECT source,url,kind,generation,error_type,attempts,next_attempt,evidence_id,state FROM failed_tasks WHERE source=? AND url=? AND generation=? AND kind=?", source, url, generation, kind).Scan(&f.Source, &f.URL, &f.Kind, &f.Generation, &f.ErrorType, &f.Attempts, &ns, &f.EvidenceID, &f.State)
	f.NextAttempt = time.Unix(0, ns).UTC()
	return f, e
}
func (j *Journal) MayRun(source, url, generation, kind string, now time.Time) (bool, error) {
	f, e := j.Failure(source, url, generation, kind)
	if errors.Is(e, sql.ErrNoRows) {
		var state string
		e = j.db.QueryRow("SELECT state FROM crawl_tasks WHERE source=? AND url=? AND generation=? AND kind=?", source, url, generation, kind).Scan(&state)
		if errors.Is(e, sql.ErrNoRows) {
			return true, nil
		}
		return state != "done", e
	}
	if e != nil {
		return false, e
	}
	return f.State == "retry" && !now.Before(f.NextAttempt), nil
}
func (j *Journal) SeedLegacyFailure(source, url, generation, kind, errorType string, at time.Time, evidenceID int64) error {
	_, e := j.db.Exec("INSERT OR IGNORE INTO failed_tasks(source,url,kind,generation,error_type,attempts,next_attempt,evidence_id,state,detail,updated_at) VALUES(?,?,?,?,?,1,?,?,'retry','legacy checkpoint imported',?)", source, url, kind, generation, errorType, at.UnixNano(), evidenceID, time.Now().UnixNano())
	return e
}
func RetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > MaxAttempts {
		attempt = MaxAttempts
	}
	return time.Minute * time.Duration(1<<(attempt-1))
}
func (j *Journal) RecordFailure(source, url, generation, kind, errorType, detail string, evidenceID int64, now, retryAfter time.Time) error {
	tx, e := j.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	n := 0
	var oldID int64
	var oldType, oldState string
	e = tx.QueryRow("SELECT attempts,evidence_id,error_type,state FROM failed_tasks WHERE source=? AND url=? AND generation=? AND kind=?", source, url, generation, kind).Scan(&n, &oldID, &oldType, &oldState)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if oldState == "repair" && oldType == errorType && evidenceID > 0 && evidenceID == oldID {
		return nil
	}
	n = min(n+1, MaxAttempts)
	if evidenceID == 0 {
		evidenceID = oldID
	}
	state := "retry"
	if n >= MaxAttempts {
		state = "exhausted"
	}
	if errorType == "forbidden" || errorType == "robots_excluded" {
		state = "blocked"
	}
	// Retrying identical archived HTML with the same parser cannot improve it.
	// Hand it to repair immediately; only a validated rule reopens the task.
	if WaitingForRule(errorType, evidenceID) {
		state = "repair"
	}

	next := now.Add(RetryDelay(n))
	if retryAfter.After(next) {
		next = retryAfter
	}
	if state != "retry" {
		next = time.Time{}
	}
	if len(detail) > 500 {
		detail = detail[:500]
	}
	_, e = tx.Exec(`INSERT INTO failed_tasks(source,url,kind,generation,error_type,attempts,next_attempt,evidence_id,state,detail,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source,url,kind,generation) DO UPDATE SET
 error_type=excluded.error_type,attempts=excluded.attempts,next_attempt=excluded.next_attempt,evidence_id=excluded.evidence_id,
 state=excluded.state,detail=excluded.detail,updated_at=excluded.updated_at`, source, url, kind, generation, errorType, n, next.UnixNano(), evidenceID, state, detail, now.UnixNano())
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO crawl_tasks(source,url,kind,generation,state,updated_at,evidence_id) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(source,url,kind,generation) DO UPDATE SET state=excluded.state,updated_at=excluded.updated_at,evidence_id=excluded.evidence_id`, source, url, kind, generation, state, now.UnixNano(), evidenceID)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (j *Journal) Resolve(source, url, generation, kind string, id int64) error {
	tx, e := j.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM failed_tasks WHERE source=? AND url=? AND generation=? AND kind=?", source, url, generation, kind); e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO crawl_tasks(source,url,kind,generation,state,updated_at,evidence_id) VALUES(?,?,?,?,'done',?,?)
 ON CONFLICT(source,url,kind,generation) DO UPDATE SET state='done',updated_at=excluded.updated_at,evidence_id=excluded.evidence_id`, source, url, kind, generation, time.Now().UnixNano(), id); e != nil {
		return e
	}
	return tx.Commit()
}
func (j *Journal) Due(source, generation, kind string, now time.Time, limit int) ([]string, error) {
	rows, e := j.db.Query("SELECT f.url FROM failed_tasks f LEFT JOIN crawl_tasks t ON t.source=f.source AND t.url=f.url AND t.generation=f.generation AND t.kind=f.kind WHERE f.source=? AND f.generation=? AND f.kind=? AND f.state='retry' AND f.next_attempt<=? ORDER BY COALESCE(t.priority,0) DESC,f.next_attempt LIMIT ?", source, generation, kind, now.UnixNano(), limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var urls []string
	for rows.Next() {
		var u string
		if e = rows.Scan(&u); e != nil {
			return nil, e
		}
		urls = append(urls, u)
	}
	return urls, rows.Err()
}
func (j *Journal) NextDue(source, generation, kind string) (time.Time, bool, error) {
	var v sql.NullInt64
	e := j.db.QueryRow("SELECT MIN(next_attempt) FROM failed_tasks WHERE source=? AND generation=? AND kind=? AND state='retry'", source, generation, kind).Scan(&v)
	if e != nil || !v.Valid {
		return time.Time{}, false, e
	}
	return time.Unix(0, v.Int64).UTC(), true, nil
}
func FailureType(err error, fallback string) (string, time.Time) {
	if err == nil {
		return fallback, time.Time{}
	}
	var he *HTTPStatus
	if errors.As(err, &he) {
		switch he.Code {
		case 403:
			return "forbidden", he.RetryAfter
		case 429:
			return "rate_limit", he.RetryAfter
		case 503:
			return "http_503", he.RetryAfter
		}
		if he.Code >= 500 {
			return "http_5xx", he.RetryAfter
		}
		return fmt.Sprintf("http_%d", he.Code), he.RetryAfter
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "robots.txt disallows"):
		return "robots_excluded", time.Time{}
	case strings.Contains(s, "minimum quantity"):
		return "parse_quantity_conflict", time.Time{}
	case strings.Contains(s, "context deadline"), strings.Contains(s, "timeout"), strings.Contains(s, "connection"), strings.Contains(s, "get \""):
		return "network", time.Time{}
	}
	return fallback, time.Time{}
}

// Parse retries use the exact retained successful response, not repeated HTTP.
func (j *Journal) RetryBody(source, url, generation, kind string) ([]byte, Evidence, bool, error) {
	f, e := j.Failure(source, url, generation, kind)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, Evidence{}, false, nil
	}
	if e != nil {
		return nil, Evidence{}, false, e
	}
	if !strings.HasPrefix(f.ErrorType, "parse_") || f.EvidenceID == 0 {
		return nil, Evidence{}, false, nil
	}
	ev, e := j.Evidence(f.EvidenceID)
	if e != nil {
		return nil, ev, false, e
	}
	if ev.Status != 200 || ev.Source != source || ev.URL != url {
		return nil, ev, false, fmt.Errorf("retry evidence does not match task")
	}
	b, e := j.Raw(ev)
	return b, ev, e == nil, e
}
func (j *Journal) Begin(source, url, generation, kind string) error {
	_, e := j.db.Exec(`INSERT INTO crawl_tasks(source,url,kind,generation,state,updated_at) VALUES(?,?,?,?,'running',?) ON CONFLICT(source,url,kind,generation) DO UPDATE SET state='running',updated_at=excluded.updated_at`, source, url, kind, generation, time.Now().UnixNano())
	return e
}
