package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/dbserver"
	"gorm.io/gorm/schema"
)

func TestConvertibleTables_OnlyThisManagersV2Tables(t *testing.T) {
	t.Parallel()

	latin1 := []string{"latin1"}
	utf8mb4 := []string{dbserver.CharsetUTF8MB4}
	report := dbserver.CharsetReport{
		Tables: []dbserver.TableCharsets{
			{Name: "image_caches", Charsets: latin1},
			{Name: "labels", Charsets: latin1},
			{Name: "notes", Charsets: latin1},
			{Name: "v2_detection_comments", Charsets: []string{"utf8mb3"}},
			{Name: "v2_dynamic_thresholds", Charsets: []string{"latin1", dbserver.CharsetUTF8MB4}},
			{Name: "v2_image_caches", Charsets: latin1},
			{Name: "v2_labels", Charsets: utf8mb4},
		},
	}

	tests := []struct {
		name  string
		namer schema.Namer
		want  []string
	}{
		{
			"v2 prefix",
			schema.NamingStrategy{TablePrefix: v2TablePrefix},
			[]string{"v2_detection_comments", "v2_dynamic_thresholds", "v2_image_caches"},
		},
		{"bare names", schema.NamingStrategy{}, []string{"image_caches", "labels"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := convertibleTables(tt.namer, report)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
