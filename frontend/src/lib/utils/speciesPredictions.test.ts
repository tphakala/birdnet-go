/**
 * Tests for the shared species-prediction helpers.
 *
 * These guard the core PR invariant: a prediction's `value` (what gets persisted)
 * is never the localized label, and label-aware filtering/matching never turns a
 * localized label into a persisted value.
 */

import { describe, it, expect } from 'vitest';
import {
  toLocalizedPredictions,
  filterLocalizedPredictions,
  matchTypedToCanonical,
  rankPredictions,
  type SpeciesPrediction,
} from './speciesPredictions';

/** A Finnish-locale label map for canonical (English/scientific) values. */
const FI_LABELS = new Map<string, string>([
  ['Barn Owl', 'Tornipöllö'],
  ['Tawny Owl', 'Lehtopöllö'],
  ['Tyto alba', 'Tornipöllö'],
]);

const localizeLabel = (value: string): string => FI_LABELS.get(value) ?? value;

describe('toLocalizedPredictions', () => {
  it('pairs each canonical value with its localized label', () => {
    const result = toLocalizedPredictions(['Barn Owl', 'Tawny Owl'], localizeLabel);
    expect(result).toMatchObject([
      { value: 'Barn Owl', label: 'Tornipöllö' },
      { value: 'Tawny Owl', label: 'Lehtopöllö' },
    ]);
  });

  it('pre-normalizes the value and label for fast filtering', () => {
    const [first] = toLocalizedPredictions(['Barn Owl'], localizeLabel);
    expect(first.normalizedValue).toBe('barn owl');
    expect(first.normalizedLabel).toBe('tornipöllö');
  });

  it('falls back to the value when no localizer is provided', () => {
    const result = toLocalizedPredictions(['Barn Owl']);
    expect(result).toMatchObject([{ value: 'Barn Owl', label: 'Barn Owl' }]);
  });

  it('falls back to the value for entries the localizer leaves unchanged', () => {
    // A taxonomy group row that the dictionary cannot localize.
    const result = toLocalizedPredictions(['Owls (Family)'], localizeLabel);
    expect(result).toMatchObject([{ value: 'Owls (Family)', label: 'Owls (Family)' }]);
  });

  it('falls back to the value when the localizer returns an empty string', () => {
    const result = toLocalizedPredictions(['Barn Owl', 'Tawny Owl'], () => '');
    expect(result).toMatchObject([
      { value: 'Barn Owl', label: 'Barn Owl' },
      { value: 'Tawny Owl', label: 'Tawny Owl' },
    ]);
  });

  it('never lets the label leak into the value', () => {
    const result = toLocalizedPredictions(['Barn Owl'], localizeLabel);
    expect(result[0].value).toBe('Barn Owl');
    expect(result[0].label).toBe('Tornipöllö');
  });
});

describe('filterLocalizedPredictions', () => {
  const predictions: SpeciesPrediction[] = [
    { value: 'Barn Owl', label: 'Tornipöllö' },
    { value: 'Tawny Owl', label: 'Lehtopöllö' },
    { value: 'Common Blackbird', label: 'Mustarastas' },
  ];

  it('matches the localized label', () => {
    const result = filterLocalizedPredictions(predictions, 'torni');
    expect(result).toEqual([{ value: 'Barn Owl', label: 'Tornipöllö' }]);
  });

  it('matches the canonical value too', () => {
    const result = filterLocalizedPredictions(predictions, 'blackbird');
    expect(result).toEqual([{ value: 'Common Blackbird', label: 'Mustarastas' }]);
  });

  it('is case- and NFC-insensitive', () => {
    expect(filterLocalizedPredictions(predictions, 'LEHTO')).toHaveLength(1);
  });

  it('returns all predictions for an empty query', () => {
    expect(filterLocalizedPredictions(predictions, '')).toHaveLength(3);
  });

  it('honors max', () => {
    expect(filterLocalizedPredictions(predictions, '', { max: 2 })).toHaveLength(2);
  });

  it('returns nothing for a zero (or negative) max', () => {
    expect(filterLocalizedPredictions(predictions, '', { max: 0 })).toEqual([]);
    expect(filterLocalizedPredictions(predictions, '', { max: -3 })).toEqual([]);
  });

  it('trims the query before matching', () => {
    expect(filterLocalizedPredictions(predictions, '  torni  ')).toEqual([
      { value: 'Barn Owl', label: 'Tornipöllö' },
    ]);
  });

  it('excludes the given canonical value', () => {
    const result = filterLocalizedPredictions(predictions, '', { excludeValue: 'Barn Owl' });
    expect(result.map(p => p.value)).not.toContain('Barn Owl');
    expect(result).toHaveLength(2);
  });

  it('excludes by localized label too (no self-echo of a fully-typed name)', () => {
    const result = filterLocalizedPredictions(predictions, '', { excludeValue: 'Tornipöllö' });
    expect(result.map(p => p.value)).not.toContain('Barn Owl');
    expect(result).toHaveLength(2);
  });
});

describe('rankPredictions', () => {
  // Mirrors the real bug: many global "...peippo" / "...tikka" look-alikes plus the
  // native species the visitor actually wants. Labels are Finnish (the visitor
  // locale); values are the canonical scientific names.
  const predictions: SpeciesPrediction[] = [
    { value: 'Chloris chloris', label: 'viherpeippo' },
    { value: 'Fringilla montifringilla', label: 'järripeippo' },
    { value: 'Amadina fasciata', label: 'viiltopeippo' },
    { value: 'Fringilla coelebs', label: 'peippo' },
    { value: 'Dendrocopos major', label: 'käpytikka' },
    { value: 'Dendrocopos leucotos', label: 'valkoselkätikka' },
    { value: 'Blythipicus pyrrhotis', label: 'tuliniskatikka' },
    { value: 'Camarhynchus pallidus', label: 'tikkasirkku' },
  ];
  // The species probable at the configured (Finnish) location.
  const localValues = new Set([
    'Fringilla coelebs',
    'Chloris chloris',
    'Fringilla montifringilla',
    'Dendrocopos major',
    'Dendrocopos leucotos',
  ]);
  const isLocal = (p: SpeciesPrediction): boolean => localValues.has(p.value);

  it('ranks an exact label match first even when look-alikes sort earlier alphabetically', () => {
    // The exact "peippo" (Fringilla coelebs) is buried mid-list; it must lead.
    const result = rankPredictions(predictions, 'peippo', { isLocal });
    expect(result[0].label).toBe('peippo');
  });

  it('surfaces native (local) contains-matches above a non-native prefix match', () => {
    // "tikka" is a compound-word suffix: native woodpeckers (käpytikka,
    // valkoselkätikka) merely contain it, while the non-native tikkasirkku starts
    // with it. Locality must outrank prefix so the natives lead.
    const labels = rankPredictions(predictions, 'tikka', { isLocal }).map(p => p.label);
    expect(labels[0]).toBe('käpytikka'); // alphabetically first local contains-match
    expect(labels.indexOf('käpytikka')).toBeLessThan(labels.indexOf('tikkasirkku'));
    expect(labels.indexOf('valkoselkätikka')).toBeLessThan(labels.indexOf('tikkasirkku'));
    // Non-local entries (prefix or contains) trail the local ones.
    expect(labels.indexOf('tikkasirkku')).toBeLessThan(labels.indexOf('tuliniskatikka'));
  });

  it('ranks a local prefix match above a local contains match', () => {
    // "peippo" starts the local label "peippo" (exact, tier 0) but is a prefix of
    // nothing else here; use "peip" so the exact drops out and only prefix/contains
    // remain. peippo (local prefix) must precede viherpeippo (local contains).
    const labels = rankPredictions(predictions, 'peip', { isLocal }).map(p => p.label);
    expect(labels.indexOf('peippo')).toBeLessThan(labels.indexOf('viherpeippo'));
    expect(labels.indexOf('peippo')).toBeLessThan(labels.indexOf('järripeippo'));
  });

  it('matches the canonical value, not only the label', () => {
    const result = rankPredictions(predictions, 'coelebs', { isLocal });
    expect(result.map(p => p.value)).toEqual(['Fringilla coelebs']);
  });

  it('drops non-matching predictions', () => {
    const result = rankPredictions(predictions, 'peippo', { isLocal });
    expect(result.every(p => p.label.includes('peippo'))).toBe(true);
  });

  it('caps the result to the limit while keeping the best-ranked entries', () => {
    const result = rankPredictions(predictions, 'peippo', { isLocal, limit: 2 });
    expect(result).toHaveLength(2);
    // The exact match must survive the cap rather than being sliced off.
    expect(result[0].label).toBe('peippo');
  });

  it('returns nothing for an empty (or whitespace-only) query', () => {
    expect(rankPredictions(predictions, '   ', { isLocal })).toEqual([]);
  });

  it('returns nothing for a zero or negative limit', () => {
    expect(rankPredictions(predictions, 'peippo', { isLocal, limit: 0 })).toEqual([]);
    expect(rankPredictions(predictions, 'peippo', { isLocal, limit: -1 })).toEqual([]);
  });

  it('sorts alphabetically by label within a tier when no locality info is given', () => {
    const result = rankPredictions(predictions, 'peippo');
    expect(result[0].label).toBe('peippo'); // exact still leads
    const rest = result.slice(1).map(p => p.label);
    expect(rest).toEqual([...rest].sort((a, b) => a.localeCompare(b)));
  });
});

describe('matchTypedToCanonical', () => {
  const predictions: SpeciesPrediction[] = [
    { value: 'Barn Owl', label: 'Tornipöllö' },
    { value: 'Tawny Owl', label: 'Lehtopöllö' },
  ];

  it('maps a typed localized label to the canonical value (the corruption guard)', () => {
    expect(matchTypedToCanonical('Tornipöllö', predictions)).toBe('Barn Owl');
  });

  it('maps a typed canonical value to itself', () => {
    expect(matchTypedToCanonical('Tawny Owl', predictions)).toBe('Tawny Owl');
  });

  it('is case- and NFC-insensitive', () => {
    expect(matchTypedToCanonical('tornipöllö', predictions)).toBe('Barn Owl');
  });

  it('trims surrounding whitespace before matching', () => {
    expect(matchTypedToCanonical('  Tornipöllö  ', predictions)).toBe('Barn Owl');
  });

  it('returns undefined when nothing matches (caller keeps the typed text)', () => {
    expect(matchTypedToCanonical('Unlisted Bird', predictions)).toBeUndefined();
  });

  it('returns undefined for empty input', () => {
    expect(matchTypedToCanonical('', predictions)).toBeUndefined();
  });
});
