package meta

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// Options configures the PostgreSQL/GORM connection. DSN is injected at runtime.
type Options struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	LogLevel        string
}

// PostgresStore is the GORM-backed MetaStore. gorm stays inside this package.
type PostgresStore struct {
	db *gorm.DB
}

func gormLogLevel(s string) logger.LogLevel {
	switch strings.ToLower(s) {
	case "silent":
		return logger.Silent
	case "error":
		return logger.Error
	case "info":
		return logger.Info
	default:
		return logger.Warn
	}
}

// OpenPostgres connects, configures the pool and applies versioned migrations.
func OpenPostgres(ctx context.Context, o Options) (*PostgresStore, error) {
	if strings.TrimSpace(o.DSN) == "" {
		return nil, errors.New("meta: postgres DSN is empty")
	}
	gl := logger.New(
		slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
		logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  gormLogLevel(o.LogLevel),
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      true,
			Colorful:                  false,
		},
	)
	db, err := gorm.Open(postgres.Open(o.DSN), &gorm.Config{Logger: gl})
	if err != nil {
		return nil, fmt.Errorf("meta: open postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if o.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(o.MaxOpenConns)
	}
	if o.MaxIdleConns >= 0 {
		sqlDB.SetMaxIdleConns(o.MaxIdleConns)
	}
	if o.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(o.ConnMaxLifetime)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("meta: ping postgres: %w", err)
	}
	s := &PostgresStore{db: db}
	if err := s.Migrate(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return s, nil
}

const bootstrapSQL = `CREATE TABLE IF NOT EXISTS schema_migrations (
    name TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// Migrate applies NNN_*.sql in filename order, once each, transactionally.
// The pgvector migration is best-effort: a missing extension must not prevent
// the metadata layer from starting.
func (s *PostgresStore) Migrate(ctx context.Context) error {
	if err := s.db.WithContext(ctx).Exec(bootstrapSQL).Error; err != nil {
		return fmt.Errorf("meta: bootstrap schema_migrations: %w", err)
	}
	files, err := migrationFiles()
	if err != nil {
		return err
	}
	for _, name := range files {
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", name).Error; err != nil {
				return err
			}
			var count int64
			if err := tx.Table("schema_migrations").Where("name = ?", name).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return nil
			}
			raw, err := migrationFS.ReadFile(name)
			if err != nil {
				return err
			}
			for _, stmt := range splitStatements(string(raw)) {
				if err := tx.Exec(stmt).Error; err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
			return tx.Exec("INSERT INTO schema_migrations (name) VALUES (?)", name).Error
		})
		if err != nil {
			if strings.Contains(name, "pgvector") {
				slog.Warn("meta: optional pgvector migration skipped", "file", name, "error", err)
				continue
			}
			return fmt.Errorf("meta: migration %s: %w", name, err)
		}
	}
	return nil
}

func splitStatements(sql string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
	}
	for _, part := range strings.Split(cur.String(), ";") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (s *PostgresStore) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (s *PostgresStore) UpsertConcept(ctx context.Context, m ConceptMeta) error {
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "concept_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"content_hash", "commit_sha", "visibility", "owner", "status", "updated_at"}),
	}).Create(&m).Error
}

func (s *PostgresStore) GetConcept(ctx context.Context, id string) (ConceptMeta, error) {
	var m ConceptMeta
	err := s.db.WithContext(ctx).Where("concept_id = ?", id).First(&m).Error
	return m, err
}

func (s *PostgresStore) ListConcepts(ctx context.Context, limit int) ([]ConceptMeta, error) {
	if limit <= 0 {
		limit = 100
	}
	var out []ConceptMeta
	err := s.db.WithContext(ctx).Order("concept_id").Limit(limit).Find(&out).Error
	return out, err
}

func (s *PostgresStore) ListProjections(ctx context.Context, q ProjectionQuery) ([]Projection, error) {
	tx := s.db.WithContext(ctx).Model(&Projection{})
	if q.ConceptID != "" {
		tx = tx.Where("concept_id = ?", q.ConceptID)
	}
	if q.Adapter != "" {
		tx = tx.Where("adapter = ?", q.Adapter)
	}
	if q.Status != "" {
		tx = tx.Where("status = ?", q.Status)
	}
	if q.Limit > 0 {
		tx = tx.Limit(q.Limit)
	}
	var out []Projection
	err := tx.Order("concept_id, adapter").Find(&out).Error
	return out, err
}

func (s *PostgresStore) SetProjectionState(ctx context.Context, p Projection) error {
	p.UpdatedAt = time.Now().UTC()
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "concept_id"}, {Name: "adapter"}},
		DoUpdates: clause.AssignmentColumns([]string{"content_hash", "fingerprint", "status", "updated_at"}),
	}).Create(&p).Error
}

func (s *PostgresStore) AppendDecision(ctx context.Context, d DecisionRecord) error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if d.Targets == nil {
		d.Targets = StringList{}
	}
	return s.db.WithContext(ctx).Create(&d).Error
}

func (s *PostgresStore) ListDecisions(ctx context.Context, q DecisionQuery) ([]DecisionRecord, error) {
	tx := s.db.WithContext(ctx).Model(&DecisionRecord{})
	if q.ConceptID != "" {
		tx = tx.Where("concept_id = ?", q.ConceptID)
	}
	if q.Limit > 0 {
		tx = tx.Limit(q.Limit)
	}
	var out []DecisionRecord
	err := tx.Order("id DESC").Find(&out).Error
	return out, err
}

func (s *PostgresStore) MigrationCursor(ctx context.Context, job string) (Cursor, error) {
	var c Cursor
	err := s.db.WithContext(ctx).Where("job = ?", job).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Cursor{Job: job}, nil
	}
	return c, err
}

func (s *PostgresStore) SaveMigrationCursor(ctx context.Context, job string, c Cursor) error {
	c.Job = job
	c.UpdatedAt = time.Now().UTC()
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "job"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_concept_id", "done", "updated_at"}),
	}).Create(&c).Error
}

func (s *PostgresStore) EngineFingerprint(ctx context.Context, adapter string) (string, error) {
	var f EngineFingerprint
	err := s.db.WithContext(ctx).Where("adapter = ?", adapter).First(&f).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return f.Fingerprint, err
}

func (s *PostgresStore) SetEngineFingerprint(ctx context.Context, adapter, fingerprint string) error {
	f := EngineFingerprint{Adapter: adapter, Fingerprint: fingerprint, UpdatedAt: time.Now().UTC()}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "adapter"}},
		DoUpdates: clause.AssignmentColumns([]string{"fingerprint", "updated_at"}),
	}).Create(&f).Error
}

func (s *PostgresStore) PutACL(ctx context.Context, e ACLEntry) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "subject"}, {Name: "concept_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"allow"}),
	}).Create(&e).Error
}

func (s *PostgresStore) CheckACL(ctx context.Context, subject, conceptID string) (bool, error) {
	var e ACLEntry
	err := s.db.WithContext(ctx).Where("subject = ? AND concept_id = ?", subject, conceptID).First(&e).Error
	if err == nil {
		return e.Allow, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	return s.fallbackACL(ctx, subject, conceptID)
}

func (s *PostgresStore) fallbackACL(ctx context.Context, subject, conceptID string) (bool, error) {
	m, err := s.GetConcept(ctx, conceptID)
	if err != nil {
		return false, nil
	}
	return aclFromVisibility(m, subject), nil
}

func aclFromVisibility(m ConceptMeta, subject string) bool {
	switch m.Visibility {
	case "public":
		return true
	case "team":
		return subject != ""
	case "private":
		return subject != "" && subject == m.Owner
	default:
		return false
	}
}
