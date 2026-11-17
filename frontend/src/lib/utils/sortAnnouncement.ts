import { t } from '$lib/i18n';

/**
 * Builds the message a table announces once after the user sorts it, for example
 * "Sorted by Score, ascending". Name the column by its visible label, as the header button
 * does. Set the result on the table's single always-mounted `role="status"` element from the
 * sort handler, never from derived state, so it is spoken once per sort and not on first render.
 */
export function sortAnnouncement(column: string, direction: 'asc' | 'desc'): string {
  return t(
    direction === 'asc'
      ? 'dataDisplay.table.sortedAscending'
      : 'dataDisplay.table.sortedDescending',
    { column }
  );
}
