//go:build integration

package v2

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMySQL_PrivacyFilterDiscardRepository runs the discard repository on MySQL
// under both table layouts. It catches MySQL-only DDL or cascade differences, a
// wrong v2_ table name, and a drop list that orders the tables after
// audio_sources (the foreign keys would make Delete fail).
func TestMySQL_PrivacyFilterDiscardRepository(t *testing.T) {
	for _, useV2Prefix := range []bool{true, false} {
		t.Run(fmt.Sprintf("useV2Prefix=%t", useV2Prefix), func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)

			mgr := newTestMySQLManager(t, useV2Prefix)
			t.Cleanup(func() { _ = mgr.Close() })
			require.NoError(t, mgr.Initialize())

			exercisePrivacyDiscardRepository(t, mgr.DB(), useV2Prefix, true)

			require.NoError(t, mgr.Delete())
		})
	}
}
