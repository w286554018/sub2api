package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

type intelligentTestRepository struct {
	db *sql.DB
}

func NewIntelligentTestRepository(db *sql.DB) service.IntelligentTestRepository {
	return &intelligentTestRepository{db: db}
}

const intelligentTestRecordColumns = `t.id,t.account_id,t.test_type,t.status,t.score,t.result,t.result_image,t.input,t.raw_response,t.raw_truncated,t.error_message,t.duration_ms,t.model,t.config_snapshot,t.evaluation,t.requested_by,COALESCE(t.lease_token,''),t.available_at,t.queue_reason,t.started_at,t.finished_at,t.created_at`

type intelligentTestScanner interface{ Scan(...any) error }

func scanIntelligentTestRecord(row intelligentTestScanner) (*service.IntelligentTestRecord, error) {
	record := &service.IntelligentTestRecord{}
	var configBytes, evalBytes []byte
	if err := row.Scan(&record.ID, &record.AccountID, &record.TestType, &record.Status, &record.Score, &record.Result, &record.ResultImage, &record.Input, &record.RawResponse, &record.RawTruncated, &record.ErrorMessage, &record.DurationMS, &record.Model, &configBytes, &evalBytes, &record.RequestedBy, &record.LeaseToken, &record.AvailableAt, &record.QueueReason, &record.StartedAt, &record.FinishedAt, &record.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrIntelligentTestNotFound
		}
		return nil, err
	}
	if len(configBytes) > 0 {
		config := &service.IntelligentTestConfig{}
		if err := json.Unmarshal(configBytes, config); err != nil {
			return nil, err
		}
		record.ConfigSnapshot = config
	}
	if len(evalBytes) > 0 {
		if err := json.Unmarshal(evalBytes, &record.Evaluation); err != nil {
			return nil, err
		}
	}
	if record.Evaluation == nil {
		record.Evaluation = map[string]any{}
	}
	return record, nil
}

func readIntelligentTestRecords(rows *sql.Rows) ([]*service.IntelligentTestRecord, error) {
	defer rows.Close()
	var out []*service.IntelligentTestRecord
	for rows.Next() {
		record, err := scanIntelligentTestRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (r *intelligentTestRepository) IsAdmin(ctx context.Context, userID int64) (bool, error) {
	var ok bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND role IN ('admin','super_admin') AND status='active' AND deleted_at IS NULL)`, userID).Scan(&ok)
	return ok, err
}

func (r *intelligentTestRepository) IsSuperAdmin(ctx context.Context, userID int64) (bool, error) {
	var ok bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND role='super_admin' AND status='active' AND deleted_at IS NULL)`, userID).Scan(&ok)
	return ok, err
}

func (r *intelligentTestRepository) Settings(ctx context.Context) ([]service.IntelligentTestSetting, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT test_type,enabled,config,updated_at FROM test_settings ORDER BY test_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := []service.IntelligentTestSetting{}
	for rows.Next() {
		var setting service.IntelligentTestSetting
		var cfg []byte
		if err := rows.Scan(&setting.TestType, &setting.Enabled, &cfg, &setting.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(cfg, &setting.Config); err != nil {
			return nil, err
		}
		setting.Name = setting.TestType
		settings = append(settings, setting)
	}
	return settings, rows.Err()
}

func (r *intelligentTestRepository) UpdateSetting(ctx context.Context, actorID int64, setting *service.IntelligentTestSetting) error {
	cfg, err := json.Marshal(setting.Config)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE test_settings SET enabled=$2,config=$3,updated_at=NOW() WHERE test_type=$1`, setting.TestType, setting.Enabled, string(cfg))
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return service.ErrIntelligentTestNotFound
	}
	_ = insertIntelligentTestAudit(ctx, tx, actorID, "intelligent_test.setting.update", map[string]any{"test_type": setting.TestType})
	return tx.Commit()
}

func (r *intelligentTestRepository) Enqueue(ctx context.Context, actorID int64, req service.IntelligentTestEnqueue) (*service.IntelligentTestEnqueued, error) {
	fingerprint := intelligentRequestFingerprint(req)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if req.IdempotencyKey != "" {
		lockKey := fmt.Sprintf("%d:%s", actorID, req.IdempotencyKey)
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
			return nil, err
		}
		existing, found, err := r.lookupIdempotentRequestTx(ctx, tx, actorID, req.IdempotencyKey, fingerprint)
		if err != nil {
			return nil, err
		}
		if found {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			records, err := r.recordsByIDs(ctx, existing)
			if err != nil {
				return nil, err
			}
			return &service.IntelligentTestEnqueued{Records: records, ReusedCount: len(records), Reused: true}, nil
		}
	}
	settings, err := intelligentSettingsForUpdate(ctx, tx, req.TestTypes)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(req.AccountIDs)*len(req.TestTypes))
	created := 0
	reused := 0
	for _, accountID := range req.AccountIDs {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=$1 AND deleted_at IS NULL)`, accountID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, service.ErrAccountNotFound
		}
		for _, testType := range req.TestTypes {
			setting, ok := settings[testType]
			if !ok || !setting.Enabled {
				return nil, service.ErrIntelligentTestNotFound
			}
			cfg := setting.Config
			if override := strings.TrimSpace(req.Models[testType]); override != "" {
				cfg.Model = override
			}
			cfgBytes, err := json.Marshal(cfg)
			if err != nil {
				return nil, err
			}
			var id int64
			var inserted bool
			err = tx.QueryRowContext(ctx, `
				INSERT INTO account_tests(account_id,test_type,status,input,model,config_snapshot,evaluation,requested_by,available_at,created_at)
				VALUES($1,$2,'queued',$3,$4,$5::jsonb,'{}'::jsonb,$6,NOW(),NOW())
				ON CONFLICT (account_id,test_type) WHERE status IN ('queued','running') DO UPDATE SET queue_reason=account_tests.queue_reason
				RETURNING id,(xmax=0)`, accountID, testType, cfg.Prompt, cfg.Model, string(cfgBytes), actorID).Scan(&id, &inserted)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
			if inserted {
				created++
			} else {
				reused++
			}
		}
	}
	if req.IdempotencyKey != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO intelligent_test_requests(actor_id,request_key,fingerprint,record_ids,created_at) VALUES($1,$2,$3,$4,NOW()) ON CONFLICT(actor_id,request_key) DO NOTHING`, actorID, req.IdempotencyKey, fingerprint, pq.Array(ids))
		if err != nil {
			return nil, err
		}
	}
	_ = insertIntelligentTestAudit(ctx, tx, actorID, "intelligent_test.enqueue", map[string]any{"count": len(ids), "test_types": req.TestTypes})
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	records, err := r.recordsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return &service.IntelligentTestEnqueued{CreatedCount: created, ReusedCount: reused, Records: records}, nil
}

func (r *intelligentTestRepository) Accounts(ctx context.Context, filter service.IntelligentTestFilter) (*service.IntelligentTestAccounts, error) {
	filter = normalizeRepoIntelligentFilter(filter)
	where, args := intelligentAccountWhere(filter)
	out := &service.IntelligentTestAccounts{Page: filter.Page, PageSize: filter.PageSize}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts a WHERE `+where, args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	out.Overview.TotalAccounts = out.Total
	pageArgs := append(append([]any{}, args...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT a.id,a.name,COALESCE(a.notes,''),a.platform,a.type,a.status FROM accounts a WHERE `+where+fmt.Sprintf(` ORDER BY a.id DESC LIMIT $%d OFFSET $%d`, len(pageArgs)-1, len(pageArgs)), pageArgs...)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var card service.IntelligentTestAccountCard
		if err := rows.Scan(&card.AccountID, &card.Name, &card.Notes, &card.Platform, &card.AccountType, &card.AccountStatus); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, card.AccountID)
		out.Items = append(out.Items, card)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(ids) == 0 {
		return out, nil
	}
	latest, err := r.latestRecordsByAccounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	counts, err := r.recordCountsByAccounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	settings, err := r.Settings(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out.Items {
		card := &out.Items[i]
		for _, setting := range settings {
			if filter.TestType != "" && filter.TestType != setting.TestType {
				continue
			}
			key := intelligentRecordKey(card.AccountID, setting.TestType)
			card.Tests = append(card.Tests, service.IntelligentTestCardTest{TestType: setting.TestType, Latest: latest[key], HistoryCount: counts[key]})
		}
	}
	return out, nil
}

func (r *intelligentTestRepository) Records(ctx context.Context, filter service.IntelligentTestFilter) (*service.IntelligentTestRecords, error) {
	filter = normalizeRepoIntelligentFilter(filter)
	where, args := intelligentRecordWhere(filter)
	out := &service.IntelligentTestRecords{Page: filter.Page, PageSize: filter.PageSize}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_tests t WHERE `+where, args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	queryArgs := append(append([]any{}, args...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT `+intelligentTestRecordColumns+` FROM account_tests t WHERE `+where+fmt.Sprintf(` ORDER BY t.id DESC LIMIT $%d OFFSET $%d`, len(queryArgs)-1, len(queryArgs)), queryArgs...)
	if err != nil {
		return nil, err
	}
	records, err := readIntelligentTestRecords(rows)
	if err != nil {
		return nil, err
	}
	out.Items = records
	return out, nil
}

func (r *intelligentTestRepository) Get(ctx context.Context, id int64) (*service.IntelligentTestRecord, error) {
	return scanIntelligentTestRecord(r.db.QueryRowContext(ctx, `SELECT `+intelligentTestRecordColumns+` FROM account_tests t WHERE t.id=$1`, id))
}

func (r *intelligentTestRepository) Claim(ctx context.Context, leaseFor time.Duration) (*service.IntelligentTestRecord, error) {
	token := uuid.NewString()
	row := r.db.QueryRowContext(ctx, `
		WITH queue_lock AS (
			SELECT pg_advisory_xact_lock(240001)
		), expired AS (
			UPDATE account_tests
			SET status='failed', lease_token=NULL, lease_until=NULL, finished_at=NOW(),
				error_message='worker lease expired; test was not replayed'
			WHERE status='running' AND lease_until < NOW()
			RETURNING id
		), next AS (
			SELECT candidate.id FROM account_tests candidate, queue_lock
			WHERE candidate.status='queued' AND candidate.available_at <= NOW()
				AND NOT EXISTS (
					SELECT 1 FROM account_tests active
					WHERE active.account_id=candidate.account_id AND active.status='running'
				)
				AND (SELECT COUNT(*) FROM account_tests active WHERE active.status='running') < 4
			ORDER BY candidate.available_at ASC, candidate.id ASC
			FOR UPDATE OF candidate SKIP LOCKED
			LIMIT 1
		)
		UPDATE account_tests t SET status='running', lease_token=$1, lease_until=NOW()+$2::interval, started_at=COALESCE(started_at,NOW()), queue_reason=''
		FROM next WHERE t.id=next.id
		RETURNING `+intelligentTestRecordColumns, token, fmt.Sprintf("%d seconds", int(leaseFor.Seconds())))
	record, err := scanIntelligentTestRecord(row)
	if errors.Is(err, service.ErrIntelligentTestNotFound) {
		return nil, nil
	}
	return record, err
}

func (r *intelligentTestRepository) Finish(ctx context.Context, record *service.IntelligentTestRecord) error {
	if record == nil || record.LeaseToken == "" {
		return service.ErrIntelligentTestNotFound
	}
	cfg, eval, err := marshalIntelligentJSON(record.ConfigSnapshot, record.Evaluation)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE account_tests SET status=$3,score=$4,result=$5,result_image=$6,input=$7,raw_response=$8,raw_truncated=$9,error_message=$10,duration_ms=$11,model=$12,config_snapshot=$13::jsonb,evaluation=$14::jsonb,lease_token=NULL,lease_until=NULL,finished_at=NOW() WHERE id=$1 AND status='running' AND lease_token=$2`, record.ID, record.LeaseToken, record.Status, record.Score, record.Result, record.ResultImage, record.Input, record.RawResponse, record.RawTruncated, record.ErrorMessage, record.DurationMS, record.Model, cfg, eval)
	return intelligentLeaseWriteResult(result, err)
}

func (r *intelligentTestRepository) Defer(ctx context.Context, record *service.IntelligentTestRecord, availableAt time.Time, reason string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE account_tests SET status='queued',lease_token=NULL,lease_until=NULL,available_at=$3,queue_reason=$4 WHERE id=$1 AND status='running' AND lease_token=$2`, record.ID, record.LeaseToken, availableAt, reason)
	return intelligentLeaseWriteResult(result, err)
}

func (r *intelligentTestRepository) Cancel(ctx context.Context, actorID, id int64) (*service.IntelligentTestRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	record, err := scanIntelligentTestRecord(tx.QueryRowContext(ctx, `UPDATE account_tests t SET status='cancelled',lease_token=NULL,lease_until=NULL,finished_at=NOW(),error_message='cancelled by administrator' WHERE id=$1 AND status IN ('queued','running') RETURNING `+intelligentTestRecordColumns, id))
	if err != nil {
		return nil, err
	}
	_ = insertIntelligentTestAudit(ctx, tx, actorID, "intelligent_test.cancel", map[string]any{"id": id})
	return record, tx.Commit()
}

func (r *intelligentTestRepository) Reevaluate(ctx context.Context, actorID, id int64, fn func(*service.IntelligentTestRecord) error) (*service.IntelligentTestRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	record, err := scanIntelligentTestRecord(tx.QueryRowContext(ctx, `SELECT `+intelligentTestRecordColumns+` FROM account_tests t WHERE t.id=$1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}
	if !isIntelligentTestTerminal(record.Status) {
		return nil, service.ErrIntelligentTestConflict
	}
	if err := fn(record); err != nil {
		return nil, err
	}
	cfg, eval, err := marshalIntelligentJSON(record.ConfigSnapshot, record.Evaluation)
	if err != nil {
		return nil, err
	}
	updated, err := scanIntelligentTestRecord(tx.QueryRowContext(ctx, `UPDATE account_tests t SET status=$2,score=$3,result_image=$4,config_snapshot=$5::jsonb,evaluation=$6::jsonb WHERE id=$1 RETURNING `+intelligentTestRecordColumns, record.ID, record.Status, record.Score, record.ResultImage, cfg, eval))
	if err != nil {
		return nil, err
	}
	_ = insertIntelligentTestAudit(ctx, tx, actorID, "intelligent_test.reevaluate", map[string]any{"id": id})
	return updated, tx.Commit()
}

func intelligentSettingsForUpdate(ctx context.Context, tx *sql.Tx, testTypes []string) (map[string]service.IntelligentTestSetting, error) {
	rows, err := tx.QueryContext(ctx, `SELECT test_type,enabled,config,updated_at FROM test_settings WHERE test_type=ANY($1) FOR SHARE`, pq.Array(testTypes))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]service.IntelligentTestSetting{}
	for rows.Next() {
		var setting service.IntelligentTestSetting
		var cfg []byte
		if err := rows.Scan(&setting.TestType, &setting.Enabled, &cfg, &setting.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(cfg, &setting.Config); err != nil {
			return nil, err
		}
		out[setting.TestType] = setting
	}
	return out, rows.Err()
}

func (r *intelligentTestRepository) lookupIdempotentRequestTx(ctx context.Context, tx *sql.Tx, actorID int64, key, fingerprint string) ([]int64, bool, error) {
	var stored string
	var ids []int64
	err := tx.QueryRowContext(ctx, `SELECT fingerprint,record_ids FROM intelligent_test_requests WHERE actor_id=$1 AND request_key=$2`, actorID, key).Scan(&stored, pq.Array(&ids))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if stored != fingerprint {
		return nil, false, service.ErrIntelligentTestConflict
	}
	return ids, true, nil
}

func intelligentLeaseWriteResult(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return service.ErrIntelligentTestLeaseLost
	}
	return nil
}

func (r *intelligentTestRepository) recordsByIDs(ctx context.Context, ids []int64) ([]*service.IntelligentTestRecord, error) {
	if len(ids) == 0 {
		return []*service.IntelligentTestRecord{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+intelligentTestRecordColumns+` FROM account_tests t WHERE t.id=ANY($1) ORDER BY t.id ASC`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	return readIntelligentTestRecords(rows)
}

func (r *intelligentTestRepository) latestRecordsByAccounts(ctx context.Context, accountIDs []int64) (map[string]*service.IntelligentTestRecord, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+intelligentTestRecordColumns+` FROM account_tests t JOIN (SELECT DISTINCT ON(account_id,test_type) id FROM account_tests WHERE account_id=ANY($1) ORDER BY account_id,test_type,id DESC) latest ON latest.id=t.id`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	records, err := readIntelligentTestRecords(rows)
	if err != nil {
		return nil, err
	}
	out := map[string]*service.IntelligentTestRecord{}
	for _, record := range records {
		compactIntelligentRecord(record)
		out[intelligentRecordKey(record.AccountID, record.TestType)] = record
	}
	return out, nil
}

func (r *intelligentTestRepository) recordCountsByAccounts(ctx context.Context, accountIDs []int64) (map[string]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT account_id,test_type,COUNT(*) FROM account_tests WHERE account_id=ANY($1) GROUP BY account_id,test_type`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var accountID, count int64
		var testType string
		if err := rows.Scan(&accountID, &testType, &count); err != nil {
			return nil, err
		}
		out[intelligentRecordKey(accountID, testType)] = count
	}
	return out, rows.Err()
}

func intelligentAccountWhere(filter service.IntelligentTestFilter) (string, []any) {
	where := []string{"a.deleted_at IS NULL"}
	args := []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(condition, len(args)))
	}
	if filter.AccountID > 0 {
		add("a.id=$%d", filter.AccountID)
	}
	if filter.Platform != "" {
		add("a.platform=$%d", filter.Platform)
	}
	if filter.AccountType != "" {
		add("a.type=$%d", filter.AccountType)
	}
	if filter.AccountStatus != "" {
		add("a.status=$%d", filter.AccountStatus)
	}
	if filter.Search != "" {
		args = append(args, filter.Search)
		where = append(where, fmt.Sprintf("(a.name ILIKE '%%' || $%d || '%%' OR COALESCE(a.notes,'') ILIKE '%%' || $%d || '%%')", len(args), len(args)))
	}
	return strings.Join(where, " AND "), args
}

func intelligentRecordWhere(filter service.IntelligentTestFilter) (string, []any) {
	where := []string{"true"}
	args := []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(condition, len(args)))
	}
	if filter.AccountID > 0 {
		add("t.account_id=$%d", filter.AccountID)
	}
	if filter.TestType != "" {
		add("t.test_type=$%d", filter.TestType)
	}
	if filter.Status != "" {
		add("t.status=$%d", filter.Status)
	}
	if filter.From != nil {
		add("t.created_at >= $%d", *filter.From)
	}
	if filter.To != nil {
		add("t.created_at <= $%d", *filter.To)
	}
	return strings.Join(where, " AND "), args
}

func normalizeRepoIntelligentFilter(filter service.IntelligentTestFilter) service.IntelligentTestFilter {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 24
	}
	if filter.PageSize > 100 {
		filter.PageSize = 100
	}
	filter.Search = strings.TrimSpace(filter.Search)
	return filter
}

func intelligentRequestFingerprint(req service.IntelligentTestEnqueue) string {
	accountIDs := append([]int64{}, req.AccountIDs...)
	testTypes := append([]string{}, req.TestTypes...)
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })
	sort.Strings(testTypes)
	encoded, _ := json.Marshal(struct {
		AccountIDs []int64           `json:"account_ids"`
		TestTypes  []string          `json:"test_types"`
		Models     map[string]string `json:"models"`
	}{accountIDs, testTypes, req.Models})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func marshalIntelligentJSON(config *service.IntelligentTestConfig, evaluation map[string]any) (string, string, error) {
	if config == nil {
		config = &service.IntelligentTestConfig{}
	}
	if evaluation == nil {
		evaluation = map[string]any{}
	}
	cfg, err := json.Marshal(config)
	if err != nil {
		return "", "", err
	}
	eval, err := json.Marshal(evaluation)
	if err != nil {
		return "", "", err
	}
	return string(cfg), string(eval), nil
}

func compactIntelligentRecord(record *service.IntelligentTestRecord) {
	record.Input = ""
	record.RawResponse = ""
	record.ConfigSnapshot = nil
	if len([]rune(record.Result)) > 600 {
		record.Result = string([]rune(record.Result)[:600]) + "..."
	}
}

func intelligentRecordKey(accountID int64, testType string) string {
	return fmt.Sprintf("%d:%s", accountID, testType)
}

func isIntelligentTestTerminal(status string) bool {
	return status != service.IntelligentTestStatusQueued && status != service.IntelligentTestStatusRunning
}

func insertIntelligentTestAudit(ctx context.Context, tx *sql.Tx, actorID int64, action string, extra any) error {
	encoded, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_user_id,actor_email,actor_role,action,method,path,status_code,extra) SELECT id,email,role,$2,'POST','/admin/intelligent-tests',200,$3::jsonb FROM users WHERE id=$1`, actorID, action, string(encoded))
	return err
}
