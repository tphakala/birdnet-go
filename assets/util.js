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

htmx.on('htmx:afterSettle', function (event) {
    if (event.detail.target.id.endsWith('-content')) {
        // Find all chart containers in the newly loaded content and render them
        event.detail.target.querySelectorAll('[id$="-chart"]').forEach(function (chartContainer) {
            renderChart(chartContainer.id, chartContainer.dataset.chartOptions);
        });
    }
});
