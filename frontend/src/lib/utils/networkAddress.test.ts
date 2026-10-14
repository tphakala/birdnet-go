import { describe, it, expect } from 'vitest';
import {
  parseIPv4,
  parseIPv6,
  isValidIP,
  isValidCIDR,
  isValidNetworkAddress,
  hasInvalidAddress,
  networkAddressKey,
  isSingleHost,
  networkContains,
  trimGoSpace,
} from './networkAddress';

describe('parseIPv4', () => {
  it('parses dotted decimal', () => {
    expect(parseIPv4('192.168.1.1')).toEqual([192, 168, 1, 1]);
    expect(parseIPv4('0.0.0.0')).toEqual([0, 0, 0, 0]);
  });

  it.each([
    '192.168.01.1',
    '1.2.3',
    '1.2.3.4.5',
    '256.1.1.1',
    '1.2.3.',
    '',
    ' 1.2.3.4',
    '1.2.3.-4',
  ])('rejects %j', input => {
    expect(parseIPv4(input)).toBeNull();
  });
});

describe('parseIPv6', () => {
  it('parses compressed and full forms', () => {
    expect(parseIPv6('::1')).toEqual([...Array(15).fill(0), 1]);
    expect(parseIPv6('::')).toEqual(Array(16).fill(0));
    expect(parseIPv6('1:2:3:4:5:6:7:8')).toEqual([0, 1, 0, 2, 0, 3, 0, 4, 0, 5, 0, 6, 0, 7, 0, 8]);
    expect(parseIPv6('2001:DB8::1')).toEqual(parseIPv6('2001:db8:0:0:0:0:0:1'));
  });

  it('parses an embedded IPv4 tail', () => {
    expect(parseIPv6('::ffff:1.2.3.4')).toEqual([...Array(10).fill(0), 255, 255, 1, 2, 3, 4]);
  });

  it.each([
    'fe80::1%eth0',
    '[::1]',
    '1:2:3:4:5:6:7:8:9',
    '1::2::3',
    '1:2:3:4:5:6:7::8',
    ':1:2:3:4:5:6:7',
    '1:2:3:4:5:6:7:',
    ':::',
    '12345::1',
    '::1.2.3.4.5',
    '1.2.3.4::1',
    '::g',
    '1:2:3:4:5:6:7',
    '::ffff:1.2.3.04',
  ])('rejects %j', input => {
    expect(parseIPv6(input)).toBeNull();
  });
});

describe('isValidIP', () => {
  it.each(['192.168.1.1', '::1', '::ffff:1.2.3.4', '2001:DB8::1', '1:2:3:4:5:6:7::'])(
    'accepts %j',
    input => {
      expect(isValidIP(input)).toBe(true);
    }
  );

  it.each([
    '192.168.01.1',
    'fe80::1%eth0',
    '1.2.3',
    '[::1]',
    '1:2:3:4:5:6:7:8:9',
    '1::2::3',
    '10.0.0.0/8',
    '',
    'cloudflare',
  ])('rejects %j', input => {
    expect(isValidIP(input)).toBe(false);
  });
});

describe('isValidCIDR', () => {
  it.each([
    '2001:db8::/32',
    '192.168.1.5/24',
    '10.0.0.0/08',
    '::/0',
    '0.0.0.0/0',
    '1.2.3.4/32',
    'fe80::1/64',
    '::ffff:1.2.3.4/120',
    '::ffff:1.2.3.4/128',
    '::ffff:1.2.3.4/32',
    '1.2.3.4/032',
  ])('accepts %j', input => {
    expect(isValidCIDR(input)).toBe(true);
  });

  it.each([
    '10.0.0.0/33',
    '::/129',
    '10.0.0.1',
    '::1',
    '10.0.0.0/',
    '10.0.0.0/+8',
    '10.0.0.0/ 8',
    '10.0.0.0/0x8',
    '10.0.0.0/8/8',
    'fe80::1%eth0/64',
    '10.0.0.0/-1',
    '/8',
    '',
  ])('rejects %j', input => {
    expect(isValidCIDR(input)).toBe(false);
  });
});

describe('isValidNetworkAddress', () => {
  it('rejects blank values after trimming', () => {
    expect(isValidNetworkAddress('   ', 'cidr')).toBe(false);
    expect(isValidNetworkAddress('', 'ip-or-cidr')).toBe(false);
  });

  it('applies the mode', () => {
    expect(isValidNetworkAddress('10.0.0.1', 'cidr')).toBe(false);
    expect(isValidNetworkAddress('10.0.0.0/8', 'cidr')).toBe(true);
    expect(isValidNetworkAddress('10.0.0.0/8', 'ip')).toBe(false);
    expect(isValidNetworkAddress('10.0.0.1', 'ip')).toBe(true);
    expect(isValidNetworkAddress('10.0.0.1', 'ip-or-cidr')).toBe(true);
    expect(isValidNetworkAddress('10.0.0.0/8', 'ip-or-cidr')).toBe(true);
    expect(isValidNetworkAddress('nope', 'ip-or-cidr')).toBe(false);
  });

  it('trims like the backend', () => {
    expect(isValidNetworkAddress('  10.0.0.1 ', 'ip')).toBe(true);
  });

  it('accepts IPv6 where valid in each mode', () => {
    expect(isValidNetworkAddress('2001:db8::1', 'ip')).toBe(true);
    expect(isValidNetworkAddress('2001:db8::/32', 'cidr')).toBe(true);
    expect(isValidNetworkAddress('2001:db8::1', 'cidr')).toBe(false);
  });
});

describe('hasInvalidAddress', () => {
  it('is false for an empty list and for valid values', () => {
    expect(hasInvalidAddress([], 'ip-or-cidr')).toBe(false);
    expect(hasInvalidAddress(['10.0.0.1', '10.0.0.0/8'], 'ip-or-cidr')).toBe(false);
  });

  it('is true when any value is invalid in the mode', () => {
    expect(hasInvalidAddress(['10.0.0.0/8', 'nope'], 'ip-or-cidr')).toBe(true);
    expect(hasInvalidAddress(['10.0.0.0/8', '10.0.0.1'], 'cidr')).toBe(true);
  });
});

describe('networkAddressKey', () => {
  it('shares a key between a host CIDR and its masked network', () => {
    expect(networkAddressKey('192.168.1.5/24')).toBe(networkAddressKey('192.168.1.0/24'));
  });

  it('keys a bare IP as its single-host prefix', () => {
    expect(networkAddressKey('10.0.0.1')).toBe(networkAddressKey('10.0.0.1/32'));
    expect(networkAddressKey('10.0.0.1')).toBe(networkAddressKey('10.0.0.1/032'));
    expect(networkAddressKey('2001:db8::1')).toBe(networkAddressKey('2001:DB8:0::1/128'));
  });

  it('keys an IPv4-mapped bare IPv6 address as IPv4', () => {
    expect(networkAddressKey('::FFFF:1.2.3.4')).toBe(networkAddressKey('1.2.3.4'));
    expect(networkAddressKey('::ffff:102:304')).toBe(networkAddressKey('1.2.3.4'));
  });

  it('follows Go for IPv4-mapped IPv6 CIDRs', () => {
    // Measured with go1.27: ::ffff:1.2.3.4/120 parses to the network 1.2.3.0/24,
    // /128 to 1.2.3.4/32, and a prefix below 96 stays an IPv6 network.
    expect(networkAddressKey('::ffff:1.2.3.4/120')).toBe(networkAddressKey('1.2.3.0/24'));
    expect(networkAddressKey('::ffff:1.2.3.4/128')).toBe(networkAddressKey('1.2.3.4'));
    expect(networkAddressKey('::ffff:1.2.3.4/96')).toBe(networkAddressKey('0.0.0.0/0'));
    expect(networkAddressKey('::ffff:1.2.3.4/32')).toBe(networkAddressKey('::/32'));
    expect(networkAddressKey('::ffff:1.2.3.4/32')).not.toBe(networkAddressKey('1.2.3.4/32'));
  });

  it('keeps IPv4 and IPv6 networks apart', () => {
    expect(networkAddressKey('0.0.0.0/0')).not.toBe(networkAddressKey('::/0'));
  });

  it('trims and ignores case', () => {
    expect(networkAddressKey(' 2001:DB8::/32 ')).toBe(networkAddressKey('2001:db8::/32'));
  });

  it('returns null for invalid values', () => {
    expect(networkAddressKey('nope')).toBeNull();
    expect(networkAddressKey('')).toBeNull();
    expect(networkAddressKey('10.0.0.0/33')).toBeNull();
  });
});

describe('networkContains', () => {
  // Vectors measured with go1.27 net.ParseCIDR + IPNet.Contains (bare IP entries are
  // single hosts, as proxytrust reads them).
  it.each([
    ['0.0.0.0/0', '1.2.3.4', true],
    ['0.0.0.0/0', '::ffff:1.2.3.4', true],
    ['::/0', '1.2.3.4', false],
    ['::/0', '::ffff:1.2.3.4', false],
    ['::/0', '2001:db8::1', true],
    ['::ffff:0:0/96', '1.2.3.4', true],
    ['::ffff:1.2.3.0/120', '1.2.3.9', true],
    ['::ffff:1.2.3.4/32', '1.2.3.4', false],
    ['10.0.0.0/8', '10.200.1.1', true],
    ['10.0.0.0/8', '11.0.0.1', false],
    ['2001:db8::/32', '2001:db8:1::5', true],
    ['2001:db8::/32', '2001:db9::5', false],
    ['172.18.0.1/32', '172.18.0.1', true],
    ['fe80::/10', 'fe80::1', true],
    ['172.18.0.1', '172.18.0.1', true],
    ['::ffff:172.18.0.1', '172.18.0.1', true],
    ['172.18.0.1', '172.18.0.2', false],
    [' 192.168.0.0/16 ', ' 192.168.4.176 ', true],
  ] as const)('%s contains %s: %s', (entry, address, expected) => {
    expect(networkContains(entry, address)).toBe(expected);
  });

  it('is false for values that do not parse', () => {
    expect(networkContains('nonsense', '10.0.0.1')).toBe(false);
    expect(networkContains('10.0.0.0/8', '')).toBe(false);
    expect(networkContains('10.0.0.0/8', '10.0.0.0/8')).toBe(false);
  });
});

describe('isSingleHost', () => {
  it.each([
    '10.0.0.1',
    '10.0.0.1/32',
    '10.0.0.1/032',
    '::1',
    '2001:db8::1/128',
    '::ffff:1.2.3.4/128',
  ])('is true for %j', input => {
    expect(isSingleHost(input)).toBe(true);
  });

  it.each(['10.0.0.0/8', '2001:db8::/32', '10.0.0.1/31', '::ffff:1.2.3.4/120', 'nope', ''])(
    'is false for %j',
    input => {
      expect(isSingleHost(input)).toBe(false);
    }
  );
});

describe('trimming', () => {
  // Go's strings.TrimSpace trims U+0085 but not U+FEFF; JavaScript's trim does the opposite.
  it('trims the same whitespace as Go strings.TrimSpace', () => {
    expect(isValidNetworkAddress('\u008510.0.0.1\u0085', 'ip')).toBe(true);
    expect(isValidNetworkAddress('\uFEFF10.0.0.1', 'ip')).toBe(false);
    expect(isValidNetworkAddress('10.0.0.1\uFEFF', 'ip')).toBe(false);
    expect(networkAddressKey('\u008510.0.0.1')).toBe('10.0.0.1/32');
    expect(networkAddressKey('\uFEFF10.0.0.1')).toBeNull();
    expect(networkContains('\u008510.0.0.0/8', '\u008510.1.2.3')).toBe(true);
    expect(networkContains('\uFEFF10.0.0.0/8', '10.1.2.3')).toBe(false);
    expect(isSingleHost('\u008510.0.0.1')).toBe(true);
    expect(isSingleHost('\uFEFF10.0.0.1')).toBe(false);
  });

  it('keeps a long whitespace run inside the text and trims the ends', () => {
    const inner = 'a' + ' '.repeat(100_000) + 'x';
    expect(trimGoSpace(inner)).toBe(inner);
    expect(trimGoSpace(`\t \u3000${inner}\u0085\n`)).toBe(inner);
    expect(trimGoSpace(' \u00A0\u2003 ')).toBe('');
  });
});
