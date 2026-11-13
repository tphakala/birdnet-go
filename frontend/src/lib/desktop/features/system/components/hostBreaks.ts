const HOST_BREAK_AFTER = /[.\-:]/;

/**
 * Splits a host string into segments that each end with a `.`, `-` or `:`, so a renderer can
 * allow a line break after every segment. Joining the segments gives back the input exactly.
 * An empty string gives no segments; a string without separators gives one.
 */
export function splitHostSegments(host: string): string[] {
  const segments: string[] = [];
  let start = 0;
  for (let i = 0; i < host.length; i++) {
    if (HOST_BREAK_AFTER.test(host[i])) {
      segments.push(host.slice(start, i + 1));
      start = i + 1;
    }
  }
  if (start < host.length) segments.push(host.slice(start));
  return segments;
}
