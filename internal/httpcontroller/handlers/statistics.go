package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"runtime"
	"time"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/labstack/echo/v4"
)

func (h *Handlers) BirdStats() string {
	startDate := time.Date(2024, time.June, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, time.June, 30, 0, 0, 0, 0, time.UTC)

	// Create charts
	birdVocalizationChart := h.createBirdVocalizationChart(startDate, endDate)
	// Render charts to HTML
	birdVocalizationChartHTML := renderBarChartToHTML(birdVocalizationChart)

	return birdVocalizationChartHTML
}

func CreateGoroutinesChart() *charts.Line {
	line := charts.NewLine()
	line.SetGlobalOptions(charts.WithTitleOpts(opts.Title{Title: "Goroutines"}))
	line.SetXAxis([]string{"Now"})
	line.AddSeries("Goroutines", []opts.LineData{{Value: runtime.NumGoroutine()}})
	return line
}

func (h *Handlers) createBirdVocalizationChart(startDate, endDate time.Time) *charts.Bar {
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
	for i := range notes {
		noteDate, err := time.Parse("2006-01-02", notes[i].Date)
		if err != nil {
			continue
		}
		if noteDate.Before(startDate) || noteDate.After(endDate) {
			continue
		}
		dailyCounts[noteDate.Format("2006-01-02")]++
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

// GetDailyStats handles the request for daily statistics
func (h *Handlers) GetDailyStats(c echo.Context) error {
	chart := h.createBirdVocalizationChart(time.Now().AddDate(0, 0, -30), time.Now())
	chartOptions := renderChartToJSON(chart)

	return c.Render(http.StatusOK, "dailyStats", map[string]interface{}{
		"ChartOptions": template.JS(chartOptions),
	})
}

// GetWeeklyStats handles the request for weekly statistics
func (h *Handlers) GetWeeklyStats(c echo.Context) error {
	chart := h.createBirdVocalizationChart(time.Now().AddDate(0, 0, -7*4), time.Now())
	chartOptions := renderChartToJSON(chart)

	return c.Render(http.StatusOK, "weeklyStats", map[string]interface{}{
		"ChartOptions": template.JS(chartOptions),
	})
}

// GetMonthlyStats handles the request for monthly statistics
func (h *Handlers) GetMonthlyStats(c echo.Context) error {
	chart := h.createBirdVocalizationChart(time.Now().AddDate(0, -6, 0), time.Now())
	chartOptions := renderChartToJSON(chart)

	return c.Render(http.StatusOK, "monthlyStats", map[string]interface{}{
		"ChartOptions": template.JS(chartOptions),
	})
}

// GetSpeciesStats handles the request for species statistics
func (h *Handlers) GetSpeciesStats(c echo.Context) error {
	// Implement species-specific statistics here
	// For now, we'll use the same chart as an example
	chart := h.createBirdVocalizationChart(time.Now().AddDate(0, -1, 0), time.Now())
	chartOptions := renderChartToJSON(chart)

	return c.Render(http.StatusOK, "speciesStats", map[string]interface{}{
		"ChartOptions": template.JS(chartOptions),
	})
}

// renderChartToJSON converts the chart to JSON for use with ECharts
func renderChartToJSON(chart *charts.Bar) string {
	options := chart.JSON()
	jsonData, err := json.Marshal(options)
	if err != nil {
		return "{}"
	}
	return string(jsonData)
}

func (h *Handlers) CreateDailyDetectionsChart() *charts.Bar {
	// Implementation similar to createBirdVocalizationChart
	// but focused on daily detections
	// ...
	return nil
}

func (h *Handlers) CreateSpeciesDiversityChart() *charts.Pie {
	pie := charts.NewPie()
	pie.SetGlobalOptions(
		charts.WithTitleOpts(opts.Title{Title: "Species Diversity"}),
	)

	// Fetch and prepare data
	// ...

	pie.AddSeries("Species", []opts.PieData{
		{Name: "Species 1", Value: 10},
		{Name: "Species 2", Value: 20},
		// Add more species data
	})

	return pie
}

func (h *Handlers) CreateTopSpeciesChart() *charts.Bar {
	bar := charts.NewBar()
	bar.SetGlobalOptions(
		charts.WithTitleOpts(opts.Title{Title: "Top 5 Species"}),
	)

	// Fetch and prepare data
	// ...

	bar.SetXAxis([]string{"Species 1", "Species 2", "Species 3", "Species 4", "Species 5"})
	bar.AddSeries("Detections", []opts.BarData{
		{Value: 30},
		{Value: 25},
		{Value: 20},
		{Value: 15},
		{Value: 10},
	})

	return bar
}
