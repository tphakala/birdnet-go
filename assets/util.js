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
	const today = new Date().toLocaleDateString('sv'); // 'sv' locale gives YYYY-MM-DD format
	
	// Only set value and trigger change if the value is actually different
	if (picker.value !== (hashDate || today)) {
		picker.value = hashDate || today;
		picker.classList.remove('invisible'); // Make picker visible
		picker.dispatchEvent(new Event('change'));
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
