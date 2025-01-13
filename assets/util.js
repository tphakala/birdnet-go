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

	const today = getIsoDateString(new Date()); // ensures consistent 'YYYY-MM-DD' format
	
	// Set the value first
	const newValue = hashDate || today;
	
	// Only trigger change if the picker is invisible (first load)
	// or if the value has actually changed
	if (picker.classList.contains('invisible') || picker.value !== newValue) {
		picker.value = newValue;
		picker.classList.remove('invisible'); // Make picker visible
		htmx.trigger(picker, 'change');
	} else {
		picker.classList.remove('invisible'); // Make picker visible
	}
}

htmx.on('htmx:afterSettle', function (event) {
	if (event.detail.target.id.endsWith('-content')) {
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
