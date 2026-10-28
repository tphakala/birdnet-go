//go:build integration

package v2

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/dbserver"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

const (
	// charsetTestOldText is representable in latin1, so the table an older
	// release created holds it.
	charsetTestOldText = "café"
	// charsetTestUnicodeText needs utf8mb4: Cyrillic and a 4-byte emoji.
	charsetTestUnicodeText = "Денис Воронов 🐦"
	charsetTestLatin1DDL   = "CHARACTER SET latin1 COLLATE latin1_swedish_ci"
	charsetTestCustomDDL   = "CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
	charsetTestCustomColl  = "utf8mb4_unicode_ci"
)

// tableCollations maps each base table of the test schema to its collation.
func tableCollations(t *testing.T) map[string]string {
	t.Helper()
	var rows []struct {
		Name      string `gorm:"column:table_name"`
		Collation string `gorm:"column:table_collation"`
	}
	require.NoError(t, coexTestDB.Raw(
		"SELECT TABLE_NAME AS table_name, TABLE_COLLATION AS table_collation "+
			"FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'",
		coexTestDBName).Scan(&rows).Error)
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Name] = r.Collation
	}
	return out
}

// roundTripUnicode writes 4-byte text through two tables the manager created
// and reads it back.
func roundTripUnicode(t *testing.T, db *gorm.DB) {
	t.Helper()

	now := time.Now()
	threshold := entities.DynamicThreshold{
		SpeciesName: charsetTestUnicodeText,
		ExpiresAt:   now, LastTriggered: now, FirstCreated: now,
	}
	require.NoError(t, db.Create(&threshold).Error, "unicode species name must be stored")
	var gotThreshold entities.DynamicThreshold
	require.NoError(t, db.First(&gotThreshold, threshold.ID).Error)
	assert.Equal(t, charsetTestUnicodeText, gotThreshold.SpeciesName)

	var model entities.AIModel
	require.NoError(t, db.First(&model).Error)
	var labelType entities.LabelType
	require.NoError(t, db.First(&labelType).Error)
	label := entities.Label{ScientificName: "Turdus merula", ModelID: model.ID, LabelTypeID: labelType.ID}
	require.NoError(t, db.Create(&label).Error)
	cache := entities.ImageCache{LabelID: label.ID, AuthorName: charsetTestUnicodeText, CachedAt: now}
	require.NoError(t, db.Create(&cache).Error, "unicode author name must be stored")
	var gotCache entities.ImageCache
	require.NoError(t, db.First(&gotCache, cache.ID).Error)
	assert.Equal(t, charsetTestUnicodeText, gotCache.AuthorName)
}

func TestMySQL_V2Initialize_SchemaCharset_NewTablesHoldUnicodeAndExistingTablesUnchanged(t *testing.T) {
	// Not parallel: the subtests share one database and change its default.
	tests := []struct {
		name        string
		schemaDDL   string
		usePrefix   bool
		wantUnicode bool // new tables are forced to utf8mb4 and store 4-byte text
	}{
		{"latin1 schema fresh install", charsetTestLatin1DDL, false, true},
		{"latin1 schema migration mode", charsetTestLatin1DDL, true, true},
		{"utf8mb4 schema with custom collation", charsetTestCustomDDL, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)
			origCharset, origCollation, err := dbserver.QuerySchemaCharset(t.Context(), coexTestDB)
			require.NoError(t, err)
			require.NoError(t, coexTestDB.Exec(fmt.Sprintf("ALTER DATABASE `%s` %s", coexTestDBName, tt.schemaDDL)).Error)
			t.Cleanup(func() {
				restore := fmt.Sprintf("ALTER DATABASE `%s` CHARACTER SET %s COLLATE %s", coexTestDBName, origCharset, origCollation)
				assert.NoError(t, coexTestDB.Exec(restore).Error)
			})

			prefix := ""
			if tt.usePrefix {
				prefix = v2TablePrefix
			}
			// A table the way an older release created it: no table options.
			oldTable := prefix + "app_metadata"
			require.NoError(t, coexTestDB.Table(oldTable).AutoMigrate(&entities.AppMetadata{}))
			require.NoError(t, coexTestDB.Table(oldTable).Create(
				&entities.AppMetadata{Key: "k", Value: charsetTestOldText}).Error)

			mgr := newTestMySQLManager(t, tt.usePrefix)
			require.NoError(t, mgr.Initialize())
			t.Cleanup(func() { _ = mgr.Close() })

			report, err := dbserver.QueryCharset(t.Context(), coexTestDB)
			require.NoError(t, err)
			collations := tableCollations(t)
			require.Contains(t, collations, oldTable)

			if tt.wantUnicode {
				assert.Equal(t, []string{oldTable}, report.NonUTF8MB4Tables(),
					"only the pre-existing table may stay outside utf8mb4")
				assert.Equal(t, "latin1_swedish_ci", collations[oldTable])
				roundTripUnicode(t, mgr.DB())
			} else {
				assert.Empty(t, report.NonUTF8MB4Tables())
				for name, collation := range collations {
					assert.Equal(t, charsetTestCustomColl, collation, "table %s must inherit the schema collation", name)
				}
			}

			// Boot again: nothing changes, including the pre-existing table and row.
			mgr2 := newTestMySQLManager(t, tt.usePrefix)
			require.NoError(t, mgr2.Initialize())
			t.Cleanup(func() { _ = mgr2.Close() })
			assert.Equal(t, collations, tableCollations(t))
			var row entities.AppMetadata
			require.NoError(t, coexTestDB.Table(oldTable).First(&row, "`key` = ?", "k").Error)
			assert.Equal(t, charsetTestOldText, row.Value)
		})
	}
}

func TestMySQL_V2MigrationDB_UnreadableSchemaCharsetFails(t *testing.T) {
	// Not parallel: shares the test database with the other integration tests.
	mgr := newTestMySQLManager(t, false)
	require.NoError(t, mgr.Close())

	db, err := mgr.migrationDB()

	require.Error(t, err, "an unreadable schema charset must not fall back to a handle without table options")
	assert.Nil(t, db)
}
