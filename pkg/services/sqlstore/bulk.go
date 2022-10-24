package sqlstore

import "github.com/grafana/grafana/pkg/services/sqlstore/migrator"

const DefaultBatchSize = 1000

type BulkOpSettings struct {
	BatchSize int
}

func BulkSettingsForDialect(d migrator.Dialect) BulkOpSettings {
	return BulkOpSettings{
		BatchSize: d.BatchSize(),
	}
}

func normalizeBulkSettings(s BulkOpSettings) BulkOpSettings {
	if s.BatchSize == 0 {
		s.BatchSize = DefaultBatchSize
	}
	return s
}

// BulkInsert bulk-inserts many items to a table, in batches.
func (sess *DBSession) BulkInsert(table interface{}, records []interface{}, opts BulkOpSettings) (int64, error) {
	var affected int64
	err := inBatches(records, opts, func(batch []interface{}) error {
		a, err := sess.Table(table).InsertMulti(batch)
		affected += a
		return err
	})
	return affected, err
}

func inBatches(records []interface{}, opts BulkOpSettings, fn func(batch []interface{}) error) error {
	opts = normalizeBulkSettings(opts)
	for i := 0; i < len(records); i += opts.BatchSize {
		end := i + opts.BatchSize
		if end > len(records) {
			end = len(records)
		}

		if err := fn(records[i:end]); err != nil {
			return err
		}
	}
	return nil
}
