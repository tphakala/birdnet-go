function moveDatePicker(days) {
	const picker = document.getElementById('datePicker');

	const yy = picker.value.split('-')[0];
	const mm = picker.value.split('-')[1];
	const dd = picker.value.split('-')[2];

	const d = new Date(yy, mm - 1, dd)
	d.setDate(d.getDate() + days);
	picker.value = d.toLocaleString('sv').split(' ')[0];
	picker.dispatchEvent(new Event('change'))
}
