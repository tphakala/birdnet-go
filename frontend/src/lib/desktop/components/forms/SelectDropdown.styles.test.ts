import { describe, it, expect } from 'vitest';
import {
  OPTION_BASE_CLASS,
  OPTION_SELECTED_BG_CLASS,
  OPTION_HIGHLIGHT_BG_CLASS,
  OPTION_HIGHLIGHT_OUTLINE_CLASS,
  getOptionStateClasses,
} from './SelectDropdown.styles';

const COMBINATIONS = [
  { selected: false, highlighted: false },
  { selected: false, highlighted: true },
  { selected: true, highlighted: false },
  { selected: true, highlighted: true },
];

function tokens(classes: string): string[] {
  return classes.split(/\s+/).filter(Boolean);
}

describe('SelectDropdown option state classes', () => {
  it('never gives an option more than one plain background utility', () => {
    for (const state of COMBINATIONS) {
      const backgrounds = tokens(getOptionStateClasses(state)).filter(token =>
        token.startsWith('bg-')
      );
      // An idle unselected option has only the hover background, so it has none here
      const expected = state.selected || state.highlighted ? 1 : 0;
      expect({ state, count: backgrounds.length }).toEqual({ state, count: expected });
    }
  });

  it('outlines an option only while it is highlighted', () => {
    const outline = tokens(OPTION_HIGHLIGHT_OUTLINE_CLASS);
    for (const state of COMBINATIONS) {
      const result = tokens(getOptionStateClasses(state));
      for (const token of outline) {
        expect({ token, state, present: result.includes(token) }).toEqual({
          token,
          state,
          present: state.highlighted,
        });
      }
    }
  });

  it('keeps the selected tint when the selected option is also highlighted', () => {
    const result = tokens(getOptionStateClasses({ selected: true, highlighted: true }));
    expect(result).toContain(OPTION_SELECTED_BG_CLASS);
    expect(result).not.toContain(OPTION_HIGHLIGHT_BG_CLASS);
  });

  it('uses the primary color only in the selected background', () => {
    const all = [OPTION_BASE_CLASS, ...COMBINATIONS.map(getOptionStateClasses)].flatMap(tokens);
    const primaryTokens = all.filter(token => token.includes('--color-primary'));
    expect(primaryTokens.length).toBeGreaterThan(0);
    for (const token of primaryTokens) {
      expect(token).toBe(OPTION_SELECTED_BG_CLASS);
    }
  });
});
