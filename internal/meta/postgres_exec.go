package meta

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Query executes a sanctioned, parameterised read in a read-only transaction.
// SQL text and bound values come from the OKF contract and the parameter
// binder; the logger is silenced for this session so GORM never emits SQL
// parameters, and rows are capped before they ever leave this layer.
func (s *PostgresStore) Query(ctx context.Context, statement string, args []any, maxRows int) (SQLRows, error) {
	if strings.TrimSpace(statement) == "" {
		return SQLRows{}, errors.New("meta: empty computation statement")
	}
	if maxRows <= 0 {
		maxRows = 100
	}
	out := SQLRows{Columns: []string{}, Values: [][]any{}}
	session := s.db.WithContext(ctx).Session(&gorm.Session{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	err := session.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET TRANSACTION READ ONLY").Error; err != nil {
			return fmt.Errorf("meta: read-only transaction: %w", err)
		}
		rows, err := tx.Raw(statement, args...).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return err
		}
		out.Columns = append(out.Columns, cols...)
		for rows.Next() {
			if len(out.Values) >= maxRows {
				break
			}
			cells := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range cells {
				ptrs[i] = &cells[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return err
			}
			row := make([]any, len(cells))
			for i, v := range cells {
				if b, ok := v.([]byte); ok {
					row[i] = string(b)
				} else {
					row[i] = v
				}
			}
			out.Values = append(out.Values, row)
		}
		return rows.Err()
	})
	if err != nil {
		return SQLRows{}, fmt.Errorf("meta: computation query: %w", err)
	}
	return out, nil
}
