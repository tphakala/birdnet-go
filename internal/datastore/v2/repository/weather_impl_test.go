package repository

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/testutil"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

func setupWeatherTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	require.NoError(t, db.AutoMigrate(&entities.DailyEvents{}, &entities.HourlyWeather{}))
	return db
}

// TestGetHourlyWeatherInLocation_DayBoundsFollowTheLocalDay pins the weather window of a local day
// whose midnight is skipped (Havana 2026-03-08, Santiago 2026-09-06): a row one minute before the
// day starts is excluded, the first instant of the day is included, and the first instant of the
// next day is excluded.
func TestGetHourlyWeatherInLocation_DayBoundsFollowTheLocalDay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		zone  string
		date  string
		start time.Time // first instant of the day
		end   time.Time // first instant of the next day
	}{
		{"Havana skipped midnight", "America/Havana", "2026-03-08", utc(2026, time.March, 8, 5, 0, 0), utc(2026, time.March, 9, 4, 0, 0)},
		{"Santiago skipped midnight", "America/Santiago", "2026-09-06", utc(2026, time.September, 6, 4, 0, 0), utc(2026, time.September, 7, 3, 0, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			zone, ok := testutil.ToolchainZones(t)[tc.zone]
			require.True(t, ok, "zone %s missing from the toolchain zone database", tc.zone)

			db := setupWeatherTestDB(t)
			repo := NewWeatherRepository(db, nil, false, false)
			ctx := t.Context()

			rows := map[string]time.Time{
				"before start": tc.start.Add(-time.Minute),
				"at start":     tc.start,
				"at end":       tc.end,
				"before end":   tc.end.Add(-time.Minute),
			}
			for _, at := range rows {
				require.NoError(t, repo.SaveHourlyWeather(ctx, &entities.HourlyWeather{Time: at.UTC()}))
			}

			got, err := repo.GetHourlyWeatherInLocation(ctx, tc.date, zone)
			require.NoError(t, err)

			var gotTimes []int64
			for _, w := range got {
				gotTimes = append(gotTimes, w.Time.Unix())
			}
			assert.Equal(t, []int64{tc.start.Unix(), tc.end.Add(-time.Minute).Unix()}, gotTimes)
		})
	}
}
