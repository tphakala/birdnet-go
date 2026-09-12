import { describe, expect, it } from 'vitest';
import { compareActiveSpeciesForExport, resolveActiveSpeciesScores } from './speciesScores';

describe('resolveActiveSpeciesScores', () => {
  it('displays the native range score without changing the exported API score', () => {
    expect(resolveActiveSpeciesScores(1, 0.95)).toEqual({
      displayScore: 0.95,
      exportScore: 1,
    });
  });

  it('uses the API score for both consumers when no range score is present', () => {
    expect(resolveActiveSpeciesScores(0.73)).toEqual({
      displayScore: 0.73,
      exportScore: 0.73,
    });
  });

  it('keeps CSV rows in raw-score order with stable API-order ties', () => {
    const rows = [
      { id: 'native-first', exportScore: 0.99, exportOrder: 1 },
      { id: 'override-second', exportScore: 1, exportOrder: 2 },
      { id: 'override-first', exportScore: 1, exportOrder: 0 },
    ];

    expect(rows.sort(compareActiveSpeciesForExport).map(row => row.id)).toEqual([
      'override-first',
      'override-second',
      'native-first',
    ]);
  });
});
