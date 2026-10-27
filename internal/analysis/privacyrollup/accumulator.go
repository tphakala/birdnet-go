package privacyrollup

// sourceKey identifies an audio source as the detection save path does.
type sourceKey struct {
	uri, node string
}

// rowKey is the identity of a rollup row before the source id is resolved.
type rowKey struct {
	hourStart      int64
	source         sourceKey
	modelID        string
	trigger        string
	thresholdMilli int
}

// counters are the additive columns of a row.
type counters struct {
	hits, held, ignored, confirmed, discards, unscanned int64
	lt50, lt100, lt200, ge200                           int64
	maxScore                                            float64
}

// accumulator folds events into per-row deltas. It has no lock: the writer
// goroutine owns it.
type accumulator struct {
	rows map[rowKey]*counters
	// display is the latest display name seen per source in this accumulator.
	display map[sourceKey]string
}

// add folds one event into its row.
//
//nolint:gocritic // hugeParam: events are small and rare, a copy is cheaper than an escaping pointer
func (a *accumulator) add(ev Event) {
	if a.rows == nil {
		a.rows = make(map[rowKey]*counters)
		a.display = make(map[sourceKey]string)
	}
	src := sourceKey{uri: ev.SourceURI, node: ev.NodeName}
	if ev.DisplayName != "" {
		a.display[src] = ev.DisplayName
	}
	key := rowKey{
		hourStart: HourStart(ev.At), source: src, modelID: ev.ModelID,
		trigger: ev.Trigger, thresholdMilli: ev.ThresholdMilli,
	}
	d := a.rows[key]
	if d == nil {
		d = &counters{}
		a.rows[key] = d
	}
	switch ev.Kind {
	case KindCountedHit:
		d.hits++
		d.bucket(ev)
	case KindHeldHit:
		d.held++
		d.bucket(ev)
	case KindConfirmedHeld:
		d.hits++
		d.confirmed++
	case KindIgnoredHeld:
		d.ignored++
	case KindDiscard:
		d.discards++
	case KindUnscannedSave:
		d.unscanned++
	}
}

// bucket raises the maximum score and counts the event in its margin bucket.
//
//nolint:gocritic // hugeParam: events are small and rare, a copy is cheaper than an escaping pointer
func (d *counters) bucket(ev Event) {
	if ev.Score > d.maxScore {
		d.maxScore = ev.Score
	}
	margin := ThresholdMilli(ev.Score) - ev.ThresholdMilli
	switch {
	case margin < scoreMarginNearMilli:
		d.lt50++
	case margin < scoreMarginMidMilli:
		d.lt100++
	case margin < scoreMarginFarMilli:
		d.lt200++
	default:
		d.ge200++
	}
}
