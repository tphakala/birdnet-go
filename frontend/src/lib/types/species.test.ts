import { describe, it, expect } from 'vitest';
import {
  extractCanonicalSections,
  parseGuideDescription,
  classifyCanonicalHeading,
} from './species';

describe('classifyCanonicalHeading', () => {
  it('maps headings to canonical rows and ignores non-canonical ones', () => {
    expect(classifyCanonicalHeading('Description')).toBe('appearance');
    expect(classifyCanonicalHeading('Voice')).toBe('voice');
    expect(classifyCanonicalHeading('Distribution and habitat')).toBe('habitat');
    expect(classifyCanonicalHeading('Behaviour')).toBe('behaviour');
    // Localized (German) headings resolve to the same rows.
    expect(classifyCanonicalHeading('Stimme')).toBe('voice');
    expect(classifyCanonicalHeading('Verbreitung')).toBe('habitat');
    // Non-canonical headings and the article lead are unclassified.
    expect(classifyCanonicalHeading('Taxonomy')).toBeNull();
    expect(classifyCanonicalHeading('')).toBeNull();
  });
});

describe('extractCanonicalSections', () => {
  it('splits a well-structured guide into four distinct rows', () => {
    // The shape the backend now emits for a Common-Chaffinch-style article: Voice
    // is its own top-level "## Voice" section (promoted from a Wikipedia
    // sub-section) rather than being nested inside Description.
    const description = [
      'The common chaffinch is a small passerine.',
      '',
      '## Description',
      'The male has a blue-grey cap and rust underparts.',
      '',
      '## Voice',
      'The song is a descending series ending in a flourish.',
      '',
      '## Distribution and habitat',
      'Widespread across Europe in woodland.',
      '',
      '## Behaviour',
      'Forms flocks outside the breeding season.',
    ].join('\n');

    const sections = extractCanonicalSections(description);

    expect(sections.appearance).toContain('blue-grey cap');
    expect(sections.voice).toContain('flourish');
    expect(sections.habitat).toContain('woodland');
    expect(sections.behaviour).toContain('flocks');
    // The critical contrast: voice prose is NOT absorbed into the appearance row.
    expect(sections.appearance).not.toContain('flourish');
  });

  it('falls back to the article lead for appearance when there is no Description section', () => {
    const description = 'A large, heavy-billed corvid.\n\n## Voice\nA deep croaking gronk.';
    const sections = extractCanonicalSections(description);
    expect(sections.appearance).toBe('A large, heavy-billed corvid.');
    expect(sections.voice).toBe('A deep croaking gronk.');
    expect(sections.habitat).toBe('');
    expect(sections.behaviour).toBe('');
  });

  it('concatenates a same-category section instead of dropping it (no silent content loss)', () => {
    // Regression guard: when the backend promotes a canonical sub-section whose
    // category matches its parent (e.g. "Identification" nested under
    // "Description", both appearance), first-match-wins would drop its prose. The
    // extractor must append it to the row instead.
    const description = [
      '## Description',
      'Grey crown and rust underparts.',
      '',
      '## Identification',
      'Distinguished from congeners by its wing bars.',
      '',
      '## Distribution and habitat',
      'Temperate woodland.',
    ].join('\n');
    const sections = extractCanonicalSections(description);
    expect(sections.appearance).toContain('Grey crown');
    expect(sections.appearance).toContain('wing bars'); // previously dropped as a duplicate
    expect(sections.habitat).toContain('Temperate woodland.');
  });

  it('documents the pre-fix degradation: without a "## Voice" split the voice prose stays in appearance', () => {
    // This is the shape the OLD backend produced (Voice flattened to a bare line
    // inside Description). The extractor cannot recover it — which is exactly why
    // the split must happen backend-side in convertWikiSections.
    const description = [
      '## Description',
      'Greyish-blue crown.',
      'Voice',
      'A loud pink-pink call.',
    ].join('\n');

    const sections = extractCanonicalSections(description);
    expect(sections.voice).toBe('');
    expect(sections.appearance).toContain('A loud pink-pink call.');
  });
});

describe('parseGuideDescription', () => {
  it('treats leading text before the first header as the lead segment', () => {
    const sections = parseGuideDescription('Lead prose.\n\n## Voice\nSings.');
    expect(sections[0]).toEqual({ heading: '', body: 'Lead prose.' });
    expect(sections[1]).toEqual({ heading: 'Voice', body: 'Sings.' });
  });
});
