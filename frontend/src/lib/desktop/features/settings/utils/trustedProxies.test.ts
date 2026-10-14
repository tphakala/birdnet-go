import { describe, it, expect } from 'vitest';
import {
  isCloudflarePreset,
  partitionTrustedProxies,
  recombineTrustedProxies,
  setCloudflarePreset,
  splitSubnetList,
  joinSubnetList,
} from './trustedProxies';

describe('isCloudflarePreset', () => {
  it.each(['cloudflare', 'Cloudflare', 'CLOUDFLARE', '  cloudflare '])('accepts %j', value => {
    expect(isCloudflarePreset(value)).toBe(true);
  });

  it.each(['', 'cloudflare2', '10.0.0.1', 'cloud flare'])('rejects %j', value => {
    expect(isCloudflarePreset(value)).toBe(false);
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
    const off = setCloudflarePreset(saved, false, saved);
    expect(off).toEqual(['10.0.0.1', '10.0.0.2']);
    expect(setCloudflarePreset(off, true, saved)).toEqual(saved);
  });

  it('Cloudflare off then on restores two saved tokens exactly', () => {
    const saved = ['cloudflare', '10.0.0.1', 'Cloudflare'];
    const off = setCloudflarePreset(saved, false, saved);
    expect(off).toEqual(['10.0.0.1']);
    expect(setCloudflarePreset(off, true, saved)).toEqual(saved);
  });

  it('Cloudflare on then off on a list without a token restores the saved list', () => {
    const saved = ['10.0.0.1'];
    const on = setCloudflarePreset(saved, true, saved);
    expect(on).toEqual(['10.0.0.1', 'cloudflare']);
    expect(setCloudflarePreset(on, false, saved)).toEqual(saved);
  });

  it('toggling on twice adds one token', () => {
    const saved = ['10.0.0.1'];
    const once = setCloudflarePreset(saved, true, saved);
    expect(setCloudflarePreset(once, true, saved)).toEqual(once);
  });

  it('toggling on keeps an existing token where it is', () => {
    const saved = ['10.0.0.1'];
    const list = ['cloudflare', '10.0.0.1', '10.0.0.5'];
    expect(setCloudflarePreset(list, true, saved)).toEqual(list);
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

describe('splitSubnetList', () => {
  it('splits on commas, trims and drops blanks', () => {
    expect(splitSubnetList('10.0.0.0/8, 192.168.0.0/16 ,, ')).toEqual([
      '10.0.0.0/8',
      '192.168.0.0/16',
    ]);
  });

  it('returns an empty list for empty and blank text', () => {
    expect(splitSubnetList('')).toEqual([]);
    expect(splitSubnetList('  , ')).toEqual([]);
  });
});

describe('joinSubnetList', () => {
  it.each(['10.0.0.0/8, 192.168.0.0/16', '10.0.0.0/8,192.168.0.0/16, '])(
    'restores the saved string byte for byte after add then remove (saved %j)',
    saved => {
      const values = splitSubnetList(saved);
      const added = joinSubnetList([...values, '172.16.0.0/12'], saved);
      expect(added).toBe('10.0.0.0/8, 192.168.0.0/16, 172.16.0.0/12');
      expect(joinSubnetList(splitSubnetList(added).slice(0, -1), saved)).toBe(saved);
    }
  );

  it('joins a real change with ", " so the backend split reads it back', () => {
    const joined = joinSubnetList(['10.0.0.0/8', '172.16.0.0/12'], '10.0.0.0/8');
    expect(joined).toBe('10.0.0.0/8, 172.16.0.0/12');
    expect(splitSubnetList(joined)).toEqual(['10.0.0.0/8', '172.16.0.0/12']);
  });

  it('yields an empty string for no values', () => {
    expect(joinSubnetList([], '10.0.0.0/8')).toBe('');
  });
});
