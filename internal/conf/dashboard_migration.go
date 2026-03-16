package conf

const defaultDashboardSummaryLimit = 30

// MigrateDashboardLayout migrates existing installations to the new dashboard layout format.
// It creates a default layout with the three elements (daily-summary, currently-hearing,
// detections-grid) in their original fixed order, and moves SummaryLimit into the daily-summary
// element config.
//
// Returns true if migration occurred, false if skipped (already has layout elements).
func (s *Settings) MigrateDashboardLayout() bool {
	if len(s.Realtime.Dashboard.Layout.Elements) > 0 {
		return false
	}

	summaryLimit := s.Realtime.Dashboard.SummaryLimit
	if summaryLimit <= 0 {
		summaryLimit = defaultDashboardSummaryLimit
	}

	s.Realtime.Dashboard.Layout = DashboardLayout{
		Elements: []DashboardElement{
			{
				ID:      "daily-summary-0",
				Type:    "daily-summary",
				Enabled: true,
				Summary: &DailySummaryConfig{
					SummaryLimit: summaryLimit,
				},
			},
			{
				ID:      "currently-hearing-0",
				Type:    "currently-hearing",
				Enabled: true,
			},
			{
				ID:      "detections-grid-0",
				Type:    "detections-grid",
				Enabled: true,
			},
		},
	}

	// Zero out deprecated field now that it's migrated into the layout element
	s.Realtime.Dashboard.SummaryLimit = 0

	return true
}

// GetEffectiveSummaryLimit returns the summary limit from the dashboard layout element,
// falling back to the deprecated Dashboard.SummaryLimit field and then to a default.
// This is needed because MigrateDashboardLayout zeroes the deprecated field after moving
// the value into the layout element config.
func (s *Settings) GetEffectiveSummaryLimit() int {
	for _, el := range s.Realtime.Dashboard.Layout.Elements {
		if el.Type == "daily-summary" && el.Summary != nil && el.Summary.SummaryLimit > 0 {
			return el.Summary.SummaryLimit
		}
	}
	if s.Realtime.Dashboard.SummaryLimit > 0 {
		return s.Realtime.Dashboard.SummaryLimit
	}
	return defaultDashboardSummaryLimit
}
