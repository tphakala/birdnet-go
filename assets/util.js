function moveDatePicker(days) {
	const picker = document.getElementById('datePicker');

	const [yy, mm, dd] = picker.value.split('-');

	const d = new Date(yy, mm - 1, dd)
	d.setDate(d.getDate() + days);
	picker.value = d.toLocaleString('sv').split(' ')[0];
	picker.dispatchEvent(new Event('change'))
}
