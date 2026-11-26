package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/airlockrun/agentsdk"
	"github.com/google/uuid"
)

const (
	privateWatchDir = "watch_private"
	privateSeries   = "watch-series.json"
	privateContext  = "watch-context.json"
	privateFileMax  = 8 << 20
)

func closedOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Scheme+"://"+u.Host != origin {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return u.Scheme == "https" || u.Scheme == "http" &&
		(host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "127.0.0.1" || host == "::1")
}

func (s *privateWatch) registerRoutes(a *agentsdk.Agent) {
	a.RegisterRoute(&agentsdk.Route{
		Method: "GET", Path: "/watchdog", Access: agentsdk.AccessUser, Logging: agentsdk.LoggingOff,
		Description: "Private watchdog setup and consent",
		Handler:     s.page,
	})
	a.RegisterRoute(&agentsdk.Route{
		Method: "POST", Path: "/watchdog", Access: agentsdk.AccessUser, Logging: agentsdk.LoggingOff,
		Description: "Private evidence upload or owner-authorized watchdog action",
		Handler:     s.submit,
	})
}

func (s *privateWatch) browserOwner(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	origin := s.origin(r.Context())
	if !closedOrigin(origin) {
		w.WriteHeader(http.StatusNotFound)
		return uuid.Nil, false
	}
	if r.Method == http.MethodPost && r.Header.Get("Origin") != origin {
		w.WriteHeader(http.StatusForbidden)
		return uuid.Nil, false
	}
	owner, err := watchOwner(r.Context(), agentsdk.ExecutionRequest)
	if err != nil || agentsdk.CallerFromContext(r.Context()).Origin().Interface != agentsdk.InterfaceHTTP {
		w.WriteHeader(http.StatusForbidden)
		return uuid.Nil, false
	}
	return owner, true
}

func (s *privateWatch) page(w http.ResponseWriter, r *http.Request) error {
	if _, ok := s.browserOwner(w, r); !ok {
		return nil
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; base-uri 'none'")
	_, err := io.WriteString(w, privateWatchPage)
	return err
}

const privateWatchPage = `<!doctype html><html lang="ru"><meta charset="utf-8">
<title>Закрытый сторож</title><h1>Закрытый сторож</h1>
<form method="post" enctype="multipart/form-data">
<h2>Загрузить снимок</h2>
<p><label>Недельные данные <input type="file" name="series" accept=".json" required></label></p>
<p><label>Контекст и разрывы (необязательно) <input type="file" name="context" accept=".json"></label></p>
<button>Сохранить в моём пространстве</button>
</form>
<form method="post">
<p>Выберите последний загруженный снимок для обнаружения измерений и слепого анализа в чате.</p>
<button name="action" value="select_blind">Разрешить анализ этого снимка в чате</button>
<button name="action" value="unselect_blind">Отозвать доступ чата</button>
</form>
<form method="post">
<h2>Проверить историю или запустить ежедневно</h2>
<p><label>Целевой продукт <input name="product_key" required></label></p>
<p><label>Контрольный продукт <input name="control_product_key" required></label></p>
<p><label>Метрика <input name="metric_key" value="orders_index" required></label></p>
<p><label>Сегмент <input name="segment"></label></p>
<p><label>Начало прогона <input name="from" type="date"></label>
<label>Конец <input name="to" type="date"></label>
<label>Дата среза <input name="as_of" type="date"></label></p>
<button name="action" value="replay">Проверить историю</button>
<p><label><input type="checkbox" name="consent" value="yes"> Разрешаю ежедневно проверять мои загруженные данные в 06:00 UTC.</label></p>
<button name="action" value="start">Включить ежедневный запуск</button>
</form>
<form method="post"><button name="action" value="stop">Остановить ежедневный запуск</button></form>`

func (s *privateWatch) submit(w http.ResponseWriter, r *http.Request) error {
	owner, ok := s.browserOwner(w, r)
	if !ok {
		return nil
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if !s.privateAccess(w) {
			return nil
		}
		defer func() { <-s.privateHTTP }()
		r.Body = http.MaxBytesReader(w, r.Body, 2*privateFileMax+(64<<10))
		pair, err := readWatchPair(r)
		if err != nil {
			http.Error(w, "Загрузите JSON ряда и, при наличии, контекст. До 8 МиБ на файл.", http.StatusBadRequest)
			return nil
		}
		if err := validateWatchPair(r.Context(), pair); err != nil {
			http.Error(w, "Несовместимые снимки ряда и контекста.", http.StatusBadRequest)
			return nil
		}
		if err := s.storePair(r.Context(), owner, pair); err != nil {
			http.Error(w, "Не удалось сохранить снимок.", http.StatusServiceUnavailable)
			return nil
		}
		return replyWatch(w, http.StatusCreated, map[string]string{"status": "stored"})
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Неверная форма.", http.StatusBadRequest)
		return nil
	}
	ctx := r.Context()
	action := r.PostForm.Get("action")
	if action == "select_blind" || action == "unselect_blind" {
		on := action == "select_blind"
		result, err := s.db.ExecContext(ctx, `UPDATE watchdog_owners SET blind_selected=$2 WHERE owner_id=$1`, owner, on)
		if err != nil {
			http.Error(w, "Не удалось выбрать снимок.", http.StatusServiceUnavailable)
			return nil
		}
		count, err := result.RowsAffected()
		if err != nil || (on && count != 1) {
			http.Error(w, "Сначала загрузите снимок.", http.StatusConflict)
			return nil
		}
		state := "unselected"
		if on {
			state = "selected"
		}
		return replyWatch(w, http.StatusOK, map[string]string{"status": state})
	}
	if action == "stop" {
		if err := s.stop(ctx, owner); err != nil {
			http.Error(w, "Не удалось остановить расписание.", http.StatusServiceUnavailable)
			return nil
		}
		return replyWatch(w, http.StatusOK, map[string]string{"status": "stopped"})
	}
	input := SignalInput{
		ProductKey: strings.TrimSpace(r.PostForm.Get("product_key")), ControlProductKey: strings.TrimSpace(r.PostForm.Get("control_product_key")),
		MetricKey: strings.TrimSpace(r.PostForm.Get("metric_key")), Segment: strings.TrimSpace(r.PostForm.Get("segment")),
		From: r.PostForm.Get("from"), To: r.PostForm.Get("to"), AsOf: r.PostForm.Get("as_of"),
		SourcePath: privateSeries, ContextPath: privateContext,
	}
	if input.ProductKey == "" || input.ControlProductKey == "" || input.ProductKey == input.ControlProductKey || input.MetricKey == "" {
		http.Error(w, "Укажите метрику и разные ключи цели и контроля.", http.StatusBadRequest)
		return nil
	}
	switch action {
	case "start":
		if r.PostForm.Get("consent") != "yes" {
			http.Error(w, "Для ежедневного запуска требуется согласие.", http.StatusBadRequest)
			return nil
		}
		id, err := s.start(ctx, owner, input)
		if err != nil {
			http.Error(w, "Сначала загрузите снимок. При сбое проверьте состояние задания в Airlock.", http.StatusConflict)
			return nil
		}
		return replyWatch(w, http.StatusAccepted, map[string]string{"job_id": id})
	case "replay":
		if !s.privateAccess(w) {
			return nil
		}
		defer func() { <-s.privateHTTP }()
		var batch SignalBatch
		err := s.db.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
			var snapshot uuid.UUID
			if err := tx.QueryRowContext(ctx, `SELECT snapshot_id FROM watchdog_owners WHERE owner_id=$1 FOR SHARE`, owner).Scan(&snapshot); err != nil {
				return err
			}
			w := s.snapshotWatch(owner, snapshot)
			w.datasetID, w.record = snapshot.String(), firstSeenIn(tx, owner)
			var err error
			batch, err = w.Replay(ctx, input)
			return err
		})
		if err != nil {
			http.Error(w, "Проверьте даты прогона и доступность снимка.", http.StatusBadRequest)
			return nil
		}
		return replyWatch(w, http.StatusOK, watchSummary(batch))
	default:
		http.Error(w, "Неизвестное действие.", http.StatusBadRequest)
		return nil
	}
}

func (s *privateWatch) privateAccess(w http.ResponseWriter) bool {
	select {
	case s.privateHTTP <- struct{}{}:
		return true
	default:
		http.Error(w, "Дождитесь завершения текущей операции.", http.StatusTooManyRequests)
		return false
	}
}

func replyWatch(w http.ResponseWriter, status int, data any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(data)
}

type watchPair map[string][]byte

func readWatchPair(r *http.Request) (watchPair, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	files := watchPair{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name := map[string]string{"series": privateSeries, "context": privateContext}[part.FormName()]
		if name == "" || part.FileName() == "" || files[name] != nil {
			return nil, errors.New("unexpected upload part")
		}
		b, err := io.ReadAll(io.LimitReader(part, privateFileMax+1))
		if err != nil || len(b) == 0 || len(b) > privateFileMax {
			return nil, errors.New("invalid upload size")
		}
		files[name] = b
	}
	if files[privateSeries] == nil {
		return nil, errors.New("a numeric series is required")
	}
	return files, nil
}

func validateWatchPair(ctx context.Context, pair watchPair) error {
	w := Watch{series: Service{read: func(_ context.Context, name string) ([]byte, error) {
		return pair[name], nil
	}}}
	input, err := w.series.historical(ctx, privateSeries)
	if err != nil {
		return err
	}
	if (input.Status != "ok" && input.Status != "partial") || len(input.Observations) == 0 {
		return errors.New("expected a nonempty imported series")
	}
	if pair[privateContext] == nil {
		synthetic, known := input.Observations[0].Details["synthetic"].(bool)
		if !known {
			return errors.New("series must declare its origin")
		}
		pair[privateContext], err = json.Marshal(WatchContext{
			SchemaVersion: "aifence.watch-context.v1", Synthetic: &synthetic, Batches: map[string]ContextBatch{},
		})
		if err != nil {
			return err
		}
	}
	catalog, err := w.loadContext(ctx, privateContext)
	if err != nil {
		return err
	}
	for _, batch := range catalog.Batches {
		if batch.AsOf != input.AsOf {
			return errors.New("series and context have different cutoff timestamps")
		}
	}
	for _, row := range input.Observations {
		if synthetic, known := row.Details["synthetic"].(bool); !known || synthetic != *catalog.Synthetic {
			return errors.New("series and context have different origins")
		}
	}
	return nil
}

func watchSnapshotPath(owner, snapshot uuid.UUID, name string) string {
	return fmt.Sprintf("%s/user-%s/%s/%s.gz", privateWatchDir, owner, snapshot, name)
}

func (s *privateWatch) snapshotWatch(owner, snapshot uuid.UUID) Watch {
	return Watch{
		series: Service{read: func(ctx context.Context, name string) ([]byte, error) {
			if name != privateSeries && name != privateContext {
				return nil, errors.New("unknown snapshot input")
			}
			f, err := s.agent.OpenFile(ctx, watchSnapshotPath(owner, snapshot, name))
			if err != nil {
				return nil, err
			}
			defer f.Close()
			z, err := gzip.NewReader(f)
			if err != nil {
				return nil, err
			}
			defer z.Close()
			return io.ReadAll(io.LimitReader(z, privateFileMax+1))
		}, config: func(context.Context) Settings { return Settings{} }},
		contextPath: func(context.Context) string { return "" },
	}
}

func (s *privateWatch) storePair(ctx context.Context, owner uuid.UUID, pair watchPair) error {
	snapshot := uuid.New()
	// Publish the pointer only after both immutable inputs have reached SDK storage.
	for _, name := range []string{privateSeries, privateContext} {
		if err := s.writePrivateFile(ctx, watchSnapshotPath(owner, snapshot, name), pair[name]); err != nil {
			s.deletePair(ctx, owner, snapshot)
			return err
		}
	}
	var old uuid.UUID
	err := s.db.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO watchdog_owners(owner_id, snapshot_id) VALUES ($1, $2)
		    ON CONFLICT(owner_id) DO NOTHING`, owner, snapshot); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT snapshot_id FROM watchdog_owners WHERE owner_id=$1 FOR UPDATE`, owner).
			Scan(&old); err != nil {
			return err
		}
		if old == snapshot {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE watchdog_owners SET snapshot_id=$2, blind_selected=false WHERE owner_id=$1`, owner, snapshot)
		return err
	})
	if err == nil && old != snapshot {
		s.deletePair(ctx, owner, old)
	}
	return err
}

func (s *privateWatch) deletePair(ctx context.Context, owner, snapshot uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, file := range []string{privateSeries, privateContext} {
		_ = s.agent.DeleteFile(ctx, watchSnapshotPath(owner, snapshot, file))
	}
}

func (s *privateWatch) writePrivateFile(ctx context.Context, path string, data []byte) error {
	// Store an opaque archive so Airlock's text indexer cannot extract the private JSON.
	var buf bytes.Buffer
	z, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return err
	}
	if _, err := z.Write(data); err != nil {
		return err
	}
	if err := z.Close(); err != nil {
		return err
	}
	_, err = s.agent.WriteFile(ctx, path, &buf, "application/gzip")
	return err
}

func (s *privateWatch) start(ctx context.Context, owner uuid.UUID, input SignalInput) (string, error) {
	id := uuid.NewString()
	err := s.db.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		var pending sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT next_job_id::text FROM watchdog_owners WHERE owner_id=$1 FOR UPDATE`, owner).
			Scan(&pending); err != nil {
			return err
		}
		if pending.Valid {
			existing, err := s.job.Get(ctx, pending.String)
			if err != nil {
				return err
			}
			if existing.Status == agentsdk.JobStatusQueued || existing.Status == agentsdk.JobStatusRunning {
				id = pending.String
				return nil
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE watchdog_owners SET product_key=$2, control_product_key=$3,
		    metric_key=$4, segment=$5, next_job_id=$6 WHERE owner_id=$1`,
			owner, input.ProductKey, input.ControlProductKey, input.MetricKey, input.Segment, id)
		if err != nil {
			return err
		}
		if _, err := s.job.EnqueueAt(ctx, id, time.Now().UTC().Add(2*time.Second), watchdogInput{Mode: "owner"}); err != nil {
			if _, getErr := s.job.Get(ctx, id); getErr != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

func (s *privateWatch) stop(ctx context.Context, owner uuid.UUID) error {
	var pending sql.NullString
	err := s.db.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		err := tx.QueryRowContext(ctx, `SELECT next_job_id::text FROM watchdog_owners WHERE owner_id=$1 FOR UPDATE`, owner).Scan(&pending)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE watchdog_owners SET next_job_id=NULL WHERE owner_id=$1`, owner)
		return err
	})
	if err == nil && pending.Valid {
		_ = s.job.Cancel(ctx, pending.String)
	}
	return err
}
