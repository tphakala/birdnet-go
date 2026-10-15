/**
 * IP address and CIDR validation that mirrors Go's net.ParseIP and
 * net.ParseCIDR, which is what the backend uses to accept security.trustedProxies
 * and the allowed-subnet list. The server answers an invalid entry with a generic
 * 400, so this client validator is the real feedback and must accept exactly what
 * Go accepts for the same mode.
 *
 * The rules, as measured against Go 1.27:
 * - IPv4 is four dotted decimal fields of 0-255 without leading zeros.
 * - IPv6 has hex groups of 1-4 digits, at most one `::` standing for at least one
 *   group, and an optional embedded IPv4 tail. Zones (`%eth0`) and brackets are
 *   rejected.
 * - A CIDR prefix is digits only (leading zeros allowed), 0-32 for IPv4 text and
 *   0-128 for IPv6 text, whatever the address is.
 * - An IPv4-mapped IPv6 CIDR with a prefix of 96 or more is the IPv4 network with
 *   the prefix reduced by 96; with a shorter prefix it stays an IPv6 network.
 *
 * All functions are pure and trim nothing except isValidNetworkAddress,
 * networkAddressKey, networkContains and isSingleHost, which trim first like the
 * backend does (see trimGoSpace).
 */

/** Which kinds of address a field accepts. */
export type NetworkAddressMode = 'cidr' | 'ip' | 'ip-or-cidr';

const IPV4_BITS = 32;
const IPV6_BITS = 128;
const BITS_PER_BYTE = 8;
const IPV4_BYTES = 4;
const IPV6_BYTES = 16;
const IPV6_GROUPS = 8;
const BYTES_PER_GROUP = 2;
const MAX_IPV6_GROUP_DIGITS = 4;
const MAX_IPV4_FIELD_VALUE = 255;
const HEX_RADIX = 16;
const DECIMAL_RADIX = 10;
/** Prefix length from which an IPv4-mapped IPv6 CIDR is an IPv4 network (the 96 bits of ::ffff:0:0). */
const MAPPED_PREFIX_BITS = IPV6_BITS - IPV4_BITS;
/** Index of the 0xffff marker inside an IPv4-mapped IPv6 address. */
const MAPPED_MARKER_INDEX = 10;
const BYTE_MASK = 0xff;

const IPV4_PATTERN = /^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$/;
const HEX_GROUP_PATTERN = /^[0-9a-fA-F]+/;
const DIGITS_PATTERN = /^[0-9]+$/;
/**
 * The code points Go's strings.TrimSpace trims (Unicode White_Space). JavaScript's
 * String.prototype.trim differs: it also trims U+FEFF and does not trim U+0085.
 */
const GO_SPACE_CODES: ReadonlySet<number> = new Set([
  0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0x85, 0xa0, 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004,
  0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000,
]);

/**
 * Trim s the way Go's strings.TrimSpace does, which the backend applies to these values.
 * Two indexes walk in from the ends, so the cost is linear however long the whitespace
 * run inside the text is.
 */
export function trimGoSpace(s: string): string {
  let start = 0;
  let end = s.length;
  while (start < end && GO_SPACE_CODES.has(s.charCodeAt(start))) start++;
  while (end > start && GO_SPACE_CODES.has(s.charCodeAt(end - 1))) end--;
  return s.slice(start, end);
}

/**
 * Parse a dotted decimal IPv4 address into its four bytes, or null.
 * Leading zeros in a field are rejected, as Go does.
 */
export function parseIPv4(s: string): number[] | null {
  const match = IPV4_PATTERN.exec(s);
  if (!match) return null;
  const bytes: number[] = [];
  for (const field of match.slice(1)) {
    if (field.length > 1 && field.startsWith('0')) return null;
    const value = Number.parseInt(field, DECIMAL_RADIX);
    if (value > MAX_IPV4_FIELD_VALUE) return null;
    bytes.push(value);
  }
  return bytes;
}

/**
 * Parse an IPv6 address into its sixteen bytes, or null. Follows Go's parser:
 * no zones, no brackets, `::` must cover at least one group, and an embedded IPv4
 * tail must be a valid IPv4 address.
 */
export function parseIPv6(s: string): number[] | null {
  if (s.length === 0 || s.includes('%')) return null;

  const groups: number[] = [];
  let ellipsisAt = -1; // index in `groups` where `::` sits
  let rest = s;

  if (rest.startsWith('::')) {
    ellipsisAt = 0;
    rest = rest.slice(2);
  }

  while (rest.length > 0) {
    const hex = HEX_GROUP_PATTERN.exec(rest);
    if (!hex || hex[0].length > MAX_IPV6_GROUP_DIGITS) {
      // Not a group; a digit run longer than 4 also lands here.
      return null;
    }
    const afterHex = rest.slice(hex[0].length);

    // A dot after the digits starts an embedded IPv4 tail, which must be the end.
    if (afterHex.startsWith('.')) {
      const v4 = parseIPv4(rest);
      if (!v4) return null;
      groups.push((v4[0] << BITS_PER_BYTE) | v4[1], (v4[2] << BITS_PER_BYTE) | v4[3]);
      rest = '';
      break;
    }

    groups.push(Number.parseInt(hex[0], HEX_RADIX));
    rest = afterHex;
    if (rest.length === 0) break;

    // Another group must follow a colon; a lone trailing colon is an error.
    if (!rest.startsWith(':') || rest.length === 1) return null;
    rest = rest.slice(1);

    if (rest.startsWith(':')) {
      if (ellipsisAt >= 0) return null;
      ellipsisAt = groups.length;
      rest = rest.slice(1);
    }
  }

  if (groups.length > IPV6_GROUPS) return null;
  if (groups.length < IPV6_GROUPS) {
    if (ellipsisAt < 0) return null;
    const missing = IPV6_GROUPS - groups.length;
    groups.splice(ellipsisAt, 0, ...Array<number>(missing).fill(0));
  } else if (ellipsisAt >= 0) {
    // `::` must stand for at least one zero group.
    return null;
  }

  const bytes: number[] = [];
  for (const group of groups) {
    bytes.push((group >> BITS_PER_BYTE) & BYTE_MASK, group & BYTE_MASK);
  }
  return bytes;
}

/** A parsed address: its bytes (4 for IPv4 text, 16 for IPv6 text). */
interface ParsedAddress {
  bytes: number[];
}

function parseAddress(s: string): ParsedAddress | null {
  const v4 = parseIPv4(s);
  if (v4) return { bytes: v4 };
  const v6 = parseIPv6(s);
  return v6 ? { bytes: v6 } : null;
}

/** Bit width of the text family an address was written in. */
function bitWidth(address: ParsedAddress): number {
  return address.bytes.length === IPV4_BYTES ? IPV4_BITS : IPV6_BITS;
}

/** True for ::ffff:a.b.c.d, which Go treats as the IPv4 address a.b.c.d. */
function isMapped(bytes: number[]): boolean {
  return (
    bytes.length === IPV6_BYTES &&
    bytes.slice(0, MAPPED_MARKER_INDEX).every(byte => byte === 0) &&
    bytes
      .slice(MAPPED_MARKER_INDEX, MAPPED_MARKER_INDEX + BYTES_PER_GROUP)
      .every(byte => byte === BYTE_MASK)
  );
}

/** Whether s is an IPv4 or IPv6 address as net.ParseIP reads it. */
export function isValidIP(s: string): boolean {
  return parseAddress(s) !== null;
}

interface ParsedCIDR {
  address: ParsedAddress;
  prefix: number;
}

function parseCIDR(s: string): ParsedCIDR | null {
  const slash = s.indexOf('/');
  if (slash < 0) return null;
  const address = parseAddress(s.slice(0, slash));
  const prefixText = s.slice(slash + 1);
  if (!address || !DIGITS_PATTERN.test(prefixText)) return null;
  const prefix = Number.parseInt(prefixText, DECIMAL_RADIX);
  if (!Number.isSafeInteger(prefix) || prefix > bitWidth(address)) return null;
  return { address, prefix };
}

/** Whether s is a CIDR as net.ParseCIDR reads it. A bare IP is not a CIDR. */
export function isValidCIDR(s: string): boolean {
  return parseCIDR(s) !== null;
}

/**
 * Whether a field value is an address of the accepted kind. The value is trimmed
 * first with trimGoSpace, like the backend, so a blank value is not valid.
 */
export function isValidNetworkAddress(s: string, accept: NetworkAddressMode): boolean {
  const value = trimGoSpace(s);
  return (accept !== 'cidr' && isValidIP(value)) || (accept !== 'ip' && isValidCIDR(value));
}

/** Whether any of `values` is not an address of the accepted kind. */
export function hasInvalidAddress(values: readonly string[], mode: NetworkAddressMode): boolean {
  return values.some(value => !isValidNetworkAddress(value, mode));
}

/** Keep the first `prefix` bits of bytes and clear the rest. */
function maskBytes(bytes: number[], prefix: number): number[] {
  return bytes.map((byte, index) => {
    const bitsInByte = Math.min(BITS_PER_BYTE, Math.max(0, prefix - index * BITS_PER_BYTE));
    const mask = (BYTE_MASK << (BITS_PER_BYTE - bitsInByte)) & BYTE_MASK;
    return byte & mask;
  });
}

function keyFromBytes(bytes: number[], prefix: number): string {
  if (bytes.length === IPV4_BYTES) return `${bytes.join('.')}/${prefix}`;
  const groups: string[] = [];
  for (let i = 0; i < IPV6_BYTES; i += BYTES_PER_GROUP) {
    const [high = 0, low = 0] = bytes.slice(i, i + BYTES_PER_GROUP);
    groups.push(((high << BITS_PER_BYTE) | low).toString(HEX_RADIX));
  }
  return `${groups.join(':')}/${prefix}`;
}

/** A network as the backend reads it: masked bytes (4 or 16) and the prefix length. */
interface CanonicalNetwork {
  bytes: number[];
  prefix: number;
}

/**
 * The network an entry describes, or null when it is neither an IP nor a CIDR. A
 * bare IP is its single-host network, an IPv4-mapped bare IP is IPv4, and an
 * IPv4-mapped CIDR of 96 bits or more is the IPv4 network (as Go's ParseCIDR).
 */
function canonicalNetwork(value: string): CanonicalNetwork | null {
  const cidr = parseCIDR(value);
  if (cidr) {
    const { address, prefix } = cidr;
    if (isMapped(address.bytes) && prefix >= MAPPED_PREFIX_BITS) {
      const v4Prefix = prefix - MAPPED_PREFIX_BITS;
      return {
        bytes: maskBytes(address.bytes.slice(IPV6_BYTES - IPV4_BYTES), v4Prefix),
        prefix: v4Prefix,
      };
    }
    return { bytes: maskBytes(address.bytes, prefix), prefix };
  }

  const address = parseAddress(value);
  if (!address) return null;
  if (isMapped(address.bytes)) {
    return { bytes: address.bytes.slice(IPV6_BYTES - IPV4_BYTES), prefix: IPV4_BITS };
  }
  return { bytes: address.bytes, prefix: bitWidth(address) };
}

/**
 * Canonical key for duplicate detection, or null when s is neither an IP nor a
 * CIDR (after trimGoSpace). Two values share a key exactly when they describe the same set of hosts
 * the way the backend reads them: the network is masked, a bare IP is its single
 * host prefix, and an IPv4-mapped IPv6 address keys as IPv4 (Go's ParseIP
 * returns it as IPv4, and a mapped CIDR of 96 bits or more is the IPv4 network).
 * Hex is lowercase and uses all eight groups.
 */
export function networkAddressKey(s: string): string | null {
  const network = canonicalNetwork(trimGoSpace(s));
  return network ? keyFromBytes(network.bytes, network.prefix) : null;
}

/**
 * Whether the network an entry describes (an IP or a CIDR, read as in
 * networkAddressKey) contains the IP address `address`, with the semantics of Go's
 * net.IPNet.Contains: an IPv4 address (including an IPv4-mapped one) is only in an
 * IPv4 network, and an IPv6 address only in an IPv6 network. Both values are
 * trimmed with trimGoSpace first. False when either value does not parse.
 */
export function networkContains(entry: string, address: string): boolean {
  const network = canonicalNetwork(trimGoSpace(entry));
  const parsed = parseAddress(trimGoSpace(address));
  if (!network || !parsed) return false;
  const bytes = isMapped(parsed.bytes) ? parsed.bytes.slice(IPV6_BYTES - IPV4_BYTES) : parsed.bytes;
  if (bytes.length !== network.bytes.length) return false;
  const masked = maskBytes(bytes, network.prefix);
  return masked.every((byte, index) => byte === network.bytes.at(index));
}

/**
 * Whether s names exactly one host: a bare IP, or a CIDR whose prefix covers the
 * whole address width. Invalid values are not single hosts.
 */
export function isSingleHost(s: string): boolean {
  const network = canonicalNetwork(trimGoSpace(s));
  return network !== null && network.prefix === network.bytes.length * BITS_PER_BYTE;
}
