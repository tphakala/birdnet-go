import { describe, it, expect } from 'vitest';
import { activeOptionId, isOptionHighlighted, optionId } from './SelectDropdown.highlight';

describe('SelectDropdown highlight helpers', () => {
  describe('optionId', () => {
    it('builds the option id from the field id and the flat index', () => {
      expect(optionId('fruit', 0)).toBe('fruit-option-0');
      expect(optionId('fruit', 12)).toBe('fruit-option-12');
    });
  });

  describe('isOptionHighlighted', () => {
    it.each([
      { flatIndex: -1, highlightedIndex: -1, expected: false, why: 'no highlight never matches' },
      { flatIndex: 0, highlightedIndex: 0, expected: true, why: 'same index' },
      { flatIndex: 2, highlightedIndex: 1, expected: false, why: 'later option' },
      { flatIndex: 1, highlightedIndex: 2, expected: false, why: 'earlier option' },
      { flatIndex: -1, highlightedIndex: 0, expected: false, why: 'unmapped option' },
    ])('returns $expected for $flatIndex vs $highlightedIndex ($why)', c => {
      expect(isOptionHighlighted(c.flatIndex, c.highlightedIndex)).toBe(c.expected);
    });
  });

  describe('activeOptionId', () => {
    it.each([
      { highlightedIndex: -1, count: 3, expected: undefined, why: 'nothing highlighted' },
      { highlightedIndex: 0, count: 3, expected: 'fruit-option-0', why: 'first option' },
      { highlightedIndex: 2, count: 3, expected: 'fruit-option-2', why: 'last option' },
      { highlightedIndex: 3, count: 3, expected: undefined, why: 'one past the end' },
      { highlightedIndex: 0, count: 0, expected: undefined, why: 'no options rendered' },
    ])('returns $expected for index $highlightedIndex of $count ($why)', c => {
      expect(activeOptionId('fruit', c.highlightedIndex, c.count)).toBe(c.expected);
    });
  });
});
