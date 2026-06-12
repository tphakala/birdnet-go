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
    expect(result).toEqual([
      { value: 'Barn Owl', label: 'Tornipöllö' },
      { value: 'Tawny Owl', label: 'Lehtopöllö' },
    ]);
  });

  it('falls back to the value when no localizer is provided', () => {
    const result = toLocalizedPredictions(['Barn Owl']);
    expect(result).toEqual([{ value: 'Barn Owl', label: 'Barn Owl' }]);
  });

  it('falls back to the value for entries the localizer leaves unchanged', () => {
    // A taxonomy group row that the dictionary cannot localize.
    const result = toLocalizedPredictions(['Owls (Family)'], localizeLabel);
    expect(result).toEqual([{ value: 'Owls (Family)', label: 'Owls (Family)' }]);
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

  it('excludes the given canonical value', () => {
    const result = filterLocalizedPredictions(predictions, '', { excludeValue: 'Barn Owl' });
    expect(result.map(p => p.value)).not.toContain('Barn Owl');
    expect(result).toHaveLength(2);
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

  it('returns undefined when nothing matches (caller keeps the typed text)', () => {
    expect(matchTypedToCanonical('Unlisted Bird', predictions)).toBeUndefined();
  });

  it('returns undefined for empty input', () => {
    expect(matchTypedToCanonical('', predictions)).toBeUndefined();
  });
});
