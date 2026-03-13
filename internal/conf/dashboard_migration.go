package conf

const defaultDashboardSummaryLimit = 100

// MigrateDashboardLayout migrates existing installations to the new dashboard layout format.
// It creates a default layout with the three existing elements (daily-summary, currently-hearing,
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
				Type:    "daily-summary",
				Enabled: true,
				Summary: &DailySummaryConfig{
					SummaryLimit: summaryLimit,
				},
			},
			{
				Type:    "currently-hearing",
				Enabled: true,
			},
			{
				Type:    "detections-grid",
				Enabled: true,
			},
		},
	}

	return true
}
