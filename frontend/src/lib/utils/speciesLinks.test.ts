import { describe, expect, it } from 'vitest';
import { getAllAboutBirdsUrl, getWikipediaUrl } from './speciesLinks';

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

describe('getWikipediaUrl', () => {
  it('uses the selected English Wikipedia article', () => {
    expect(getWikipediaUrl('Tufted Titmouse', 'en')).toBe(
      'https://en.wikipedia.org/wiki/Tufted_Titmouse'
    );
  });

  it('uses the localized Wikipedia host for the selected UI language', () => {
    expect(getWikipediaUrl('Haubenmeise', 'de')).toBe('https://de.wikipedia.org/wiki/Haubenmeise');
  });

  it("maps Norwegian Bokmal to Wikipedia's no domain", () => {
    expect(getWikipediaUrl('Toppmeis', 'nb')).toBe('https://no.wikipedia.org/wiki/Toppmeis');
  });
});
