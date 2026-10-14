import { describe, it, expect } from 'vitest';
import {
  parseIPv4,
  parseIPv6,
  isValidIP,
  isValidCIDR,
  validateNetworkAddress,
  networkAddressKey,
  isSingleHost,
  networkContains,
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

describe('validateNetworkAddress', () => {
  it('reports empty after trimming', () => {
    expect(validateNetworkAddress('   ', 'cidr')).toBe('empty');
    expect(validateNetworkAddress('', 'ip-or-cidr')).toBe('empty');
  });

  it('applies the mode', () => {
    expect(validateNetworkAddress('10.0.0.1', 'cidr')).toBe('invalid');
    expect(validateNetworkAddress('10.0.0.0/8', 'cidr')).toBeNull();
    expect(validateNetworkAddress('10.0.0.0/8', 'ip')).toBe('invalid');
    expect(validateNetworkAddress('10.0.0.1', 'ip')).toBeNull();
    expect(validateNetworkAddress('10.0.0.1', 'ip-or-cidr')).toBeNull();
    expect(validateNetworkAddress('10.0.0.0/8', 'ip-or-cidr')).toBeNull();
    expect(validateNetworkAddress('nope', 'ip-or-cidr')).toBe('invalid');
  });

  it('trims like the backend', () => {
    expect(validateNetworkAddress('  10.0.0.1 ', 'ip')).toBeNull();
  });

  it('accepts IPv6 where valid in each mode', () => {
    expect(validateNetworkAddress('2001:db8::1', 'ip')).toBeNull();
    expect(validateNetworkAddress('2001:db8::/32', 'cidr')).toBeNull();
    expect(validateNetworkAddress('2001:db8::1', 'cidr')).toBe('invalid');
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
