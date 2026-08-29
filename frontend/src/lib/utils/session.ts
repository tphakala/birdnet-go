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

// RFC 4122 v4 UUID layout constants (kept named so the bit handling is auditable).
const UUID_BYTE_LENGTH = 16;
const UUID_VERSION_BYTE_INDEX = 6; // byte carrying the 4-bit version field
const UUID_VARIANT_BYTE_INDEX = 8; // byte carrying the 2-bit variant field
const UUID_VERSION_NIBBLE_MASK = 0x0f; // clears the high nibble before setting the version
const UUID_VERSION_4_BITS = 0x40; // version 4 (0100) in the high nibble
const UUID_VARIANT_BITS_MASK = 0x3f; // clears the top two bits before setting the variant
const UUID_VARIANT_RFC4122_BITS = 0x80; // variant 10xx
const UUID_GROUP_LENGTHS = [8, 4, 4, 4, 12] as const; // canonical 8-4-4-4-12 hex grouping

const HEX_RADIX = 16;
const HEX_BYTE_WIDTH = 2; // hex digits per byte
const HEX_PADDING_CHARACTER = '0'; // left-pad for a single-digit hex byte
const UUID_GROUP_SEPARATOR = '-'; // separator between the 8-4-4-4-12 groups
const BYTE_VALUE_COUNT = 256; // number of distinct byte values [0, 255]

export function generateSessionId(): string {
  return uuidv4();
}

function uuidv4(): string {
  const native = tryNativeUUID();
  if (native !== null) {
    return native;
  }

  const bytes = randomBytes(UUID_BYTE_LENGTH);
  const hex = Array.from(bytes, (byte, index) => {
    let value = byte;
    if (index === UUID_VERSION_BYTE_INDEX) {
      value = (value & UUID_VERSION_NIBBLE_MASK) | UUID_VERSION_4_BITS;
    } else if (index === UUID_VARIANT_BYTE_INDEX) {
      value = (value & UUID_VARIANT_BITS_MASK) | UUID_VARIANT_RFC4122_BITS;
    }
    return value.toString(HEX_RADIX).padStart(HEX_BYTE_WIDTH, HEX_PADDING_CHARACTER);
  }).join('');

  const groups: string[] = [];
  let offset = 0;
  for (const length of UUID_GROUP_LENGTHS) {
    groups.push(hex.slice(offset, offset + length));
    offset += length;
  }
  return groups.join(UUID_GROUP_SEPARATOR);
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
  return bytes.map(() => Math.floor(Math.random() * BYTE_VALUE_COUNT));
}
