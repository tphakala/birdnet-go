import { describe, expect, it } from 'vitest';
import {
  getAllAboutBirdsSoundsUrl,
  getAllAboutBirdsUrl,
  getWikipediaUrl,
  hasSpeciesReferenceName,
} from './speciesLinks';

describe('getAllAboutBirdsUrl', () => {
  it('builds the guide URL from the server-provided common name', () => {
    expect(getAllAboutBirdsUrl("Wilson's Warbler")).toBe(
      'https://www.allaboutbirds.org/guide/Wilsons_Warbler/id'
    );
  });

  it('encodes names with punctuation that is not safe in a URL path', () => {
    expect(getAllAboutBirdsUrl('Black-throated Blue Warbler')).toBe(
      'https://www.allaboutbirds.org/guide/Black-throated_Blue_Warbler/id'
    );
  });
});

describe('getAllAboutBirdsSoundsUrl', () => {
  it('builds the sounds page URL from the server-provided common name', () => {
    expect(getAllAboutBirdsSoundsUrl("Wilson's Warbler")).toBe(
      'https://www.allaboutbirds.org/guide/Wilsons_Warbler/sounds'
    );
  });

  it('encodes names with punctuation that is not safe in a URL path', () => {
    expect(getAllAboutBirdsSoundsUrl('Black-throated Blue Warbler')).toBe(
      'https://www.allaboutbirds.org/guide/Black-throated_Blue_Warbler/sounds'
    );
  });
});

describe('getWikipediaUrl', () => {
  it('uses the selected English Wikipedia article', () => {
    expect(getWikipediaUrl('Tufted Titmouse', 'en')).toBe(
      'https://en.wikipedia.org/wiki/Tufted_Titmouse'
    );
  });

  it('uses the localized Wikipedia host for the selected UI language', () => {
    expect(getWikipediaUrl('Haubenmeise', 'de', 'Tufted Titmouse')).toBe(
      'https://de.wikipedia.org/wiki/Haubenmeise'
    );
  });

  it("maps Norwegian Bokmal to Wikipedia's no domain", () => {
    expect(getWikipediaUrl('Toppmeis', 'nb', 'Great Tit')).toBe(
      'https://no.wikipedia.org/wiki/Toppmeis'
    );
  });

  it('uses English Wikipedia when the display name is the English fallback', () => {
    expect(getWikipediaUrl('House Sparrow', 'de', 'House Sparrow')).toBe(
      'https://en.wikipedia.org/wiki/House_Sparrow'
    );
  });
});

describe('hasSpeciesReferenceName', () => {
  it('accepts a real common name', () => {
    expect(hasSpeciesReferenceName('House Sparrow')).toBe(true);
  });

  it.each([
    ['an empty string', ''],
    ['whitespace only', '   '],
    ['a tab', '\t'],
  ])('rejects %s, which would build a guide URL for a page that does not exist', (_label, name) => {
    expect(hasSpeciesReferenceName(name)).toBe(false);
  });

  it.each([
    ['undefined', undefined],
    ['null', null],
  ])('rejects %s', (_label, name) => {
    expect(hasSpeciesReferenceName(name)).toBe(false);
  });
});
