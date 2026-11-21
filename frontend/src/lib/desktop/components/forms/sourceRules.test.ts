import { describe, it, expect } from 'vitest';
import type { AudioSourceConfig } from '$lib/stores/settings';
import { soundCardProblems } from './sourceRules';

function source(name: string, device: string): AudioSourceConfig {
  return { name, device, gain: 0, models: [] };
}

const SAVED = [source('Garden', 'hw:0'), source('Porch', 'hw:1')];

describe('soundCardProblems', () => {
  it('reports nothing for a valid list', () => {
    expect(soundCardProblems(SAVED, SAVED)).toEqual([{}, {}]);
  });

  it.each([
    ['empty', ''],
    ['blank', '   '],
  ])('reports a %s name as required', (_label, name) => {
    const problems = soundCardProblems([source(name, 'hw:0'), SAVED[1]], SAVED);
    expect(problems).toEqual([{ name: 'required' }, {}]);
  });

  it('reports an empty device as required', () => {
    const problems = soundCardProblems([source('Garden', ' '), SAVED[1]], SAVED);
    expect(problems).toEqual([{ device: 'required' }, {}]);
  });

  it('reports a name that now matches another source, ignoring case and spaces, on both rows', () => {
    const problems = soundCardProblems(
      [source('Garden', 'hw:0'), source(' garden ', 'hw:1')],
      SAVED
    );
    expect(problems).toEqual([{ name: 'duplicate' }, { name: 'duplicate' }]);
  });

  it('reports a device that now matches another source on both rows', () => {
    const problems = soundCardProblems([source('Garden', 'hw:0'), source('Porch', 'hw:0')], SAVED);
    expect(problems).toEqual([{ device: 'duplicate' }, { device: 'duplicate' }]);
  });

  it('does not report a duplicate the saved list already holds', () => {
    const saved = [source('Mic', 'hw:0'), source('mic', 'hw:1')];
    expect(soundCardProblems(saved, saved)).toEqual([{}, {}]);
  });

  it('reports the duplicate once it occurs more often than in the saved list', () => {
    const saved = [source('Mic', 'hw:0'), source('mic', 'hw:1')];
    const grown = [...saved, source('MIC', 'hw:2')];
    expect(soundCardProblems(grown, saved)).toEqual([
      { name: 'duplicate' },
      { name: 'duplicate' },
      { name: 'duplicate' },
    ]);
  });

  it('still reports an empty name when the saved list holds one, and keeps the other rows clean', () => {
    const saved = [source('', 'hw:0'), source('Porch', 'hw:1')];
    expect(soundCardProblems(saved, saved)).toEqual([{ name: 'required' }, {}]);
  });

  it('keeps the result independent of row order', () => {
    const edited = [source('Porch', 'hw:1'), source('Garden', 'hw:0'), source('garden', 'hw:2')];
    expect(soundCardProblems(edited, SAVED)).toEqual([
      {},
      { name: 'duplicate' },
      { name: 'duplicate' },
    ]);
  });
});
