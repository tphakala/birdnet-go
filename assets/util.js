function logout() {
	window.location.href = `/logout`;
}

function moveDatePicker(days) {
	const picker = document.getElementById('datePicker');
	const [yy, mm, dd] = picker.value.split('-');
	const date = new Date(yy, mm - 1, dd);

	date.setDate(date.getDate() + days);
	picker.value = date.toLocaleDateString('sv');
	picker.dispatchEvent(new Event('change'));
}

function renderChart(chartId, chartData) {
	const chart = echarts.init(document.getElementById(chartId));
	chart.setOption(chartData);

	window.addEventListener('resize', () => chart.resize());
}

function isNotArrowKey(event) {
	return !['ArrowLeft', 'ArrowRight'].includes(event.key);
}

function isLocationDashboard() {
	const pathname = window.location.pathname;
	return pathname === '/' || pathname.endsWith('/dashboard');
}

function initializeDatePicker() {
	const picker = document.getElementById('datePicker');
	if (!picker) return;

	// Try to get date from URL hash first, then use current date
	const hashDate = window.location.hash.substring(1);

	// Helper to ensure a zero-padded YYYY-MM-DD date string consistently across browsers
	function getIsoDateString(date) {
		const year = date.getFullYear();
		const month = String(date.getMonth() + 1).padStart(2, '0');
		const day = String(date.getDate()).padStart(2, '0');
		return `${year}-${month}-${day}`;
	}

	const today = getIsoDateString(new Date());
	const newValue = hashDate || today;

	if (hashDate === '') {
		// Allow the dashboard root path to always load "today" without
		// triggering a page change
		picker.value = today
		picker.classList.remove('invisible');
		htmx.trigger(picker, 'load');
	}
	else if (picker.value !== newValue) {
		// Only trigger change if the value is actually different
		picker.value = newValue;
		picker.classList.remove('invisible');
		htmx.trigger(picker, 'change');
	} else if (!document.getElementById('topBirdsChart').children.length) {
		// If the value hasn't changed but the chart is empty, trigger the change
		htmx.trigger(picker, 'change');
	}
}

htmx.on('htmx:afterSettle', function (event) {
    // Skip if target or id is not available
    if (!event.detail?.target) return;
    
    // Get the target id, ensuring it's a string
    const targetId = String(event.detail.target?.id || '');
    
    if (targetId.endsWith('-content')) {
        // Find all chart containers in the newly loaded content and render them
        event.detail.target.querySelectorAll('[id$="-chart"]').forEach(function (chartContainer) {
            renderChart(chartContainer.id, chartContainer.dataset.chartOptions);
        });
    }
    
    // Initialize date picker if we're on the dashboard
    if (isLocationDashboard()) {
        initializeDatePicker();
    }
});

// Add document ready listener to handle initial page load
document.addEventListener('DOMContentLoaded', function() {
	if (isLocationDashboard()) {
		initializeDatePicker();
	}
});
