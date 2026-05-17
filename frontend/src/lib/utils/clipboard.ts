export const COPY_FEEDBACK_TIMEOUT_MS = 2000;

/**
 * Copy text to clipboard with execCommand fallback for non-HTTPS contexts.
 * Falls back to textarea if the Clipboard API is unavailable or rejects.
 * Returns true if the copy succeeded.
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- clipboard API is undefined on plain HTTP
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Permission denied or other failure; fall through to textarea fallback
    }
  }
  return fallbackCopy(text);
}

function fallbackCopy(text: string): boolean {
  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  document.body.appendChild(textarea);
  try {
    textarea.select();
    return document.execCommand('copy');
  } catch {
    return false;
  } finally {
    document.body.removeChild(textarea);
  }
}
