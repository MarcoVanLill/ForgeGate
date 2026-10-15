package storage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MarcoVanLill/ForgeGate/internal/providers"
	"github.com/MarcoVanLill/ForgeGate/migrations"
	"github.com/exaring/otelpgx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrBudgetExceeded = errors.New("tenant budget exceeded")
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnLifetime = 30 * time.Minute
	config.ConnConfig.Tracer = otelpgx.NewTracer()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) Migrate(ctx context.Context) error {
	entries, err := fs.Glob(migrations.FS, "*.up.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	for _, name := range entries {
		versionText := strings.SplitN(name, "_", 2)[0]
		version, err := strconv.ParseInt(versionText, 10, 64)
		if err != nil {
			return fmt.Errorf("parse migration %s: %w", name, err)
		}
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		// Serialize migrators across rolling deploys, then re-check the version
		// while holding the transaction-scoped advisory lock.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(734732908314)`); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("lock migration %s: %w", name, err)
		}
		var applied bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&applied); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if applied {
			_ = tx.Rollback(ctx)
			continue
		}
		if _, err = tx.Exec(ctx, string(sql)); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

type Tenant struct {
	ID                      uuid.UUID `json:"id"`
	Name                    string    `json:"name"`
	RequestsPerMinute       int       `json:"requests_per_minute"`
	MonthlyRequestBudget    int64     `json:"monthly_request_budget"`
	MonthlyCostBudgetMicros int64     `json:"monthly_cost_budget_micros"`
}

func HashAPIKey(key string) [32]byte { return sha256.Sum256([]byte(key)) }

func (s *Store) Authenticate(ctx context.Context, hash [32]byte) (Tenant, error) {
	var tenant Tenant
	err := s.pool.QueryRow(ctx, `
		UPDATE api_keys k SET last_used_at=now()
		FROM tenants t
		WHERE k.key_hash=$1 AND k.tenant_id=t.id AND k.enabled AND t.enabled
		  AND (k.expires_at IS NULL OR k.expires_at > now())
		RETURNING t.id,t.name,t.requests_per_minute,t.monthly_request_budget,t.monthly_cost_budget_micros`, hash[:]).Scan(
		&tenant.ID, &tenant.Name, &tenant.RequestsPerMinute, &tenant.MonthlyRequestBudget, &tenant.MonthlyCostBudgetMicros)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tenant{}, ErrNotFound
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("authenticate key: %w", err)
	}
	return tenant, nil
}

type ProviderConfig struct {
	Name           string
	Kind           string
	BaseURL        string
	APIKeyEnv      string
	Priority       int
	Timeout        time.Duration
	MaxRetries     int
	MaxConcurrency int
	Settings       json.RawMessage
}

func (s *Store) ProviderConfigs(ctx context.Context, tenantID uuid.UUID, model string) ([]ProviderConfig, error) {
	rows, err := s.pool.Query(ctx, `SELECT name,kind,COALESCE(base_url,''),COALESCE(api_key_env,''),priority,timeout_ms,max_retries,max_concurrency,settings FROM provider_configs WHERE tenant_id=$1 AND enabled AND ($2 LIKE model_prefix || '%' OR model_prefix='') ORDER BY priority,name`, tenantID, model)
	if err != nil {
		return nil, fmt.Errorf("query provider configs: %w", err)
	}
	defer rows.Close()
	var result []ProviderConfig
	for rows.Next() {
		var c ProviderConfig
		var timeoutMS int
		if err := rows.Scan(&c.Name, &c.Kind, &c.BaseURL, &c.APIKeyEnv, &c.Priority, &timeoutMS, &c.MaxRetries, &c.MaxConcurrency, &c.Settings); err != nil {
			return nil, fmt.Errorf("scan provider config: %w", err)
		}
		c.Timeout = time.Duration(timeoutMS) * time.Millisecond
		result = append(result, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider configs: %w", err)
	}
	return result, nil
}

func (s *Store) ReserveRequest(ctx context.Context, tenant Tenant, estimatedCostMicros int64) error {
	return reserveRequest(ctx, s.pool, tenant, estimatedCostMicros)
}

type budgetQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func reserveRequest(ctx context.Context, q budgetQuerier, tenant Tenant, estimatedCostMicros int64) error {
	month := time.Now().UTC().Format("2006-01-01")
	var count int64
	err := q.QueryRow(ctx, `
		INSERT INTO tenant_usage_monthly(tenant_id,month,request_count,cost_micros) VALUES($1,$2,1,$3)
		ON CONFLICT(tenant_id,month) DO UPDATE SET request_count=tenant_usage_monthly.request_count+1,cost_micros=tenant_usage_monthly.cost_micros+EXCLUDED.cost_micros,updated_at=now()
		WHERE ($4=0 OR tenant_usage_monthly.request_count+1 <= $4)
		  AND ($5=0 OR (CASE WHEN EXCLUDED.cost_micros=0 THEN tenant_usage_monthly.cost_micros < $5 ELSE tenant_usage_monthly.cost_micros+EXCLUDED.cost_micros <= $5 END))
		RETURNING request_count`, tenant.ID, month, estimatedCostMicros, tenant.MonthlyRequestBudget, tenant.MonthlyCostBudgetMicros).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBudgetExceeded
	}
	if err != nil {
		return fmt.Errorf("reserve tenant budget: %w", err)
	}
	return nil
}

type RequestRecord struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	RequestID  string
	Model      string
	Input      string
	Provider   string
	Outcome    string
	StatusCode int
	Latency    time.Duration
	ErrorCode  string
	Usage      providers.Usage
}

func (s *Store) RecordRequest(ctx context.Context, record RequestRecord) error {
	inputHash := sha256.Sum256([]byte(record.Input))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin request record: %w", err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO request_records(id,tenant_id,request_id,model,input_hash,selected_provider,outcome,status_code,latency_ms,error_code) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,NULLIF($10,''))`, record.ID, record.TenantID, record.RequestID, record.Model, inputHash[:], record.Provider, record.Outcome, record.StatusCode, record.Latency.Milliseconds(), record.ErrorCode)
	if err != nil {
		return fmt.Errorf("insert request record: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO usage_records(request_record_id,tenant_id,input_tokens,output_tokens,total_tokens,cost_micros) VALUES($1,$2,$3,$4,$5,$6)`, record.ID, record.TenantID, record.Usage.InputTokens, record.Usage.OutputTokens, record.Usage.TotalTokens, record.Usage.CostMicros)
	if err != nil {
		return fmt.Errorf("insert usage record: %w", err)
	}
	if record.Usage.CostMicros != 0 {
		_, err = tx.Exec(ctx, `UPDATE tenant_usage_monthly SET cost_micros=cost_micros+$1,updated_at=now() WHERE tenant_id=$2 AND month=$3`, record.Usage.CostMicros, record.TenantID, time.Now().UTC().Format("2006-01-01"))
		if err != nil {
			return fmt.Errorf("update cost budget: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit request record: %w", err)
	}
	return nil
}

type Job struct {
	ID                    uuid.UUID           `json:"id"`
	TenantID              uuid.UUID           `json:"-"`
	IdempotencyKey        string              `json:"-"`
	Status                string              `json:"status"`
	Request               providers.Request   `json:"-"`
	Result                *providers.Response `json:"result,omitempty"`
	SelectedProvider      string              `json:"selected_provider,omitempty"`
	ErrorCode             string              `json:"error_code,omitempty"`
	ErrorMessage          string              `json:"error_message,omitempty"`
	Attempts              int                 `json:"attempts"`
	MaxAttempts           int                 `json:"max_attempts"`
	CancellationRequested bool                `json:"cancellation_requested"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
	StartedAt             *time.Time          `json:"started_at,omitempty"`
	CompletedAt           *time.Time          `json:"completed_at,omitempty"`
}

func (s *Store) CreateJob(ctx context.Context, tenant Tenant, key string, request providers.Request) (Job, bool, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return Job{}, false, fmt.Errorf("encode job request: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Job{}, false, fmt.Errorf("begin create job: %w", err)
	}
	defer tx.Rollback(ctx)
	job := Job{ID: uuid.New(), TenantID: tenant.ID, IdempotencyKey: key, Status: "queued", Request: request, MaxAttempts: 5}
	var insertedID uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO jobs(id,tenant_id,idempotency_key,status,request,max_attempts) VALUES($1,$2,$3,'queued',$4,5) ON CONFLICT(tenant_id,idempotency_key) DO NOTHING RETURNING id`, job.ID, tenant.ID, key, payload).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, getErr := getJob(ctx, tx, tenant.ID, uuid.Nil, key)
		if getErr != nil {
			return Job{}, false, getErr
		}
		if err := tx.Commit(ctx); err != nil {
			return Job{}, false, fmt.Errorf("commit duplicate job lookup: %w", err)
		}
		return existing, true, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("insert job: %w", err)
	}
	if err := reserveRequest(ctx, tx, tenant, 0); err != nil {
		return Job{}, false, err
	}
	eventPayload, _ := json.Marshal(map[string]any{"job_id": job.ID, "tenant_id": tenant.ID})
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,payload) VALUES('job',$1,'job.created',$2)`, job.ID, eventPayload); err != nil {
		return Job{}, false, fmt.Errorf("insert job outbox event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, false, fmt.Errorf("commit create job: %w", err)
	}
	job.CreatedAt = time.Now().UTC()
	job.UpdatedAt = job.CreatedAt
	return job, false, nil
}

// SetProviderEnabled is the control-plane mutation boundary. It records the
// actor and request correlation in the same transaction as the sensitive change.
func (s *Store) SetProviderEnabled(ctx context.Context, tenantID uuid.UUID, providerName string, enabled bool, actor, requestID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin provider update: %w", err)
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE provider_configs SET enabled=$3,updated_at=now() WHERE tenant_id=$1 AND name=$2`, tenantID, providerName, enabled)
	if err != nil {
		return fmt.Errorf("update provider: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	metadata, _ := json.Marshal(map[string]any{"enabled": enabled})
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor,action,target_type,target_id,request_id,metadata) VALUES($1,$2,'provider.enabled_changed','provider',$3,NULLIF($4,''),$5)`, tenantID, actor, providerName, requestID, metadata); err != nil {
		return fmt.Errorf("audit provider update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit provider update: %w", err)
	}
	return nil
}

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getJob(ctx context.Context, q querier, tenantID, id uuid.UUID, key string) (Job, error) {
	query := `SELECT id,tenant_id,idempotency_key,status,request,result,COALESCE(selected_provider,''),COALESCE(error_code,''),COALESCE(error_message,''),attempts,max_attempts,cancellation_requested,created_at,updated_at,started_at,completed_at FROM jobs WHERE tenant_id=$1 AND id=$2`
	args := []any{tenantID, id}
	if id == uuid.Nil {
		query = strings.Replace(query, "id=$2", "idempotency_key=$2", 1)
		args = []any{tenantID, key}
	}
	var job Job
	var reqJSON []byte
	var resultJSON []byte
	err := q.QueryRow(ctx, query, args...).Scan(&job.ID, &job.TenantID, &job.IdempotencyKey, &job.Status, &reqJSON, &resultJSON, &job.SelectedProvider, &job.ErrorCode, &job.ErrorMessage, &job.Attempts, &job.MaxAttempts, &job.CancellationRequested, &job.CreatedAt, &job.UpdatedAt, &job.StartedAt, &job.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("get job: %w", err)
	}
	if err := json.Unmarshal(reqJSON, &job.Request); err != nil {
		return Job{}, fmt.Errorf("decode job request: %w", err)
	}
	if len(resultJSON) > 0 && string(resultJSON) != "null" {
		var result providers.Response
		if err := json.Unmarshal(resultJSON, &result); err != nil {
			return Job{}, fmt.Errorf("decode job result: %w", err)
		}
		job.Result = &result
	}
	return job, nil
}
func (s *Store) GetJob(ctx context.Context, tenantID, id uuid.UUID) (Job, error) {
	return getJob(ctx, s.pool, tenantID, id, "")
}

func (s *Store) CancelJob(ctx context.Context, tenantID, id uuid.UUID) (Job, error) {
	command, err := s.pool.Exec(ctx, `UPDATE jobs SET cancellation_requested=true,status=CASE WHEN status IN ('queued','retrying') THEN 'cancelled' ELSE status END,completed_at=CASE WHEN status IN ('queued','retrying') THEN now() ELSE completed_at END,updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status NOT IN ('completed','failed','cancelled')`, tenantID, id)
	if err != nil {
		return Job{}, fmt.Errorf("cancel job: %w", err)
	}
	if command.RowsAffected() == 0 {
		if _, getErr := s.GetJob(ctx, tenantID, id); errors.Is(getErr, ErrNotFound) {
			return Job{}, ErrNotFound
		}
	}
	return s.GetJob(ctx, tenantID, id)
}

type OutboxEvent struct {
	ID          uuid.UUID
	AggregateID uuid.UUID
	EventType   string
	Payload     []byte
	Attempts    int
}

func (s *Store) ClaimOutbox(ctx context.Context, limit int) ([]OutboxEvent, error) {
	rows, err := s.pool.Query(ctx, `UPDATE outbox_events SET locked_until=now()+interval '30 seconds',attempts=attempts+1 WHERE id IN (SELECT id FROM outbox_events WHERE published_at IS NULL AND next_attempt_at<=now() AND (locked_until IS NULL OR locked_until<now()) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1) RETURNING id,aggregate_id,event_type,payload,attempts`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim outbox: %w", err)
	}
	defer rows.Close()
	var events []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.AggregateID, &e.EventType, &e.Payload, &e.Attempts); err != nil {
			return nil, fmt.Errorf("scan outbox: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
func (s *Store) MarkOutboxPublished(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox_events SET published_at=now(),locked_until=NULL,last_error=NULL WHERE id=$1`, id)
	return err
}
func (s *Store) MarkOutboxFailed(ctx context.Context, id uuid.UUID, cause string, attempts int) error {
	delay := time.Duration(1<<min(attempts, 6)) * time.Second
	_, err := s.pool.Exec(ctx, `UPDATE outbox_events SET locked_until=NULL,next_attempt_at=now()+$2::interval,last_error=$3 WHERE id=$1`, id, delay.String(), truncate(cause, 500))
	return err
}

func (s *Store) ClaimJob(ctx context.Context, id uuid.UUID) (Job, bool, error) {
	command, err := s.pool.Exec(ctx, `UPDATE jobs SET status='running',attempts=attempts+1,started_at=COALESCE(started_at,now()),updated_at=now() WHERE id=$1 AND status IN ('queued','retrying') AND NOT cancellation_requested`, id)
	if err != nil {
		return Job{}, false, fmt.Errorf("claim job: %w", err)
	}
	var tenantID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT tenant_id FROM jobs WHERE id=$1`, id).Scan(&tenantID); errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, ErrNotFound
	} else if err != nil {
		return Job{}, false, err
	}
	job, err := s.GetJob(ctx, tenantID, id)
	return job, command.RowsAffected() == 1, err
}
func (s *Store) CompleteJob(ctx context.Context, id uuid.UUID, provider string, response providers.Response) error {
	payload, _ := json.Marshal(response)
	command, err := s.pool.Exec(ctx, `UPDATE jobs SET status=CASE WHEN cancellation_requested THEN 'cancelled' ELSE 'completed' END,result=CASE WHEN cancellation_requested THEN NULL ELSE $2::jsonb END,selected_provider=$3,completed_at=now(),updated_at=now() WHERE id=$1 AND status='running'`, id, payload, provider)
	if err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) FailJob(ctx context.Context, id uuid.UUID, code, message string, retry bool) error {
	status := "failed"
	if retry {
		status = "retrying"
	}
	_, err := s.pool.Exec(ctx, `UPDATE jobs SET status=$2,error_code=$3,error_message=$4,completed_at=CASE WHEN $2='failed' THEN now() ELSE NULL END,updated_at=now() WHERE id=$1 AND status='running'`, id, status, code, truncate(message, 500))
	return err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
