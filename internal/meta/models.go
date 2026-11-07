package meta

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The schema itself is owned by the idempotent SQL under migrations/. These
// structs describe shape only; auto-migration is never the source of truth.

type ConceptMeta struct {
	ConceptID   string    `gorm:"primaryKey;size:512" json:"concept_id"`
	ContentHash string    `gorm:"size:64;not null;index" json:"content_hash"`
	CommitSHA   string    `gorm:"size:64" json:"commit_sha"`
	Visibility  string    `gorm:"size:32;not null" json:"visibility"`
	Owner       string    `gorm:"size:128" json:"owner"`
	Status      string    `gorm:"size:32;not null" json:"status"`
	CreatedAt   time.Time `gorm:"index" json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (ConceptMeta) TableName() string { return "concepts_meta" }

type Projection struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	ConceptID   string    `gorm:"size:512;not null;uniqueIndex:idx_projection_target" json:"concept_id"`
	Adapter     string    `gorm:"size:64;not null;uniqueIndex:idx_projection_target" json:"adapter"`
	ContentHash string    `gorm:"size:64;not null" json:"content_hash"`
	Fingerprint string    `gorm:"size:256" json:"fingerprint"`
	Status      string    `gorm:"size:32;not null;index" json:"status"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (Projection) TableName() string { return "projections" }

type DecisionRecord struct {
	ID         uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	ConceptID  string     `gorm:"size:512;index" json:"concept_id"`
	RuleID     string     `gorm:"size:128" json:"rule_id"`
	Reason     string     `gorm:"type:text" json:"reason"`
	Source     string     `gorm:"size:32" json:"source"`
	Confidence float64    `json:"confidence"`
	Targets    StringList `gorm:"type:jsonb" json:"targets"`
	CreatedAt  time.Time  `gorm:"index" json:"created_at"`
}

func (DecisionRecord) TableName() string { return "decisions" }

type Cursor struct {
	Job           string    `gorm:"primaryKey;size:128" json:"job"`
	LastConceptID string    `gorm:"size:512" json:"last_concept_id"`
	Done          bool      `json:"done"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (Cursor) TableName() string { return "migration_cursors" }

type ACLEntry struct {
	Subject   string `gorm:"primaryKey;size:128" json:"subject"`
	ConceptID string `gorm:"primaryKey;size:512" json:"concept_id"`
	Allow     bool   `json:"allow"`
}

func (ACLEntry) TableName() string { return "acl" }

type EngineFingerprint struct {
	Adapter     string    `gorm:"primaryKey;size:64" json:"adapter"`
	Fingerprint string    `gorm:"size:256;not null" json:"fingerprint"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (EngineFingerprint) TableName() string { return "engine_fingerprints" }

// StringList is a JSONB-backed []string (Valuer/Scanner pair).
type StringList []string

func (l StringList) Value() (driver.Value, error) {
	if l == nil {
		return "[]", nil
	}
	return json.Marshal(l)
}

func (l *StringList) Scan(src any) error {
	if src == nil {
		*l = nil
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("meta: cannot scan %T into StringList", src)
	}
	return json.Unmarshal(b, l)
}

// Vector is a pgvector literal type. GORM has no native vector support, so the
// adapter uses this Valuer/Scanner plus parameterised native SQL.
type Vector []float32

func (v Vector) Value() (driver.Value, error) {
	if len(v) == 0 {
		return nil, errors.New("meta: empty vector")
	}
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.FormatFloat(float64(x), 'f', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}

func (v *Vector) Scan(src any) error {
	var s string
	switch t := src.(type) {
	case string:
		s = t
	case []byte:
		s = string(t)
	case nil:
		*v = nil
		return nil
	default:
		return fmt.Errorf("meta: cannot scan %T into Vector", src)
	}
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		*v = Vector{}
		return nil
	}
	var out Vector
	for _, part := range strings.Split(s, ",") {
		f, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return fmt.Errorf("meta: bad vector element %q: %w", part, err)
		}
		out = append(out, float32(f))
	}
	*v = out
	return nil
}
