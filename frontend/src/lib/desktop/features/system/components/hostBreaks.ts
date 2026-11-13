const LOCATION_SEGMENT = /[^.\-:/]*[.\-:/]|[^.\-:/]+/g;

/**
 * Splits a database location such as `host:port/database` into segments that each end with a
 * `.`, `-`, `:` or `/`, except that the last may have no separator. A renderer can allow a line
 * break between segments, but not after the last. Joining the segments gives back the input
 * exactly. An empty string gives no segments; a string without separators gives one.
 */
export function splitHostSegments(location: string): string[] {
  return location.match(LOCATION_SEGMENT) ?? [];
}
