const HOST_SEGMENT = /[^.\-:]*[.\-:]|[^.\-:]+/g;

/**
 * Splits a host string into segments that each end with a `.`, `-` or `:`, so a renderer can
 * allow a line break after every segment. Joining the segments gives back the input exactly.
 * An empty string gives no segments; a string without separators gives one.
 */
export function splitHostSegments(host: string): string[] {
  return host.match(HOST_SEGMENT) ?? [];
}
