package catalog

import (
	"bytes"
	"fmt"
	"time"
)

// Version this deterministic audit whenever its parser behavior changes.
// An unchanged parser/evidence pair is never re-evaluated in a loop.
const codeAuditRevision = "exact-disposition-v1"

type CodeAuditReport struct {
	Checked  int            `json:"checked"`
	Requeued int            `json:"requeued"`
	States   map[string]int `json:"states"`
}

func codeResolved(s Source, p PageResult) bool {
	if s.ID == "softmagazin" && p.Category != "software" {
		return p.State == "out_of_scope" && p.Category == "hardware"
	}
	switch p.State {
	case "priced":
		return len(p.Offers) > 0
	case "no_public_price", "unavailable", "not_product":
		return len(p.Offers) == 0
	}
	return false
}

// Apply the very same production InspectPage implementation before asking a
// model. The Softline family/direct adapter has its own parser; this preflight
// deliberately does not substitute InspectPage for that adapter.
func (j *Journal) PreflightRepairs(limit int) (CodeAuditReport, error) {
	report := CodeAuditReport{States: map[string]int{}}
	if limit < 1 {
		return report, fmt.Errorf("positive audit limit required")
	}
	if _, e := j.db.Exec(`CREATE TABLE IF NOT EXISTS repair_code_checks(
 evidence_id INTEGER NOT NULL,revision TEXT NOT NULL,source TEXT NOT NULL,
 disposition TEXT NOT NULL,reason TEXT NOT NULL,checked_at INTEGER NOT NULL,
 PRIMARY KEY(evidence_id,revision))`); e != nil {
		return report, e
	}
	rows, e := j.db.Query(`SELECT DISTINCT f.evidence_id FROM failed_tasks f
 JOIN http_responses r ON r.id=f.evidence_id AND r.source=f.source AND r.url=f.url AND r.status=200
 LEFT JOIN repair_code_checks a ON a.evidence_id=f.evidence_id AND a.revision=?
 WHERE f.source IN ('softmagazin','softmap') AND f.state IN ('repair','exhausted')
 AND f.error_type LIKE 'parse_%' AND a.evidence_id IS NULL
 ORDER BY f.evidence_id LIMIT ?`, codeAuditRevision, limit)
	if e != nil {
		return report, e
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return report, e
		}
		ids = append(ids, id)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return report, e
	}
	rows.Close()
	for _, id := range ids {
		ev, e := j.Evidence(id)
		if e != nil {
			return report, e
		}
		raw, e := j.Raw(ev)
		if e != nil {
			return report, e
		}
		s, e := sourceByID(ev.Source)
		if e != nil {
			return report, e
		}
		p, parseErr := InspectPage(s, ev.URL, bytes.NewReader(raw), ev.At)
		resolved := parseErr == nil && codeResolved(s, p)
		disposition := p.State
		if parseErr != nil {
			disposition = "parse_error"
		}
		reason := p.StatusEvidence
		if reason == "" {
			reason = "production parser result"
		}
		if parseErr != nil {
			reason = parseErr.Error()
		}
		tx, e := j.db.Begin()
		if e != nil {
			return report, e
		}
		now := time.Now().UnixNano()
		if _, e = tx.Exec("INSERT OR IGNORE INTO repair_code_checks(evidence_id,revision,source,disposition,reason,checked_at) VALUES(?,?,?,?,?,?)", id, codeAuditRevision, ev.Source, disposition, reason, now); e != nil {
			tx.Rollback()
			return report, e
		}
		if resolved {
			res, e := tx.Exec("UPDATE failed_tasks SET state='retry',attempts=0,next_attempt=?,updated_at=? WHERE source=? AND url=? AND evidence_id=? AND state IN ('repair','exhausted') AND error_type LIKE 'parse_%'", now, now, ev.Source, ev.URL, id)
			if e != nil {
				tx.Rollback()
				return report, e
			}
			n, e := res.RowsAffected()
			if e != nil {
				tx.Rollback()
				return report, e
			}
			report.Requeued += int(n)
			if _, e = tx.Exec(`UPDATE crawl_tasks SET state='retry',updated_at=? WHERE source=? AND url=? AND EXISTS(
     SELECT 1 FROM failed_tasks f WHERE f.source=crawl_tasks.source AND f.url=crawl_tasks.url AND f.kind=crawl_tasks.kind AND f.generation=crawl_tasks.generation
     AND f.evidence_id=? AND f.state='retry' AND f.attempts=0)`, now, ev.Source, ev.URL, id); e != nil {
				tx.Rollback()
				return report, e
			}
		}
		if e = tx.Commit(); e != nil {
			return report, e
		}
		report.Checked++
		report.States[ev.Source+"/"+disposition]++
	}
	return report, nil
}

// Reopen retained Softline product failures once for a real parser revision,
// through the existing writer's exact adapter, never through the LLM or HTTP.
func (j *Journal) RequeueSoftlineVariantChecks(generation string) (int, error) {
	if _, e := j.db.Exec(`CREATE TABLE IF NOT EXISTS repair_code_checks(
 evidence_id INTEGER NOT NULL,revision TEXT NOT NULL,source TEXT NOT NULL,
 disposition TEXT NOT NULL,reason TEXT NOT NULL,checked_at INTEGER NOT NULL,
 PRIMARY KEY(evidence_id,revision))`); e != nil {
		return 0, e
	}
	tx, e := j.db.Begin()
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	const revision = "softline-exact-family-gap-v1"
	now := time.Now().UnixNano()
	if _, e = tx.Exec(`INSERT OR IGNORE INTO repair_code_checks(evidence_id,revision,source,disposition,reason,checked_at)
 SELECT DISTINCT f.evidence_id,?,f.source,'reparse_requested','exact family variant membership',?
 FROM failed_tasks f JOIN http_responses r ON r.id=f.evidence_id AND r.source=f.source AND r.url=f.url AND r.status=200
 WHERE f.source='softline' AND f.kind='product' AND f.generation=?
 AND f.state IN ('repair','exhausted') AND f.error_type LIKE 'parse_%'`, revision, now, generation); e != nil {
		return 0, e
	}
	res, e := tx.Exec(`UPDATE failed_tasks SET state='retry',attempts=0,next_attempt=?,updated_at=?
 WHERE source='softline' AND kind='product' AND generation=? AND state IN ('repair','exhausted')
 AND error_type LIKE 'parse_%' AND EXISTS(SELECT 1 FROM repair_code_checks a WHERE a.evidence_id=failed_tasks.evidence_id AND a.revision=? AND a.checked_at=?)`, now, now, generation, revision, now)
	if e != nil {
		return 0, e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return 0, e
	}
	if _, e = tx.Exec(`UPDATE crawl_tasks SET state='retry',updated_at=? WHERE source='softline' AND kind='product' AND generation=?
 AND EXISTS(SELECT 1 FROM failed_tasks f WHERE f.source=crawl_tasks.source AND f.url=crawl_tasks.url AND f.kind=crawl_tasks.kind AND f.generation=crawl_tasks.generation AND f.state='retry' AND f.attempts=0 AND f.updated_at=?)`, now, generation, now); e != nil {
		return 0, e
	}
	if e = tx.Commit(); e != nil {
		return 0, e
	}
	return int(n), nil
}
