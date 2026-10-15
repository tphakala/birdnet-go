import { describe, it, expect } from 'vitest';
import {
  isCloudflarePreset,
  hasInvalidTrustedProxy,
  partitionTrustedProxies,
  recombineTrustedProxies,
} from './trustedProxies';

describe('isCloudflarePreset', () => {
  it.each(['cloudflare', 'Cloudflare', 'CLOUDFLARE', '  cloudflare '])('accepts %j', value => {
    expect(isCloudflarePreset(value)).toBe(true);
  });

  it.each(['', 'cloudflare2', '10.0.0.1', 'cloud flare'])('rejects %j', value => {
    expect(isCloudflarePreset(value)).toBe(false);
  });

  it('trims the whitespace set the backend trims (U+0085 yes, U+FEFF no)', () => {
    expect(isCloudflarePreset('\u0085cloudflare\u0085')).toBe(true);
    expect(isCloudflarePreset('\uFEFFcloudflare')).toBe(false);
  });

  it('treats a U+0085-only entry as blank and a U+FEFF entry as a row, like the backend', () => {
    expect(partitionTrustedProxies(['\u0085', '\uFEFF', '10.0.0.1'])).toEqual({
      entries: ['\uFEFF', '10.0.0.1'],
      hasCloudflare: false,
    });
  });
});

describe('partitionTrustedProxies', () => {
  it('separates addresses from the preset token', () => {
    expect(partitionTrustedProxies(['10.0.0.1', 'Cloudflare', '10.0.0.2'])).toEqual({
      entries: ['10.0.0.1', '10.0.0.2'],
      hasCloudflare: true,
    });
  });

  it('reports no token for a plain list', () => {
    expect(partitionTrustedProxies(['10.0.0.1'])).toEqual({
      entries: ['10.0.0.1'],
      hasCloudflare: false,
    });
  });

  it('does not show blank entries as rows', () => {
    expect(partitionTrustedProxies(['', '10.0.0.1', '   '])).toEqual({
      entries: ['10.0.0.1'],
      hasCloudflare: false,
    });
  });

  it('keeps duplicate and invalid entries', () => {
    expect(partitionTrustedProxies(['10.0.0.1', '10.0.0.1', 'bad']).entries).toEqual([
      '10.0.0.1',
      '10.0.0.1',
      'bad',
    ]);
  });
});

const ROUND_TRIP_LISTS: string[][] = [
  [],
  ['10.0.0.1'],
  ['cloudflare', '10.0.0.1', '10.0.0.2'],
  ['10.0.0.1', 'Cloudflare', '10.0.0.2'],
  ['10.0.0.1', '10.0.0.2', 'CLOUDFLARE'],
  ['cloudflare', '10.0.0.1', 'Cloudflare'],
  ['', '10.0.0.1', '  ', 'cloudflare'],
  ['10.0.0.1', '10.0.0.1'],
];

describe('recombineTrustedProxies', () => {
  it.each(ROUND_TRIP_LISTS)('recombine(partition(x), x) equals x for %j', (...saved) => {
    const { entries, hasCloudflare } = partitionTrustedProxies(saved);
    expect(recombineTrustedProxies(entries, hasCloudflare, saved)).toEqual(saved);
  });

  it('returns a copy, not the saved array', () => {
    const saved = ['10.0.0.1', 'cloudflare'];
    const { entries, hasCloudflare } = partitionTrustedProxies(saved);
    expect(recombineTrustedProxies(entries, hasCloudflare, saved)).not.toBe(saved);
  });

  it('drops a preset token passed in the rows when the preset is off', () => {
    expect(recombineTrustedProxies(['10.0.0.1', 'cloudflare'], false, ['10.0.0.1'])).toEqual([
      '10.0.0.1',
    ]);
    expect(recombineTrustedProxies(['10.0.0.1', 'Cloudflare'], true, ['10.0.0.1'])).toEqual([
      '10.0.0.1',
      'cloudflare',
    ]);
  });

  it('ignores blank entries passed in the rows, so the saved list is returned unchanged', () => {
    // The saved list keeps its own blank entry, so only the identity return can produce it.
    expect(recombineTrustedProxies(['10.0.0.1', ' '], false, ['10.0.0.1', ''])).toEqual([
      '10.0.0.1',
      '',
    ]);
  });

  it('puts the token back at its saved index, in its saved spelling, after a real change', () => {
    const saved = ['10.0.0.1', 'CloudFlare', '10.0.0.2'];
    expect(recombineTrustedProxies(['10.0.0.1', '10.0.0.2', '10.0.0.3'], true, saved)).toEqual([
      '10.0.0.1',
      'CloudFlare',
      '10.0.0.2',
      '10.0.0.3',
    ]);
  });

  it('clamps the token index to the number of entries', () => {
    const saved = ['10.0.0.1', '10.0.0.2', 'cloudflare'];
    expect(recombineTrustedProxies(['10.0.0.9'], true, saved)).toEqual(['10.0.0.9', 'cloudflare']);
  });

  it('appends a plain token when the saved list had none', () => {
    expect(recombineTrustedProxies(['10.0.0.1'], true, ['10.0.0.1'])).toEqual([
      '10.0.0.1',
      'cloudflare',
    ]);
  });

  it('drops the token when the preset is off and entries changed', () => {
    expect(recombineTrustedProxies(['10.0.0.9'], false, ['10.0.0.1', 'cloudflare'])).toEqual([
      '10.0.0.9',
    ]);
  });

  it('drops extra duplicate tokens on a real change', () => {
    const saved = ['cloudflare', '10.0.0.1', 'Cloudflare'];
    expect(recombineTrustedProxies(['10.0.0.1', '10.0.0.2'], true, saved)).toEqual([
      'cloudflare',
      '10.0.0.1',
      '10.0.0.2',
    ]);
  });

  it('drops blanks on a real change', () => {
    expect(recombineTrustedProxies(['10.0.0.1', '10.0.0.2'], false, ['', '10.0.0.1'])).toEqual([
      '10.0.0.1',
      '10.0.0.2',
    ]);
  });
});

/** Apply a form edit the way the card does: through partition and recombine. */
function editEntries(list: string[], saved: string[], edit: (entries: string[]) => string[]) {
  const { entries, hasCloudflare } = partitionTrustedProxies(list);
  return recombineTrustedProxies(edit(entries), hasCloudflare, saved);
}

/** Flip the Cloudflare checkbox the way the Reverse Proxy card does. */
function toggleCloudflare(list: readonly string[], enabled: boolean, saved: readonly string[]) {
  return recombineTrustedProxies(partitionTrustedProxies(list).entries, enabled, saved);
}

describe('lossless round trip of trustedProxies (I6)', () => {
  it('add then remove an entry restores the saved list exactly', () => {
    const saved = ['10.0.0.1', 'Cloudflare', '10.0.0.2'];
    const added = editEntries(saved, saved, entries => [...entries, '10.0.0.3']);
    expect(added).toEqual(['10.0.0.1', 'Cloudflare', '10.0.0.2', '10.0.0.3']);
    const removed = editEntries(added, saved, entries => entries.slice(0, -1));
    expect(removed).toEqual(saved);
  });

  it('delete then re-add restores the saved order', () => {
    const saved = ['10.0.0.1', '10.0.0.2', 'cloudflare'];
    const deleted = editEntries(saved, saved, entries => entries.filter(e => e !== '10.0.0.2'));
    expect(deleted).toEqual(['10.0.0.1', 'cloudflare']);
    const readded = editEntries(deleted, saved, entries => [...entries, '10.0.0.2']);
    expect(readded).toEqual(saved);
  });

  it('Cloudflare off then on restores a saved token in its spelling and slot', () => {
    const saved = ['10.0.0.1', 'CLOUDFLARE', '10.0.0.2'];
    const off = toggleCloudflare(saved, false, saved);
    expect(off).toEqual(['10.0.0.1', '10.0.0.2']);
    expect(toggleCloudflare(off, true, saved)).toEqual(saved);
  });

  it('Cloudflare off then on restores two saved tokens exactly', () => {
    const saved = ['cloudflare', '10.0.0.1', 'Cloudflare'];
    const off = toggleCloudflare(saved, false, saved);
    expect(off).toEqual(['10.0.0.1']);
    expect(toggleCloudflare(off, true, saved)).toEqual(saved);
  });

  it('Cloudflare on then off on a list without a token restores the saved list', () => {
    const saved = ['10.0.0.1'];
    const on = toggleCloudflare(saved, true, saved);
    expect(on).toEqual(['10.0.0.1', 'cloudflare']);
    expect(toggleCloudflare(on, false, saved)).toEqual(saved);
  });

  it('Trust-button add then remove restores the saved list', () => {
    const saved = ['cloudflare', '10.0.0.1'];
    const trusted = editEntries(saved, saved, entries => [...entries, '172.18.0.1']);
    expect(trusted).toEqual(['cloudflare', '10.0.0.1', '172.18.0.1']);
    expect(editEntries(trusted, saved, entries => entries.slice(0, -1))).toEqual(saved);
  });

  it('keeps saved blank entries while nothing changes', () => {
    const saved = ['', '10.0.0.1'];
    expect(editEntries(saved, saved, entries => entries)).toEqual(saved);
  });
});

describe('hasInvalidTrustedProxy', () => {
  it('is false for valid rows, the preset token and blanks', () => {
    expect(hasInvalidTrustedProxy([])).toBe(false);
    expect(hasInvalidTrustedProxy(['10.0.0.1', '10.0.0.0/8', 'Cloudflare', '  '])).toBe(false);
  });

  it('is true when a row is neither an IP nor a CIDR', () => {
    expect(hasInvalidTrustedProxy(['10.0.0.1', 'proxy.example.com'])).toBe(true);
    expect(hasInvalidTrustedProxy(['cloudflare2'])).toBe(true);
  });
});
