/**
 * security.allowSubnetBypass.subnet is a comma-separated string; splitSubnetList
 * and joinSubnetList convert it to and from list rows. Blank values are not rows,
 * and a list that equals the saved one gives the saved string back byte for byte.
 */

import { trimGoSpace } from '$lib/utils/networkAddress';
import { deepEqual } from '$lib/utils/settingsChanges';

/** Separator written between subnets when a list is saved as text. */
const SUBNET_SEPARATOR = ', ';

/** Split the comma-separated subnet text into trimmed, non-blank values, as the backend does. */
export function splitSubnetList(text: string): string[] {
  return text
    .split(',')
    .map(value => trimGoSpace(value))
    .filter(value => value !== '');
}

/**
 * Join subnet values into the text to store. Returns `saved` itself when it
 * already describes exactly `values`, so edits that return to the saved list
 * restore the saved string byte for byte; otherwise the values joined with ", ".
 */
export function joinSubnetList(values: readonly string[], saved: string): string {
  return deepEqual(splitSubnetList(saved), values) ? saved : values.join(SUBNET_SEPARATOR);
}
