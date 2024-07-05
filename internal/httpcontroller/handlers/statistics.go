package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"runtime"
	"time"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
)

func (h *Handlers) CreateGoroutinesChart() *charts.Line {
	line := charts.NewLine()
	line.SetGlobalOptions(charts.WithTitleOpts(opts.Title{Title: "Goroutines"}))
	line.SetXAxis([]string{"Now"})
	line.AddSeries("Goroutines", []opts.LineData{{Value: runtime.NumGoroutine()}})
	return line
}

func (h *Handlers) CreateBirdVocalizationChart(startDate, endDate time.Time) *charts.Bar {
	// If no custom dates are provided, use the current month
	if startDate.IsZero() || endDate.IsZero() {
		now := time.Now()
		currentYear, currentMonth, _ := now.Date()
		startDate = time.Date(currentYear, currentMonth, 1, 0, 0, 0, 0, now.Location())
		endDate = startDate.AddDate(0, 1, -1)
	}

	// Ensure startDate is before endDate
	if startDate.After(endDate) {
		startDate, endDate = endDate, startDate
	}

	// Fetch data using the datastore interface
	notes, err := h.DS.GetAllNotes()
	if err != nil {
		fmt.Printf("Error fetching data: %v\n", err)
		return nil
	}

	// Process the notes to get daily counts
	dailyCounts := make(map[string]int)
	for _, note := range notes {
		noteDate, err := time.Parse("2006-01-02", note.Date)
		if err != nil {
			continue
		}
		if noteDate.Before(startDate) || noteDate.After(endDate) {
			continue
		}
		dailyCounts[note.Date]++
	}

	// Prepare data for the chart
	var dates []string
	var counts []opts.BarData
	for date := startDate; date.Before(endDate.AddDate(0, 0, 1)); date = date.AddDate(0, 0, 1) {
		dateStr := date.Format("2006-01-02")
		dates = append(dates, dateStr)
		counts = append(counts, opts.BarData{Value: dailyCounts[dateStr]})
	}

	// Helper function to create a pointer to a boolean
	boolPtr := func(b bool) *bool { return &b }

	// Create a new bar chart
	bar := charts.NewBar()
	bar.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "400px",
		}),
		charts.WithTitleOpts(opts.Title{
			Title: "Bird Detections by Date",
		}),
		charts.WithTooltipOpts(opts.Tooltip{Show: boolPtr(true)}),
		charts.WithLegendOpts(opts.Legend{Show: boolPtr(true)}),
		charts.WithXAxisOpts(opts.XAxis{
			Name:      "Date",
			AxisLabel: &opts.AxisLabel{Rotate: 45},
		}),
		charts.WithYAxisOpts(opts.YAxis{
			Name: "Number of Detections",
		}),
		charts.WithGridOpts(opts.Grid{
			ContainLabel: boolPtr(true),
			Left:         "3%",
			Right:        "4%",
			Bottom:       "15%",
		}),
	)

	// Add data to the chart
	bar.SetXAxis(dates).AddSeries("Detections", counts)

	return bar
}

func renderBarChartToHTML(chart *charts.Bar) string {
	// Note: We're not setting the ID here anymore, as go-echarts is generating its own

	chartSnippet := chart.RenderSnippet()
	tmpl := `{{.Element}} {{.Script}}`
	t := template.Must(template.New("snippet").Parse(tmpl))

	data := struct {
		Element template.HTML
		Script  template.HTML
	}{
		Element: template.HTML(chartSnippet.Element),
		Script:  template.HTML(chartSnippet.Script),
	}

	var buf bytes.Buffer
	err := t.Execute(&buf, data)
	if err != nil {
		log.Printf("Error rendering bar chart to HTML: %v", err)
		return "Error rendering bar chart: " + err.Error()
	}

	renderedHTML := buf.String()

	return renderedHTML
}
