import { describe, it, expect, beforeEach, vi } from 'vitest';
import { get } from 'svelte/store';
import {
  settingsValidationErrors,
  settingsValidationReasons,
  setValidationIssues,
} from './settings';

const OWNED = ['mine-a', 'mine-b'];

describe('setValidationIssues', () => {
  beforeEach(() => {
    settingsValidationErrors.set([]);
    settingsValidationReasons.set(new Map());
  });

  it('adds the active entries and their reasons, keeping foreign entries', () => {
    settingsValidationErrors.set(['other']);
    settingsValidationReasons.set(new Map([['other', 'Other reason']]));

    setValidationIssues(OWNED, new Map([['mine-a', 'A is wrong']]));

    expect(get(settingsValidationErrors)).toEqual(['other', 'mine-a']);
    expect(get(settingsValidationReasons)).toEqual(
      new Map([
        ['other', 'Other reason'],
        ['mine-a', 'A is wrong'],
      ])
    );
  });

  it('ignores active keys that are not owned', () => {
    settingsValidationErrors.set(['other']);
    settingsValidationReasons.set(new Map([['other', 'Other reason']]));

    setValidationIssues(
      OWNED,
      new Map([
        ['other', 'Changed by a page that does not own it'],
        ['stranger', 'Not owned either'],
        ['mine-a', 'A is wrong'],
      ])
    );

    expect(get(settingsValidationErrors)).toEqual(['other', 'mine-a']);
    expect(get(settingsValidationReasons)).toEqual(
      new Map([
        ['other', 'Other reason'],
        ['mine-a', 'A is wrong'],
      ])
    );
  });

  it('removes owned entries that are no longer active', () => {
    setValidationIssues(
      OWNED,
      new Map([
        ['mine-a', 'A'],
        ['mine-b', 'B'],
      ])
    );
    setValidationIssues(OWNED, new Map([['mine-b', 'B']]));

    expect(get(settingsValidationErrors)).toEqual(['mine-b']);
    expect(get(settingsValidationReasons)).toEqual(new Map([['mine-b', 'B']]));
  });

  it('clears every owned entry for an empty map', () => {
    setValidationIssues(OWNED, new Map([['mine-a', 'A']]));
    setValidationIssues(OWNED, new Map());

    expect(get(settingsValidationErrors)).toEqual([]);
    expect(get(settingsValidationReasons).size).toBe(0);
  });

  it('writes neither store when nothing changed', () => {
    const active = new Map([['mine-a', 'A']]);
    setValidationIssues(OWNED, active);

    const onErrors = vi.fn();
    const onReasons = vi.fn();
    const stopErrors = settingsValidationErrors.subscribe(onErrors);
    const stopReasons = settingsValidationReasons.subscribe(onReasons);
    onErrors.mockClear();
    onReasons.mockClear();

    setValidationIssues(OWNED, active);

    expect(onErrors).not.toHaveBeenCalled();
    expect(onReasons).not.toHaveBeenCalled();
    stopErrors();
    stopReasons();
  });

  it('rewrites the reasons when only the text changed', () => {
    setValidationIssues(OWNED, new Map([['mine-a', 'old']]));
    setValidationIssues(OWNED, new Map([['mine-a', 'new']]));

    expect(get(settingsValidationReasons).get('mine-a')).toBe('new');
    expect(get(settingsValidationErrors)).toEqual(['mine-a']);
  });
});
