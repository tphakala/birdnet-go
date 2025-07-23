<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import Chart from 'chart.js/auto';
  import 'chartjs-adapter-date-fns';
  import StatCard from '../components/ui/StatCard.svelte';
  import ChartCard from '../components/ui/ChartCard.svelte';
  import FilterForm from '../components/forms/FilterForm.svelte';
  import { alertIconsSvg } from '$lib/utils/icons';
  import { t } from '$lib/i18n/store.svelte.js';

  // Type definitions
  interface Filters {
    timePeriod: 'all' | 'today' | 'week' | 'month' | '90days' | 'year' | 'custom';
    startDate: string;
    endDate: string;
  }

  interface Summary {
    totalDetections: number;
    uniqueSpecies: number;
    avgConfidence: number;
    mostCommonSpecies: string;
    mostCommonCount: number;
  }

  interface Detection {
    id: string;
    timestamp: string;
    commonName: string;
    scientificName: string;
    confidence: number;
    timeOfDay: string;
  }

  interface SpeciesData {
    common_name: string;
    scientific_name?: string;
    count: number;
    avg_confidence: number;
  }

  interface TimeOfDayData {
    hour: number;
    count: number;
  }

  interface TrendData {
    data: {
      date: string;
      count: number;
    }[];
  }

  interface NewSpeciesData {
    common_name: string;
    scientific_name: string;
    first_heard_date: string;
  }

  interface ChartData {
    species: SpeciesData[];
    timeOfDay: TimeOfDayData[];
    trend: TrendData | null;
    newSpecies: NewSpeciesData[];
  }

  interface Charts {
    species: Chart | null;
    timeOfDay: Chart | null;
    trend: Chart | null;
    newSpecies: Chart<'bar', [number, number][]> | null;
  }

  // State variables
  let isLoading = $state<boolean>(true);
  let error = $state<string | null>(null);

  // Filters
  let filters = $state<Filters>({
    timePeriod: 'week',
    startDate: '',
    endDate: '',
  });

  // Summary data
  let summary = $state<Summary>({
    totalDetections: 0,
    uniqueSpecies: 0,
    avgConfidence: 0,
    mostCommonSpecies: '',
    mostCommonCount: 0,
  });

  // Data arrays
  let recentDetections = $state<Detection[]>([]);
  let newSpeciesData = $state<NewSpeciesData[]>([]);

  // Chart data storage
  let chartData = $state<ChartData>({
    species: [],
    timeOfDay: [],
    trend: null,
    newSpecies: [],
  });

  // Chart instances
  let charts: Charts = {
    species: null,
    timeOfDay: null,
    trend: null,
    newSpecies: null,
  };

  // Format number with thousand separators
  function formatNumber(number: number): string {
    return number.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  }

  // Format percentage
  function formatPercentage(value: number): string {
    return (value * 100).toFixed(1) + '%';
  }

  // Format datetime for display
  function formatDateTime(dateString: string): string {
    if (!dateString) return '';
    const date = new Date(dateString);
    return date.toLocaleString();
  }

  // Format date for input (YYYY-MM-DD)
  function formatDateForInput(date: Date): string {
    return date.toISOString().split('T')[0];
  }

  // Get period label based on current filter
  function getPeriodLabel(): string {
    switch (filters.timePeriod) {
      case 'today':
        return t('analytics.periods.today');
      case 'week':
        return t('analytics.periods.lastWeek');
      case 'month':
        return t('analytics.periods.lastMonth');
      case '90days':
        return t('analytics.periods.last90Days');
      case 'year':
        return t('analytics.periods.lastYear');
      case 'custom':
        return t('analytics.periods.customRange');
      default:
        return t('analytics.periods.allTime');
    }
  }

  // Get theme color from CSS variables
  function getThemeColor(colorName: string, opacity = 1) {
    let color = getComputedStyle(document.documentElement)
      .getPropertyValue(`--${colorName}`)
      .trim();

    if (color.startsWith('#')) {
      const r = parseInt(color.slice(1, 3), 16);
      const g = parseInt(color.slice(3, 5), 16);
      const b = parseInt(color.slice(5, 7), 16);
      return `rgba(${r}, ${g}, ${b}, ${opacity})`;
    }

    if (color.startsWith('rgb')) {
      if (color.startsWith('rgba')) {
        return color.replace(/rgba\((.+?),\s*[\d.]+\)/, `rgba($1, ${opacity})`);
      }
      return color.replace(/rgb\((.+?)\)/, `rgba($1, ${opacity})`);
    }

    return color;
  }

  // Get chart theme configuration
  function getChartTheme() {
    const currentTheme = document.documentElement.getAttribute('data-theme');
    let textColor, gridColor, tooltipBgColor, tooltipBorderColor;

    if (currentTheme === 'dark') {
      textColor = 'rgba(200, 200, 200, 1)';
      gridColor = 'rgba(255, 255, 255, 0.1)';
      tooltipBgColor = 'rgba(55, 65, 81, 0.9)';
      tooltipBorderColor = 'rgba(255, 255, 255, 0.2)';
    } else {
      textColor = 'rgba(55, 65, 81, 1)';
      gridColor = 'rgba(0, 0, 0, 0.1)';
      tooltipBgColor = 'rgba(255, 255, 255, 0.9)';
      tooltipBorderColor = 'rgba(0, 0, 0, 0.2)';
    }

    return {
      color: {
        text: textColor,
        grid: gridColor,
      },
      scales: {
        grid: {
          color: gridColor,
        },
        ticks: {
          color: textColor,
        },
        title: {
          color: textColor,
        },
      },
      legend: {
        labels: {
          color: textColor,
        },
      },
      tooltip: {
        backgroundColor: tooltipBgColor,
        titleColor: textColor,
        bodyColor: textColor,
        borderColor: tooltipBorderColor,
        borderWidth: 1,
      },
    };
  }

  // Generate color palette for charts
  function generateColorPalette(count: number, alpha = 1) {
    const baseColors = [
      `rgba(59, 130, 246, ${alpha})`, // Blue
      `rgba(16, 185, 129, ${alpha})`, // Green
      `rgba(245, 158, 11, ${alpha})`, // Orange
      `rgba(236, 72, 153, ${alpha})`, // Pink
      `rgba(139, 92, 246, ${alpha})`, // Purple
      `rgba(239, 68, 68, ${alpha})`, // Red
      `rgba(20, 184, 166, ${alpha})`, // Teal
      `rgba(234, 179, 8, ${alpha})`, // Yellow
      `rgba(99, 102, 241, ${alpha})`, // Indigo
      `rgba(249, 115, 22, ${alpha})`, // Orange-red
    ];

    if (count <= baseColors.length) {
      return baseColors.slice(0, count);
    } else {
      let palette = [...baseColors];
      while (palette.length < count) {
        const newAlpha = alpha * 0.8;
        const variations = baseColors.map(color => color.replace(`${alpha})`, `${newAlpha})`));
        palette = [...palette, ...variations];
      }
      return palette.slice(0, count);
    }
  }

  // Reset filters
  function resetFilters() {
    filters.timePeriod = 'week';
    const today = new Date();
    const lastWeek = new Date();
    lastWeek.setDate(today.getDate() - 6);
    filters.endDate = formatDateForInput(today);
    filters.startDate = formatDateForInput(lastWeek);
    fetchData();
  }

  // Fetch all data
  async function fetchData() {
    isLoading = true;
    error = null;

    try {
      // Determine date range based on time period
      let startDate, endDate;
      const today = new Date();

      switch (filters.timePeriod) {
        case 'today':
          startDate = formatDateForInput(today);
          endDate = startDate;
          break;
        case 'week':
          endDate = formatDateForInput(today);
          startDate = formatDateForInput(new Date(today.getTime() - 6 * 24 * 60 * 60 * 1000));
          break;
        case 'month':
          endDate = formatDateForInput(today);
          startDate = formatDateForInput(new Date(today.getTime() - 29 * 24 * 60 * 60 * 1000));
          break;
        case '90days':
          endDate = formatDateForInput(today);
          startDate = formatDateForInput(new Date(today.getTime() - 89 * 24 * 60 * 60 * 1000));
          break;
        case 'year':
          endDate = formatDateForInput(today);
          startDate = formatDateForInput(new Date(today.getTime() - 364 * 24 * 60 * 60 * 1000));
          break;
        case 'custom':
          startDate = filters.startDate;
          endDate = filters.endDate;
          break;
        case 'all':
        default:
          startDate = null;
          endDate = null;
          break;
      }

      // Update filters with calculated dates
      if (filters.timePeriod !== 'custom') {
        filters.startDate = startDate || '';
        filters.endDate = endDate || '';
      }

      // Run all API calls in parallel
      await Promise.allSettled([
        fetchSummaryData(startDate || '', endDate || ''),
        fetchSpeciesSummary(startDate || '', endDate || ''),
        fetchRecentDetections(),
        fetchTimeOfDayData(startDate || '', endDate || ''),
        fetchTrendData(startDate || '', endDate || ''),
        fetchNewSpeciesData(startDate || '', endDate || ''),
      ]);
    } catch (err) {
      console.error('General error fetching analytics data:', err);
      error = t('analytics.loadingError');
    }

    // Set loading to false and wait for DOM update before finishing
    isLoading = false;
    await tick();

    // Now create all charts after DOM is ready
    setTimeout(() => {
      createAllCharts();
    }, 100);
  }

  // Fetch summary metrics
  async function fetchSummaryData(startDate: string, endDate: string) {
    try {
      const params = new URLSearchParams({ limit: '100' });
      if (startDate) params.set('start_date', startDate);
      if (endDate) params.set('end_date', endDate);

      const response = await fetch(`/api/v2/analytics/species/summary?${params}`);
      if (!response.ok) throw new Error(`Server responded with ${response.status}`);

      const speciesData = await response.json();
      const speciesArray = Array.isArray(speciesData) ? speciesData : [];

      // Calculate summary metrics
      let totalDetections = 0;
      let totalConfidence = 0;
      let mostCommonSpecies = '';
      let mostCommonCount = 0;

      speciesArray.forEach(species => {
        const count = species.count || 0;
        const confidence = species.avg_confidence || 0;

        totalDetections += count;
        totalConfidence += confidence * count;

        if (count > mostCommonCount) {
          mostCommonCount = count;
          mostCommonSpecies = species.common_name || t('analytics.recentDetections.unknown');
        }
      });

      summary = {
        totalDetections,
        uniqueSpecies: speciesArray.length,
        avgConfidence: totalDetections > 0 ? totalConfidence / totalDetections : 0,
        mostCommonSpecies,
        mostCommonCount,
      };
    } catch (err) {
      console.error('Error fetching summary data:', err);
    }
  }

  // Fetch species summary for chart
  async function fetchSpeciesSummary(startDate: string, endDate: string) {
    try {
      const params = new URLSearchParams({ limit: '10' });
      if (startDate) params.set('start_date', startDate);
      if (endDate) params.set('end_date', endDate);

      const response = await fetch(`/api/v2/analytics/species/summary?${params}`);
      if (!response.ok) throw new Error(`Server responded with ${response.status}`);

      const speciesData = await response.json();
      chartData.species = Array.isArray(speciesData) ? speciesData : [];
    } catch (err) {
      console.error('Error fetching species summary:', err);
      chartData.species = [];
    }
  }

  // Fetch recent detections
  async function fetchRecentDetections() {
    try {
      const response = await fetch('/api/v2/detections/recent?limit=10');
      if (!response.ok) throw new Error(`Server responded with ${response.status}`);

      const data = await response.json();
      const detections = Array.isArray(data) ? data : [];

      recentDetections = detections.map(detection => ({
        id: detection.id,
        timestamp:
          detection.timestamp ||
          (detection.date && detection.time ? `${detection.date} ${detection.time}` : null),
        commonName: detection.commonName,
        scientificName: detection.scientificName,
        confidence: detection.confidence,
        timeOfDay:
          detection.timeOfDay ||
          calculateTimeOfDay(detection.timestamp || `${detection.date} ${detection.time}`),
      }));
    } catch (err) {
      console.error('Error fetching recent detections:', err);
      recentDetections = [];
    }
  }

  // Calculate time of day from timestamp
  function calculateTimeOfDay(timestamp: string) {
    const date = new Date(timestamp);
    const hour = date.getHours();

    if (hour >= 5 && hour < 8) return 'Sunrise';
    if (hour >= 8 && hour < 17) return 'Day';
    if (hour >= 17 && hour < 20) return 'Sunset';
    return 'Night';
  }

  // Fetch time of day data
  async function fetchTimeOfDayData(startDate: string, endDate: string) {
    try {
      const params = new URLSearchParams();
      if (startDate) params.set('start_date', startDate);
      if (endDate) params.set('end_date', endDate);

      const response = await fetch(`/api/v2/analytics/time/distribution/hourly?${params}`);
      if (!response.ok) throw new Error(`Server responded with ${response.status}`);

      const timeData = await response.json();
      chartData.timeOfDay = timeData;
    } catch (err) {
      console.error('Error fetching time of day data:', err);
      chartData.timeOfDay = [];
    }
  }

  // Fetch trend data
  async function fetchTrendData(startDate: string, endDate: string) {
    try {
      const params = new URLSearchParams();
      if (startDate) params.set('start_date', startDate);
      if (endDate) params.set('end_date', endDate);

      const response = await fetch(`/api/v2/analytics/time/daily?${params}`);
      if (!response.ok) throw new Error(`Server responded with ${response.status}`);

      const trendData = await response.json();
      chartData.trend = trendData;
    } catch (err) {
      console.error('Error fetching trend data:', err);
      chartData.trend = { data: [] };
    }
  }

  // Fetch new species data
  async function fetchNewSpeciesData(startDate: string, endDate: string) {
    try {
      const params = new URLSearchParams();
      if (startDate) params.set('start_date', startDate);
      if (endDate) params.set('end_date', endDate);

      const response = await fetch(`/api/v2/analytics/species/detections/new?${params}`);
      if (!response.ok) throw new Error(`Server responded with ${response.status}`);

      const data = await response.json();
      newSpeciesData = Array.isArray(data) ? data : [];
      chartData.newSpecies = newSpeciesData;
    } catch (err) {
      console.error('Error fetching new species data:', err);
      newSpeciesData = [];
      chartData.newSpecies = [];
    }
  }

  // Create all charts after data is loaded and DOM is ready
  function createAllCharts() {
    console.log('Creating all charts...');
    createSpeciesChart(chartData.species);
    createTimeOfDayChart(chartData.timeOfDay);
    createTrendChart(chartData.trend);
    createNewSpeciesChart(chartData.newSpecies);
  }

  // Create species distribution chart
  function createSpeciesChart(data: SpeciesData[]) {
    const ctx = (document.getElementById('speciesChart') as HTMLCanvasElement)?.getContext('2d');
    if (!ctx) {
      console.error('Species chart canvas not found');
      return;
    }

    if (charts.species) {
      charts.species.destroy();
    }

    data.sort((a: SpeciesData, b: SpeciesData) => b.count - a.count);

    const labels = data.map((item: SpeciesData) => item.common_name);
    const counts = data.map((item: SpeciesData) => item.count);
    const backgroundColors = generateColorPalette(data.length, 0.7);
    const theme = getChartTheme();

    charts.species = new Chart(ctx, {
      type: 'bar',
      data: {
        labels: labels,
        datasets: [
          {
            label: t('analytics.charts.numberOfDetections'),
            data: counts,
            backgroundColor: backgroundColors,
            borderWidth: 1,
          },
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        indexAxis: 'y',
        plugins: {
          legend: {
            display: false,
          },
          tooltip: {
            ...theme.tooltip,
            callbacks: {
              label: context => `Detections: ${formatNumber(context.raw as number)}`,
            },
          },
        },
        scales: {
          x: {
            beginAtZero: true,
            title: {
              display: true,
              text: t('analytics.charts.numberOfDetections'),
              color: theme.color.text,
            },
            ticks: {
              color: theme.color.text,
            },
            grid: {
              color: theme.color.grid,
            },
          },
          y: {
            ticks: {
              color: theme.color.text,
            },
            grid: {
              color: theme.color.grid,
            },
          },
        },
      },
    });
  }

  // Create time of day chart
  function createTimeOfDayChart(data: TimeOfDayData[]) {
    const ctx = (document.getElementById('timeOfDayChart') as HTMLCanvasElement)?.getContext('2d');
    if (!ctx) {
      console.error('Time of day chart canvas not found');
      return;
    }

    if (charts.timeOfDay) {
      charts.timeOfDay.destroy();
    }

    const periods = [
      'Night (0-4)',
      'Dawn (5-8)',
      'Morning (9-11)',
      'Afternoon (12-16)',
      'Evening (17-19)',
      'Night (20-23)',
    ];
    const periodCounts = new Array(periods.length).fill(0);

    if (Array.isArray(data)) {
      data.forEach(entry => {
        const hour = entry.hour;
        let periodIndex;
        if (hour >= 0 && hour < 5) periodIndex = 0;
        else if (hour >= 5 && hour < 9) periodIndex = 1;
        else if (hour >= 9 && hour < 12) periodIndex = 2;
        else if (hour >= 12 && hour < 17) periodIndex = 3;
        else if (hour >= 17 && hour < 20) periodIndex = 4;
        else periodIndex = 5;

        periodCounts[periodIndex] += entry.count;
      });
    }

    const backgroundColors = [
      'rgba(55, 48, 163, 0.7)',
      'rgba(251, 146, 60, 0.7)',
      'rgba(250, 204, 21, 0.7)',
      'rgba(56, 189, 248, 0.7)',
      'rgba(251, 113, 133, 0.7)',
      'rgba(42, 36, 122, 0.7)',
    ];

    const theme = getChartTheme();

    charts.timeOfDay = new Chart(ctx, {
      type: 'bar',
      data: {
        labels: periods,
        datasets: [
          {
            label: t('analytics.charts.detections'),
            data: periodCounts,
            backgroundColor: backgroundColors,
            borderWidth: 1,
          },
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: {
            display: false,
          },
          tooltip: {
            ...theme.tooltip,
            callbacks: {
              label: context => `Detections: ${formatNumber(context.raw as number)}`,
            },
          },
        },
        scales: {
          y: {
            beginAtZero: true,
            title: {
              display: true,
              text: t('analytics.charts.numberOfDetections'),
              color: theme.color.text,
            },
            ticks: {
              color: theme.color.text,
            },
            grid: {
              color: theme.color.grid,
            },
          },
          x: {
            title: {
              display: true,
              text: t('analytics.charts.timePeriod'),
              color: theme.color.text,
            },
            ticks: {
              color: theme.color.text,
            },
            grid: {
              color: theme.color.grid,
            },
          },
        },
      },
    });
  }

  // Create trend chart
  function createTrendChart(responseData: TrendData | null) {
    const ctx = (document.getElementById('trendChart') as HTMLCanvasElement)?.getContext('2d');
    if (!ctx) {
      console.error('Trend chart canvas not found');
      return;
    }

    if (charts.trend) {
      charts.trend.destroy();
    }

    const data = responseData?.data || [];
    const dailyData: Record<string, number> = {};

    if (Array.isArray(data)) {
      data.forEach(entry => {
        const date = entry.date;
        if (!dailyData[date]) {
          dailyData[date] = 0;
        }
        dailyData[date] += entry.count;
      });
    }

    const sortedDates = Object.keys(dailyData).sort();
    const labels = sortedDates;
    const counts = sortedDates.map(date => dailyData[date]);

    const theme = getChartTheme();
    const primaryColor = getThemeColor('primary', 1);

    charts.trend = new Chart(ctx, {
      type: 'line',
      data: {
        labels: labels,
        datasets: [
          {
            label: t('analytics.charts.dailyDetections'),
            data: counts,
            fill: false,
            borderColor: primaryColor,
            tension: 0.1,
            pointBackgroundColor: primaryColor,
          },
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: {
            display: true,
            position: 'top',
            labels: {
              color: theme.color.text,
            },
          },
          tooltip: {
            ...theme.tooltip,
            callbacks: {
              label: context => `Detections: ${formatNumber(context.raw as number)}`,
            },
          },
        },
        scales: {
          y: {
            beginAtZero: true,
            title: {
              display: true,
              text: t('analytics.charts.numberOfDetections'),
              color: theme.color.text,
            },
            ticks: {
              color: theme.color.text,
            },
            grid: {
              color: theme.color.grid,
            },
          },
          x: {
            title: {
              display: true,
              text: t('analytics.charts.date'),
              color: theme.color.text,
            },
            ticks: {
              color: theme.color.text,
            },
            grid: {
              color: theme.color.grid,
            },
          },
        },
      },
    });
  }

  // Create new species chart
  function createNewSpeciesChart(data: any) {
    const canvas = document.getElementById('newSpeciesChart') as HTMLCanvasElement;
    const ctx = canvas?.getContext('2d');
    if (!ctx) {
      console.error('New species chart canvas not found');
      return;
    }

    if (charts.newSpecies) {
      charts.newSpecies.destroy();
      charts.newSpecies = null;
    }

    if (!Array.isArray(data) || data.length === 0) return;

    // Helper to add one day
    const addOneDay = (dateStr: string) => {
      if (!dateStr || typeof dateStr !== 'string') return null;
      const date = new Date(dateStr);
      if (isNaN(date.getTime())) return null;
      date.setDate(date.getDate() + 1);
      return date.toISOString().split('T')[0];
    };

    // Filter and process data
    const validData = data.filter(item => {
      const endDate = addOneDay(item.first_heard_date);
      return item.first_heard_date && typeof item.first_heard_date === 'string' && endDate;
    });

    if (validData.length === 0) return;

    // Sort and limit data
    validData.sort(
      (a, b) => new Date(b.first_heard_date).getTime() - new Date(a.first_heard_date).getTime()
    );
    const displayLimit = 20;
    const limitedData = validData.slice(0, displayLimit);
    limitedData.sort(
      (a, b) => new Date(a.first_heard_date).getTime() - new Date(b.first_heard_date).getTime()
    );

    const labels = limitedData.map(item => item.common_name || item.scientific_name);
    const chartValues = limitedData.map(item => {
      const startDate = new Date(item.first_heard_date).getTime();
      const endDate = new Date(addOneDay(item.first_heard_date) || item.first_heard_date).getTime();
      return [startDate, endDate] as [number, number];
    });

    const theme = getChartTheme();
    const colors = generateColorPalette(labels.length, 0.7);

    let minDate: number | undefined = undefined;
    let maxDate: number | undefined = undefined;

    if (filters.timePeriod !== 'all') {
      if (filters.startDate) minDate = new Date(filters.startDate).getTime();
      if (filters.endDate) maxDate = new Date(filters.endDate).getTime();
    }

    if (!minDate && validData.length > 0) {
      minDate = new Date(validData[0].first_heard_date).getTime();
    }
    if (!maxDate && validData.length > 0) {
      const lastDate = addOneDay(validData[validData.length - 1].first_heard_date);
      maxDate = new Date(lastDate || validData[validData.length - 1].first_heard_date).getTime();
    }

    if (maxDate) {
      maxDate = maxDate + 24 * 60 * 60 * 1000; // Add one day in milliseconds
    }

    charts.newSpecies = new Chart(ctx, {
      type: 'bar',
      data: {
        labels: labels,
        datasets: [
          {
            label: t('analytics.charts.firstHeardOn'),
            data: chartValues,
            backgroundColor: colors,
            borderWidth: 1,
            barPercentage: 0.5,
            categoryPercentage: 0.7,
          },
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        indexAxis: 'y',
        plugins: {
          legend: { display: false },
          tooltip: {
            ...theme.tooltip,
            callbacks: {
              title: tooltipItems => tooltipItems[0].label,
              label: context => {
                const dataPoint = context.dataset.data[context.dataIndex] as [number, number];
                const startDate = new Date(dataPoint[0]).toISOString().split('T')[0];
                return `${t('analytics.charts.firstHeard')}: ${startDate}`;
              },
            },
          },
        },
        scales: {
          x: {
            type: 'time',
            time: {
              unit: 'day',
              tooltipFormat: 'yyyy-MM-dd',
              displayFormats: {
                day: 'MMM d',
              },
            },
            min: minDate,
            max: maxDate,
            title: {
              display: true,
              text: t('analytics.charts.firstHeardDate'),
              color: theme.color.text,
            },
            ticks: { color: theme.color.text },
            grid: { color: theme.color.grid },
          },
          y: {
            type: 'category',
            ticks: { color: theme.color.text },
            grid: { display: false },
          },
        },
      },
    });
  }

  // Initialize on mount
  onMount(async () => {
    // Set Chart.js default font
    try {
      const bodyFont = window.getComputedStyle(document.body).fontFamily;
      if (bodyFont) {
        Chart.defaults.font.family = bodyFont;
      }
    } catch (e) {
      console.error('Could not set Chart.js default font family:', e);
    }

    // Set default dates
    const today = new Date();
    const lastMonth = new Date();
    lastMonth.setDate(today.getDate() - 30);

    filters.endDate = formatDateForInput(today);
    filters.startDate = formatDateForInput(lastMonth);

    // Wait for component to be fully mounted
    await tick();

    // Fetch initial data
    fetchData();
  });

  // Cleanup on destroy
  onDestroy(() => {
    Object.values(charts).forEach(chart => {
      if (chart) chart.destroy();
    });
  });
</script>

<div class="col-span-12 space-y-4" role="region" aria-label={t('analytics.title')}>
  {#if error}
    <div class="alert alert-error">
      {@html alertIconsSvg.error}
      <span>{error}</span>
    </div>
  {/if}

  <!-- Summary Stats Cards -->
  <div class="grid gap-4 summary-cards-grid">
    <!-- Total Detections Card -->
    <StatCard
      title={t('analytics.stats.totalDetections')}
      value={formatNumber(summary.totalDetections)}
      subtitle={getPeriodLabel()}
      iconClassName="bg-primary/20"
      {isLoading}
    >
      {#snippet icon()}
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 text-primary"
          viewBox="0 0 921.998 921.998"
          fill="currentColor"
          aria-hidden="true"
        >
          <path
            d="M869.694,385.652c-11.246-12.453-132.373-110.907-154.023-117.272c-9.421-2.735-18.892-4.447-28.681-5.164
              c-45.272-3.315-95.213,10.875-126.684,44.794c-2.741,2.956-4.311,4.645-4.311,4.645s1.172-1.996,3.224-5.488
              c9.706-16.365,23.847-30.577,38.989-41.956c6.979-5.243,14.37-9.937,22.088-14.014c2.116-1.118,21.797-11.751,23.12-10.357
              c-0.003-0.003-10.744-11.33-10.744-11.33c-17.273-17.276-35.963-32.167-61.415-32.167c-31.547,0-58.505,19.559-69.472,47.201
              c-9.306-6.917-24.11-11.392-40.788-11.392c-16.678,0-31.481,4.475-40.788,11.392c-10.967-27.643-37.925-47.201-69.472-47.201
              c-25.452,0-44.142,14.891-61.416,32.166c0,0-10.741,11.327-10.744,11.33c1.322-1.395,21.003,9.239,23.12,10.357
              c7.718,4.077,15.109,8.771,22.088,14.014c15.145,11.378,29.283,25.591,38.989,41.956c2.052,3.493,3.224,5.488,3.224,5.488
              s-1.566-1.689-4.31-4.645c-31.471-33.919-81.411-48.109-126.683-44.794c-9.789,0.717-19.26,2.429-28.681,5.164
              c-21.651,6.365-142.778,104.819-154.023,117.272C19.797,421.645,0,469.336,0,521.655c0,112.112,90.886,203,203,203
              c102.56,0,187.34-76.062,201.048-174.851c15.983,11.645,35.663,18.52,56.951,18.52c21.289,0,40.968-6.875,56.951-18.52
              c13.708,98.788,98.487,174.851,201.048,174.851c112.114,0,203-90.888,203-203C921.996,469.336,902.199,421.647,869.694,385.652z
              M198.497,649.155c-67.611,0-122.421-54.811-122.421-122.421s54.81-122.42,122.421-122.42s122.421,54.81,122.421,122.42
              S266.108,649.155,198.497,649.155z M460.997,515.234c-17.833,0-32.29-14.457-32.29-32.29s14.457-32.289,32.29-32.289
              s32.29,14.457,32.29,32.289C493.287,500.777,478.83,515.234,460.997,515.234z M723.497,649.155
              c-67.611,0-122.421-54.811-122.421-122.421s54.81-122.42,122.421-122.42s122.421,54.81,122.421,122.42
              S791.108,649.155,723.497,649.155z"
          />
        </svg>
      {/snippet}
    </StatCard>

    <!-- Unique Species Card -->
    <StatCard
      title={t('analytics.stats.uniqueSpecies')}
      value={formatNumber(summary.uniqueSpecies)}
      subtitle={getPeriodLabel()}
      iconClassName="bg-secondary/20"
      {isLoading}
    >
      {#snippet icon()}
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 text-secondary"
          viewBox="0 0 256 256"
          fill="currentColor"
          aria-hidden="true"
        >
          <path
            d="M236.4375,73.34375,213.207,57.85547A60.00943,60.00943,0,0,0,96,76V93.19385L1.75293,211.00244A7.99963,7.99963,0,0,0,8,224H112A104.11791,104.11791,0,0,0,216,120V100.28125l20.4375-13.625a7.99959,7.99959,0,0,0,0-13.3125Zm-126.292,67.77783-40,48a7.99987,7.99987,0,0,1-12.291-10.24316l40-48a7.99987,7.99987,0,0,1,12.291,10.24316ZM164,80a12,12,0,1,1,12-12A12,12,0,0,1,164,80Z"
          />
        </svg>
      {/snippet}
    </StatCard>

    <!-- Average Confidence Card -->
    <StatCard
      title={t('analytics.stats.avgConfidence')}
      value={formatPercentage(summary.avgConfidence)}
      subtitle={getPeriodLabel()}
      iconClassName="bg-accent/20"
      {isLoading}
    >
      {#snippet icon()}
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 text-accent"
          viewBox="0 0 20 20"
          fill="currentColor"
          aria-hidden="true"
        >
          <path
            fill-rule="evenodd"
            d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a.75.75 0 000 1.5h.253a.25.25 0 01.244.304l-.459 2.066A1.75 1.75 0 0010.747 15H11a.75.75 0 000-1.5h-.253a.25.25 0 01-.244-.304l.459-2.066A1.75 1.75 0 009.253 9H9z"
            clip-rule="evenodd"
          />
        </svg>
      {/snippet}
    </StatCard>

    <!-- Most Common Species Card -->
    <StatCard
      title={t('analytics.stats.mostCommon')}
      value={summary.mostCommonSpecies || t('analytics.stats.none')}
      subtitle={summary.mostCommonCount > 0
        ? formatNumber(summary.mostCommonCount) + ' ' + t('analytics.stats.detections')
        : ''}
      iconClassName="bg-success/20"
      valueClassName="text-lg truncate max-w-[150px]"
      {isLoading}
    >
      {#snippet icon()}
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 text-success"
          viewBox="0 0 20 20"
          fill="currentColor"
          aria-hidden="true"
        >
          <path
            fill-rule="evenodd"
            d="M3.293 9.707a1 1 0 010-1.414l6-6a1 1 0 011.414 0l6 6a1 1 0 01-1.414 1.414L11 5.414V17a1 1 0 11-2 0V5.414L4.707 9.707a1 1 0 01-1.414 0z"
            clip-rule="evenodd"
          />
        </svg>
      {/snippet}
    </StatCard>
  </div>

  <!-- Filter Controls -->
  <FilterForm bind:filters {isLoading} onSubmit={fetchData} onReset={resetFilters} />

  <!-- Charts Section -->
  <div class="grid gap-4 charts-grid">
    <!-- Species Distribution Chart -->
    <ChartCard title={t('analytics.charts.top10Species')} chartId="speciesChart" {isLoading} />

    <!-- Time of Day Chart -->
    <ChartCard
      title={t('analytics.charts.detectionsByTimeOfDay')}
      chartId="timeOfDayChart"
      {isLoading}
    />
  </div>

  <!-- Trend Charts -->
  <ChartCard title={t('analytics.charts.detectionTrends')} chartId="trendChart" {isLoading} />

  <!-- New Species Chart -->
  <ChartCard
    title={t('analytics.charts.newSpeciesDetected')}
    chartId="newSpeciesChart"
    {isLoading}
    showEmpty={!isLoading && newSpeciesData.length === 0}
    emptyMessage={t('analytics.charts.noNewSpecies')}
    chartHeight="h-auto"
  />

  <!-- Data Table for Recent Detections -->
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body card-padding">
      <h2 class="card-title">{t('analytics.recentDetections.title')}</h2>
      {#if isLoading}
        <div class="flex justify-center items-center p-8">
          <span class="loading loading-spinner loading-lg text-primary"></span>
        </div>
      {:else}
        <div class="overflow-x-auto">
          <table class="table w-full">
            <thead>
              <tr>
                <th>{t('analytics.recentDetections.headers.dateTime')}</th>
                <th>{t('analytics.recentDetections.headers.species')}</th>
                <th>{t('analytics.recentDetections.headers.confidence')}</th>
                <th>{t('analytics.recentDetections.headers.timeOfDay')}</th>
              </tr>
            </thead>
            <tbody>
              {#each recentDetections as detection, index}
                <tr class={index % 2 === 0 ? 'bg-base-100' : 'bg-base-200'}>
                  <td>{formatDateTime(detection.timestamp)}</td>
                  <td>
                    <div class="flex items-center gap-2">
                      <div class="w-8 h-8 rounded-full bg-base-200 overflow-hidden">
                        <img
                          src="/api/v2/media/species-image?name={encodeURIComponent(
                            detection.scientificName
                          )}"
                          alt={detection.commonName || 'Unknown species'}
                          class="w-full h-full object-cover"
                          onerror={e =>
                            ((e.currentTarget as HTMLImageElement).src =
                              '/assets/images/bird-placeholder.svg')}
                          loading="lazy"
                        />
                      </div>
                      <div>
                        <div class="font-medium">
                          {detection.commonName || t('analytics.recentDetections.unknownSpecies')}
                        </div>
                        <div class="text-xs opacity-50">{detection.scientificName || ''}</div>
                      </div>
                    </div>
                  </td>
                  <td>
                    <div class="flex items-center gap-2">
                      <div class="w-16 h-4 rounded-full overflow-hidden bg-base-200">
                        <div
                          class="h-full {detection.confidence >= 0.8
                            ? 'bg-success'
                            : detection.confidence >= 0.4
                              ? 'bg-warning'
                              : 'bg-error'}"
                          style:width="{detection.confidence * 100}%"
                        ></div>
                      </div>
                      <span class="text-sm">{formatPercentage(detection.confidence)}</span>
                    </div>
                  </td>
                  <td>{detection.timeOfDay || t('analytics.recentDetections.unknown')}</td>
                </tr>
              {:else}
                <tr>
                  <td colspan="4" class="text-center py-4 text-base-content/50"
                    >{t('analytics.recentDetections.noRecentDetections')}</td
                  >
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>
  </div>
</div>

<style>
  .card-padding {
    padding: 1rem;
  }

  @media (min-width: 768px) {
    .card-padding {
      padding: 1.5rem;
    }
  }

  /* Summary cards grid - matches grid-cols-1 md:grid-cols-2 lg:grid-cols-4 */
  .summary-cards-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .summary-cards-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (min-width: 1024px) {
    .summary-cards-grid {
      grid-template-columns: repeat(4, minmax(0, 1fr));
    }
  }

  /* Charts grid - matches grid-cols-1 lg:grid-cols-2 */
  .charts-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 1024px) {
    .charts-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
</style>
