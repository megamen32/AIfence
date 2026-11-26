package decision

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"agent/internal/db"
	"github.com/airlockrun/agentsdk"
)

type Service struct {
	conn  *agentsdk.AgentDB
	q     *db.Queries
	topic *agentsdk.UIChangeTopic
}

func New(conn *agentsdk.AgentDB, topic *agentsdk.UIChangeTopic) *Service {
	if conn == nil || topic == nil {
		panic("decision: agent DB and UI change topic required")
	}
	return &Service{conn: conn, q: db.New(conn), topic: topic}
}
func owner(ctx context.Context) (string, error) {
	user, ok := agentsdk.CallerFromContext(ctx).User()
	if !ok || user.ID == "" {
		return "", errors.New("требуется учётная запись Airlock")
	}
	return user.ID, nil
}
func decode(raw json.RawMessage, err error) (Record, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrMissing
	}
	if err != nil {
		return Record{}, err
	}
	var r Record
	err = json.Unmarshal(raw, &r)
	return r, err
}
func (s *Service) Get(ctx context.Context, in GetInput) (Record, error) {
	who, err := owner(ctx)
	if err != nil {
		return Record{}, err
	}
	return decode(s.q.PMRead(ctx, db.PMReadParams{OwnerID: who, HypothesisID: in.HypothesisID}))
}
func (s *Service) List(ctx context.Context, _ struct{}) (ListResult, error) {
	who, err := owner(ctx)
	if err != nil {
		return ListResult{}, err
	}
	rows, err := s.q.PMList(ctx, who)
	if err != nil {
		return ListResult{}, err
	}
	result := ListResult{Records: make([]Record, 0, len(rows))}
	for _, raw := range rows {
		r, err := decode(raw, nil)
		if err != nil {
			return ListResult{}, err
		}
		result.Records = append(result.Records, r)
	}
	return result, nil
}

// Every update writes the current head and an immutable revision in the caller's transaction.
func (s *Service) persist(ctx context.Context, tx agentsdk.DBTX, who string, r Record) error {
	q := db.New(tx)
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	id := r.Hypothesis.ID
	var rows int64
	if r.Version == 1 {
		rows, err = q.PMCreate(ctx, db.PMCreateParams{OwnerID: who, HypothesisID: id, Version: r.Version, Body: raw})
	} else {
		rows, err = q.PMUpdate(ctx, db.PMUpdateParams{OwnerID: who, HypothesisID: id, OldVersion: r.Version - 1, NewVersion: r.Version, Body: raw})
	}
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrVersion
	}
	if err = q.PMRemember(ctx, db.PMRememberParams{OwnerID: who, HypothesisID: id, Version: r.Version, Body: raw}); err != nil {
		return err
	}
	return s.topic.Publish(ctx, tx, who)
}
func (s *Service) Save(ctx context.Context, in SaveInput) (Record, error) {
	who, err := owner(ctx)
	if err != nil {
		return Record{}, err
	}
	var out Record
	err = s.conn.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		var prior *Record
		if in.ExpectedVersion != 0 {
			r, e := decode(db.New(tx).PMLock(ctx, db.PMLockParams{OwnerID: who, HypothesisID: in.Hypothesis.ID}))
			if e != nil {
				return e
			}
			prior = &r
		}
		next, e := draft(prior, in)
		if e != nil {
			return e
		}
		if e = s.persist(ctx, tx, who, next); e != nil {
			return e
		}
		out = next
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return out, nil
}
func (s *Service) Send(ctx context.Context, in SendInput) (Task, error) {
	who, err := owner(ctx)
	if err != nil {
		return Task{}, err
	}
	if !in.Confirmed {
		return Task{}, errors.New("создание задачи требует явного подтверждения")
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 256 {
		return Task{}, errors.New("укажите стабильный ключ операции длиной до 256 байт")
	}
	var out Task
	err = s.conn.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		q := db.New(tx)
		r, e := decode(q.PMLock(ctx, db.PMLockParams{OwnerID: who, HypothesisID: in.HypothesisID}))
		if e != nil {
			return e
		}
		existing, e := q.PMClaimTaskKey(ctx, db.PMClaimTaskKeyParams{OwnerID: who, RequestKey: in.IdempotencyKey, HypothesisID: in.HypothesisID})
		if e != nil {
			return e
		}
		if existing != in.HypothesisID {
			return errors.New("ключ операции уже связан с другой карточкой")
		}
		if r.Task == nil {
			if in.ExpectedVersion != nil && *in.ExpectedVersion != r.Version {
				return ErrVersion
			}
			t := newTask(who, r)
			raw, e := json.Marshal(t)
			if e != nil {
				return e
			}
			rows, e := q.PMAddTask(ctx, db.PMAddTaskParams{OwnerID: who, HypothesisID: r.Hypothesis.ID, RequestKey: in.IdempotencyKey, TaskID: t.ID, Receipt: raw})
			if e != nil {
				return e
			}
			if rows != 1 {
				return errors.New("конфликт ключа операции, перечитайте карточку")
			}
			r.Version++
			r.Task = &t
			r.Hypothesis.Status = "in_backlog"
			r.Hypothesis.BacklogRef = &BacklogRef{ID: t.ID, URL: t.URL, Tag: t.Tag}
			if e = s.persist(ctx, tx, who, r); e != nil {
				return e
			}
		}
		// Read the persisted receipt from the backlog table even on retries.
		raw, e := q.PMReadTask(ctx, db.PMReadTaskParams{OwnerID: who, HypothesisID: in.HypothesisID})
		if e != nil {
			return e
		}
		return json.Unmarshal(raw, &out)
	})
	if err != nil {
		return Task{}, err
	}
	return out, nil
}
func (s *Service) Evaluate(ctx context.Context, in EvaluateInput) (Assessment, error) {
	who, err := owner(ctx)
	if err != nil {
		return Assessment{}, err
	}
	if in.ReleaseID == "" && in.ReleasedOn == "" && in.Measurement == nil {
		r, e := s.Get(ctx, GetInput{HypothesisID: in.HypothesisID})
		if e != nil {
			return Assessment{}, e
		}
		return assess(r, in, time.Now())
	}
	var out Assessment
	err = s.conn.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		r, e := decode(db.New(tx).PMLock(ctx, db.PMLockParams{OwnerID: who, HypothesisID: in.HypothesisID}))
		if e != nil {
			return e
		}
		out, e = assess(r, in, time.Now())
		if e != nil {
			return e
		}
		if r.Assessment != nil && reflect.DeepEqual(*r.Assessment, out) {
			return nil
		}
		if r.Assessment != nil && (r.Assessment.Status == "supported" || r.Assessment.Status == "rejected") {
			return fmt.Errorf("итог уже зафиксирован в версии %d", r.Version)
		}
		r.Assessment = &out
		r.Version++
		r.Hypothesis.Status = out.Status
		r.Hypothesis.ReleaseID = out.ReleaseID
		r.Hypothesis.DecisionReason = out.Reason
		return s.persist(ctx, tx, who, r)
	})
	if err != nil {
		return Assessment{}, err
	}
	return out, nil
}
