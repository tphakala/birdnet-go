/**
 * clipExport.ts
 *
 * Helpers for naming the file a clip export downloads. The container extension
 * for a clip format must match the backend's clipFileExtension
 * (internal/api/v2/media/media.go), which names the file in the response's
 * Content-Disposition header.
 */

/** Clip formats whose container extension differs from the format name. */
const CONTAINER_EXTENSIONS: ReadonlyMap<string, string> = new Map([
  ['aac', 'm4a'],
  ['opus', 'ogg'],
]);

/**
 * Return the file extension (without a dot) for an exported clip format:
 * AAC is delivered in an M4A container and Opus in an Ogg container, every other
 * format uses its own name.
 */
export function clipExportExtension(format: string): string {
  return CONTAINER_EXTENSIONS.get(format) ?? format;
}
