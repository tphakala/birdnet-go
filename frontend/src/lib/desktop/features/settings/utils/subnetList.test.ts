import { describe, it, expect } from 'vitest';
import { splitSubnetList, joinSubnetList } from './subnetList';

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

  it('trims the whitespace set the backend trims (U+0085 yes, U+FEFF no)', () => {
    expect(splitSubnetList('\u008510.0.0.0/8\u0085,\uFEFF')).toEqual(['10.0.0.0/8', '\uFEFF']);
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
