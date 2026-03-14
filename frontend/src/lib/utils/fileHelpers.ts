const DEFAULT_MAX_SIZE_BYTES = 1_048_576; // 1 MB

/**
 * Reads a file as text content using FileReader API.
 * Rejects if the file exceeds maxSizeBytes or if reading fails.
 */
export function readFileAsText(
  file: File,
  maxSizeBytes: number = DEFAULT_MAX_SIZE_BYTES
): Promise<string> {
  return new Promise((resolve, reject) => {
    if (file.size > maxSizeBytes) {
      const maxMB = Math.round(maxSizeBytes / 1_048_576);
      reject(new Error(`File exceeds the ${maxMB}MB limit`));
      return;
    }

    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as string);
    reader.onerror = () => reject(new Error('Failed to read file'));
    reader.readAsText(file);
  });
}
