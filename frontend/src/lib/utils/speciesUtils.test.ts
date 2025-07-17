import { describe, it, expect } from 'vitest';
import {
  parseSpeciesList,
  formatSpeciesName,
  parseSpeciesName,
  validateSpecies,
  filterSpeciesForAutocomplete,
  sortSpecies,
} from './speciesUtils';

describe('speciesUtils', () => {
  describe('parseSpeciesList', () => {
    it('parses comma-separated list', () => {
      const result = parseSpeciesList('Robin, Blue Jay, Cardinal');
      expect(result).toEqual(['Robin', 'Blue Jay', 'Cardinal']);
    });

    it('parses newline-separated list', () => {
      const result = parseSpeciesList('Robin\nBlue Jay\nCardinal');
      expect(result).toEqual(['Robin', 'Blue Jay', 'Cardinal']);
    });

    it('removes duplicates', () => {
      const result = parseSpeciesList('Robin, Blue Jay, Robin, Cardinal');
      expect(result).toEqual(['Robin', 'Blue Jay', 'Cardinal']);
    });

    it('trims whitespace', () => {
      const result = parseSpeciesList('  Robin  ,  Blue Jay  ');
      expect(result).toEqual(['Robin', 'Blue Jay']);
    });

    it('handles empty input', () => {
      expect(parseSpeciesList('')).toEqual([]);
      expect(parseSpeciesList(null as unknown as string)).toEqual([]);
      expect(parseSpeciesList(undefined as unknown as string)).toEqual([]);
    });
  });

  describe('formatSpeciesName', () => {
    it('formats Scientific_Common format', () => {
      const result = formatSpeciesName('Turdus_migratorius_American Robin');
      expect(result).toBe('American Robin (Turdus_migratorius)');
    });

    it('returns unchanged if no underscore', () => {
      const result = formatSpeciesName('American Robin');
      expect(result).toBe('American Robin');
    });

    it('handles empty input', () => {
      expect(formatSpeciesName('')).toBe('');
    });
  });

  describe('parseSpeciesName', () => {
    it('parses Scientific_Common format', () => {
      const result = parseSpeciesName('Turdus migratorius_American Robin');
      expect(result).toEqual({
        scientific: 'Turdus migratorius',
        common: 'American Robin',
      });
    });

    it('parses Common (Scientific) format', () => {
      const result = parseSpeciesName('American Robin (Turdus migratorius)');
      expect(result).toEqual({
        scientific: 'Turdus migratorius',
        common: 'American Robin',
      });
    });

    it('handles simple common name', () => {
      const result = parseSpeciesName('Robin');
      expect(result).toEqual({
        scientific: '',
        common: 'Robin',
      });
    });

    it('handles empty input', () => {
      const result = parseSpeciesName('');
      expect(result).toEqual({
        scientific: '',
        common: '',
      });
    });
  });

  describe('validateSpecies', () => {
    const allowedList = ['Robin', 'Blue Jay', 'Cardinal'];

    it('validates exact match', () => {
      expect(validateSpecies('Robin', allowedList)).toBe(true);
      expect(validateSpecies('Crow', allowedList)).toBe(false);
    });

    it('validates case-insensitive', () => {
      expect(validateSpecies('ROBIN', allowedList)).toBe(true);
      expect(validateSpecies('blue jay', allowedList)).toBe(true);
    });

    it('validates partial matches', () => {
      expect(validateSpecies('American Robin', ['Robin'])).toBe(true);
      expect(validateSpecies('Robin', ['American Robin'])).toBe(true);
    });

    it('returns true when no allowed list', () => {
      expect(validateSpecies('Any Bird', [])).toBe(true);
      expect(validateSpecies('Any Bird', null as unknown as string[])).toBe(true);
    });

    it('handles empty species', () => {
      expect(validateSpecies('', allowedList)).toBe(true);
    });
  });

  describe('filterSpeciesForAutocomplete', () => {
    const sourceList = ['American Robin', 'European Robin', 'Blue Jay', 'Cardinal', 'Crow'];

    it('filters by substring match', () => {
      const result = filterSpeciesForAutocomplete('rob', sourceList);
      expect(result).toEqual(['American Robin', 'European Robin']);
    });

    it('excludes items in exclude list', () => {
      const result = filterSpeciesForAutocomplete('rob', sourceList, ['American Robin']);
      expect(result).toEqual(['European Robin']);
    });

    it('sorts by relevance', () => {
      const list = ['Blue Robin', 'Robin', 'American Robin'];
      const result = filterSpeciesForAutocomplete('robin', list);
      expect(result[0]).toBe('Robin'); // Exact match first
    });

    it('respects max results', () => {
      const result = filterSpeciesForAutocomplete('r', sourceList, [], 2);
      expect(result.length).toBe(2);
    });

    it('handles empty input', () => {
      expect(filterSpeciesForAutocomplete('', sourceList)).toEqual([]);
      expect(filterSpeciesForAutocomplete('rob', [])).toEqual([]);
    });

    it('is case insensitive', () => {
      const result = filterSpeciesForAutocomplete('ROBIN', sourceList);
      expect(result.length).toBeGreaterThan(0);
    });
  });

  describe('sortSpecies', () => {
    it('sorts species alphabetically by common name', () => {
      const input = ['Cardinal', 'Blue Jay', 'American Robin'];
      const result = sortSpecies(input);
      expect(result).toEqual(['American Robin', 'Blue Jay', 'Cardinal']);
    });

    it('handles Scientific_Common format', () => {
      const input = [
        'Cardinalis_cardinalis_Cardinal',
        'Cyanocitta_cristata_Blue Jay',
        'Turdus_migratorius_American Robin',
      ];
      const result = sortSpecies(input);
      expect(result[0]).toContain('American Robin');
      expect(result[1]).toContain('Blue Jay');
      expect(result[2]).toContain('Cardinal');
    });

    it('handles empty array', () => {
      expect(sortSpecies([])).toEqual([]);
    });

    it('handles invalid input', () => {
      expect(sortSpecies(null as unknown as string[])).toEqual([]);
      expect(sortSpecies(undefined as unknown as string[])).toEqual([]);
    });

    it('does not mutate original array', () => {
      const input = ['Cardinal', 'Blue Jay'];
      const result = sortSpecies(input);
      expect(input).toEqual(['Cardinal', 'Blue Jay']);
      expect(result).toEqual(['Blue Jay', 'Cardinal']);
    });
  });
});
