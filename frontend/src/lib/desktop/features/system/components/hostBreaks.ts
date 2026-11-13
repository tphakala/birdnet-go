const HOST_SEGMENT = /[^.\-:]*[.\-:]|[^.\-:]+/g;

/**
 * Splits a host string into segments that each end with a `.`, `-` or `:`, except that the last
 * may have no separator. A renderer can allow a line break between segments, but not after the
 * last. Joining the segments gives back the input exactly. An empty string gives no segments; a
 * string without separators gives one.
 */
export function splitHostSegments(host: string): string[] {
  return host.match(HOST_SEGMENT) ?? [];
}
