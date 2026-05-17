export const COPY_FEEDBACK_TIMEOUT_MS = 2000;

/**
 * Copy text to clipboard with execCommand fallback for non-HTTPS contexts.
 * Falls back to textarea if the Clipboard API is unavailable or rejects.
 * Pass targetDoc when copying from a popup window so the fallback textarea
 * is appended to the correct document.
 */
export async function copyToClipboard(text: string, targetDoc?: Document): Promise<boolean> {
  const nav = targetDoc?.defaultView?.navigator ?? navigator;
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- clipboard API is undefined on plain HTTP
  if (nav.clipboard?.writeText) {
    try {
      await nav.clipboard.writeText(text);
      return true;
    } catch {
      // Permission denied or other failure; fall through to textarea fallback
    }
  }
  return fallbackCopy(text, targetDoc);
}

function fallbackCopy(text: string, targetDoc?: Document): boolean {
  const doc = targetDoc ?? document;
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- body is null when a popup document is destroyed
  if (!doc.body) return false;
  const textarea = doc.createElement('textarea');
  textarea.value = text;
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  doc.body.appendChild(textarea);
  try {
    textarea.select();
    return doc.execCommand('copy');
  } catch {
    return false;
  } finally {
    doc.body.removeChild(textarea);
  }
}
