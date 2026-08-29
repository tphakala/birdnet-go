/**
 * Generate a session identifier used to distinguish concurrent live-audio
 * consumers (the HLS audio player and the dashboard spectrogram) running on the
 * same host. The backend keys its per-client reference count on this value, so it
 * MUST be unique per component and in a format the backend accepts (a canonical
 * UUID or another safe token). If two components send the same identifier the
 * backend collapses them into one client, and the first to stop tears the shared
 * stream down for the other.
 *
 * `crypto.randomUUID()` is only defined in a secure context, and BirdNET-Go is
 * commonly served over plain HTTP on home networks, so we must not rely on it.
 * We fall back to `crypto.getRandomValues()` (available on plain HTTP) and, if the
 * Web Crypto API is absent or throws, to `Math.random()`. All three paths return a
 * valid RFC 4122 v4 UUID. The identifier is a concurrency token, not an
 * authentication secret, so the non-cryptographic last resort is acceptable.
 */
export function generateSessionId(): string {
  return uuidv4();
}

function uuidv4(): string {
  const native = tryNativeUUID();
  if (native !== null) {
    return native;
  }

  const bytes = randomBytes(16);
  const hex = Array.from(bytes, (byte, index) => {
    let value = byte;
    if (index === 6) {
      value = (value & 0x0f) | 0x40; // version 4
    } else if (index === 8) {
      value = (value & 0x3f) | 0x80; // variant 10xx
    }
    return value.toString(16).padStart(2, '0');
  }).join('');

  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function tryNativeUUID(): string | null {
  try {
    if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
      return crypto.randomUUID();
    }
  } catch {
    // Secure-context guard threw; fall back to manual generation.
  }
  return null;
}

function randomBytes(length: number): Uint8Array {
  const bytes = new Uint8Array(length);
  try {
    if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
      crypto.getRandomValues(bytes);
      return bytes;
    }
  } catch {
    // getRandomValues threw (extremely rare); fall back to Math.random below.
  }
  return bytes.map(() => Math.floor(Math.random() * 256));
}
