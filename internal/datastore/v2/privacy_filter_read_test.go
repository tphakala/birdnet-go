package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

const (
	readTestHour      = int64(3600 * 1000)
	readTestSecPerHr  = int64(3600)
	readTestHalfHour  = int64(1800)
	readTestRangeHrs  = 4
	readTestStartMs   = int64(5_000_000)
	readTestEndMs     = int64(6_000_000)
	readTestPageLimit = 2

	// readTestBulkRows exceeds the 500 ids a single hit query carries, so the
	// hit loading has to run more than one batch.
	readTestBulkRows  = 510
	readTestBulkStart = int64(9_000_000)
)

// TestPrivacyFilterReads_SQLite runs the privacy filter read methods on the
// real SQLite manager.
func TestPrivacyFilterReads_SQLite(t *testing.T) {
	t.Parallel()
	mgr, err := NewSQLiteManager(Config{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	require.NoError(t, mgr.Initialize())

	exercisePrivacyReads(t, mgr.DB(), false, false)
}

// exercisePrivacyReads checks the hourly overlap read (bounds, ordering) and the
// discard page read (bounds, ordering, pagination, hit attachment, NULL source).
func exercisePrivacyReads(t *testing.T, db *gorm.DB, useV2Prefix, isMySQL bool) {
	t.Helper()
	ctx := t.Context()
	hourly := repository.NewPrivacyFilterHourlyRepository(db, nil, useV2Prefix, isMySQL)
	discards := repository.NewPrivacyFilterDiscardRepository(db, nil, useV2Prefix)
	sources := repository.NewAudioSourceRepository(db, nil, useV2Prefix, isMySQL)

	srcA, err := sources.GetOrCreate(ctx, "read-test-a", "n", nil, entities.SourceType(""))
	require.NoError(t, err)
	srcB, err := sources.GetOrCreate(ctx, "read-test-b", "n", nil, entities.SourceType(""))
	require.NoError(t, err)

	// Hourly: rows at H-1, H (three keys), H+1, H+2 (the model and the trigger
	// type each vary alone, inserted in descending key order) and E-1 and E, with
	// E = H + 4 hours.
	h := readTestHour
	e := h + int64(readTestRangeHrs)*readTestSecPerHr
	addKey := func(hour int64, src uint, model, trigger string, threshold int) {
		require.NoError(t, hourly.AddDelta(ctx, &entities.PrivacyFilterHourly{
			HourStart: hour, SourceID: src, ModelID: model, TriggerType: trigger, ThresholdMilli: threshold, Hits: 1,
		}))
	}
	add := func(hour int64, src uint, threshold int) { addKey(hour, src, "m1", "label", threshold) }
	add(e, srcA.ID, 50)
	add(h+readTestSecPerHr, srcA.ID, 50)
	add(h, srcB.ID, 50)
	add(h, srcA.ID, 100)
	add(h, srcA.ID, 50)
	add(h-readTestSecPerHr, srcA.ID, 50)
	add(e-readTestSecPerHr, srcA.ID, 50)
	addKey(h+2*readTestSecPerHr, srcA.ID, "m2", "label", 50)
	addKey(h+2*readTestSecPerHr, srcA.ID, "m1", "vad", 50)
	addKey(h+2*readTestSecPerHr, srcA.ID, "m1", "label", 50)

	type key struct {
		hour    int64
		src     uint
		model   string
		trigger string
		thr     int
	}
	k := func(hour int64, src uint, thr int) key { return key{hour, src, "m1", "label", thr} }
	keys := func(rows []entities.PrivacyFilterHourly) []key {
		out := make([]key, 0, len(rows))
		for i := range rows {
			out = append(out, key{rows[i].HourStart, rows[i].SourceID, rows[i].ModelID, rows[i].TriggerType, rows[i].ThresholdMilli})
		}
		return out
	}

	aligned, err := hourly.ListOverlapping(ctx, h, e)
	require.NoError(t, err)
	hPlus2 := h + 2*readTestSecPerHr
	assert.Equal(t, []key{
		k(h, srcA.ID, 50), k(h, srcA.ID, 100), k(h, srcB.ID, 50),
		k(h+readTestSecPerHr, srcA.ID, 50),
		k(hPlus2, srcA.ID, 50), {hPlus2, srcA.ID, "m1", "vad", 50}, {hPlus2, srcA.ID, "m2", "label", 50},
		k(e-readTestSecPerHr, srcA.ID, 50),
	}, keys(aligned), "aligned range is half-open and ordered by the full key")

	shifted, err := hourly.ListOverlapping(ctx, h+readTestHalfHour, e+readTestHalfHour)
	require.NoError(t, err)
	assert.Equal(t, []key{
		k(h, srcA.ID, 50), k(h, srcA.ID, 100), k(h, srcB.ID, 50),
		k(h+readTestSecPerHr, srcA.ID, 50),
		k(hPlus2, srcA.ID, 50), {hPlus2, srcA.ID, "m1", "vad", 50}, {hPlus2, srcA.ID, "m2", "label", 50},
		k(e-readTestSecPerHr, srcA.ID, 50), k(e, srcA.ID, 50),
	}, keys(shifted), "a range off the hour boundary keeps the hours it overlaps")

	empty, err := hourly.ListOverlapping(ctx, e, h)
	require.NoError(t, err)
	assert.Empty(t, empty)

	// Discards.
	s, en := readTestStartMs, readTestEndMs
	newDiscard := func(at int64, src *uint, hits ...entities.PrivacyFilterDiscardHit) *entities.PrivacyFilterDiscard {
		return &entities.PrivacyFilterDiscard{
			DiscardedAt: at, SourceID: src, ScientificName: "Parus major", CommonName: "Great Tit",
			Confidence: 0.8, ModelID: "m1", ClipBegin: at - 3000, ClipEnd: at + 3000,
			LabelThresholdMilli: 50, Hits: hits,
		}
	}
	hit := func(detector string, count int) entities.PrivacyFilterDiscardHit {
		return entities.PrivacyFilterDiscardHit{Detector: detector, TriggerType: "label", HitCount: count, MaxScore: 0.9}
	}
	before := newDiscard(s-1, &srcA.ID, hit("m1", 9))
	atStart := newDiscard(s, &srcA.ID, hit("m1", 1))
	tieFirst := newDiscard(s+100, &srcA.ID, hit("m1", 2), hit("silero_vad", 3))
	tieSecond := newDiscard(s+100, nil, hit("m1", 4))
	noHits := newDiscard(s+200, &srcB.ID)
	atEnd := newDiscard(en, &srcA.ID, hit("m1", 9))
	for _, d := range []*entities.PrivacyFilterDiscard{before, atStart, tieFirst, tieSecond, noHits, atEnd} {
		require.NoError(t, discards.Save(ctx, d))
	}
	wantOrder := []uint{noHits.ID, tieSecond.ID, tieFirst.ID, atStart.ID}

	var gotOrder []uint
	for offset := 0; offset < len(wantOrder); offset += readTestPageLimit {
		page, total, err := discards.ListPage(ctx, s, en, readTestPageLimit, offset)
		require.NoError(t, err)
		assert.Equal(t, int64(len(wantOrder)), total, "total is the same on every page")
		for i := range page {
			gotOrder = append(gotOrder, page[i].ID)
			switch page[i].ID {
			case noHits.ID:
				assert.Empty(t, page[i].Hits)
			case tieSecond.ID:
				assert.Nil(t, page[i].SourceID, "the NULL source survives")
				require.Len(t, page[i].Hits, 1)
				assert.Equal(t, 4, page[i].Hits[0].HitCount)
			case tieFirst.ID:
				require.Len(t, page[i].Hits, 2)
				assert.Equal(t, "m1", page[i].Hits[0].Detector, "hits come in id order")
				assert.Equal(t, 2, page[i].Hits[0].HitCount)
				assert.Equal(t, "silero_vad", page[i].Hits[1].Detector)
			case atStart.ID:
				require.Len(t, page[i].Hits, 1)
				assert.Equal(t, 1, page[i].Hits[0].HitCount)
			}
		}
	}
	assert.Equal(t, wantOrder, gotOrder, "pages concatenate to the full order with no repeat or gap")

	past, total, err := discards.ListPage(ctx, s, en, readTestPageLimit, len(wantOrder))
	require.NoError(t, err)
	assert.Empty(t, past)
	assert.Equal(t, int64(len(wantOrder)), total)

	// A page larger than one hit batch attaches every discard's own hit.
	discardTable, hitTable := "privacy_filter_discards", "privacy_filter_discard_hits"
	if useV2Prefix {
		discardTable, hitTable = V2TableName(discardTable), V2TableName(hitTable)
	}
	bulk := make([]entities.PrivacyFilterDiscard, 0, readTestBulkRows)
	for i := range readTestBulkRows {
		at := readTestBulkStart + int64(i)
		bulk = append(bulk, entities.PrivacyFilterDiscard{
			DiscardedAt: at, SourceID: &srcA.ID, ScientificName: "Parus major", CommonName: "Great Tit",
			Confidence: 0.8, ModelID: "m1", ClipBegin: at - 3000, ClipEnd: at + 3000, LabelThresholdMilli: 50,
		})
	}
	require.NoError(t, db.Table(discardTable).Omit("Hits", "Source").CreateInBatches(&bulk, readTestBulkRows).Error)
	bulkHits := make([]entities.PrivacyFilterDiscardHit, 0, readTestBulkRows)
	for i := range bulk {
		bulkHits = append(bulkHits, entities.PrivacyFilterDiscardHit{
			DiscardID: bulk[i].ID, Detector: "m1", TriggerType: "label", HitCount: i + 1, MaxScore: 0.9,
		})
	}
	require.NoError(t, db.Table(hitTable).CreateInBatches(&bulkHits, readTestBulkRows).Error)

	big, bigTotal, err := discards.ListPage(ctx, readTestBulkStart, readTestBulkStart+readTestBulkRows, readTestBulkRows, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(readTestBulkRows), bigTotal)
	require.Len(t, big, readTestBulkRows)
	for j := range big {
		require.Len(t, big[j].Hits, 1, "row %d", j)
		assert.Equal(t, readTestBulkRows-j, big[j].Hits[0].HitCount, "row %d keeps its own hit", j)
	}
}
