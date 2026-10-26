package repository

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
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

// TestGetHourlyWeatherInLocation_DayBoundsFollowTheLocalDay pins the weather window of the local
// days in dayBoundsCases, including the skipped-midnight days (Havana 2026-03-08, Santiago
// 2026-09-06): a row one minute before the day starts is excluded, the first instant of the day
// is included, and the first instant of the next day is excluded.
func TestGetHourlyWeatherInLocation_DayBoundsFollowTheLocalDay(t *testing.T) {
	t.Parallel()

	for _, tc := range dayBoundsCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			zone := dayBoundsZone(t, tc.zone)

			db := setupWeatherTestDB(t)
			repo := NewWeatherRepository(db, nil, false, false)
			ctx := t.Context()

			for _, at := range []time.Time{
				tc.wantStart.Add(-time.Minute),
				tc.wantStart,
				tc.wantEnd,
				tc.wantEnd.Add(-time.Minute),
			} {
				require.NoError(t, repo.SaveHourlyWeather(ctx, &entities.HourlyWeather{Time: at.UTC()}))
			}

			got, err := repo.GetHourlyWeatherInLocation(ctx, tc.date, zone)
			require.NoError(t, err)

			var gotTimes []int64
			for _, w := range got {
				gotTimes = append(gotTimes, w.Time.Unix())
			}
			assert.Equal(t, []int64{tc.wantStart.Unix(), tc.wantEnd.Add(-time.Minute).Unix()}, gotTimes)
		})
	}
}
