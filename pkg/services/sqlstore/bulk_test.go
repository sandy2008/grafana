package sqlstore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBulkOps(t *testing.T) {
	t.Run("batching", func(t *testing.T) {
		t.Run("succeeds on empty slices", func(t *testing.T) {
			calls := 0
			fn := func(batch []interface{}) error { calls += 1; return nil }
			records := make([]interface{}, 0)
			opts := BulkOpSettings{
				BatchSize: DefaultBatchSize,
			}

			err := inBatches(records, opts, fn)

			require.NoError(t, err)
			require.Zero(t, calls)
		})
	})
}
