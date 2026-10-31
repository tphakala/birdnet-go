import { describe, it, expect } from 'vitest';
import { parseLifeListCsv } from './lifeListCsv';

describe('parseLifeListCsv', () => {
  it('parses a well-formed eBird export', () => {
    const csv = [
      'Common Name,Scientific Name,Count,Location',
      'Common Blackbird,Turdus merula,1,"Backyard, Home"',
      'Great Tit,Parus major,3,Backyard',
    ].join('\n');

    const result = parseLifeListCsv(csv);

    expect(result.accepted).toEqual(['Turdus merula_Common Blackbird', 'Parus major_Great Tit']);
    expect(result.rejected).toEqual([]);
  });

  it('is resilient to column reordering', () => {
    const csv = [
      'Location,Scientific Name,Count,Common Name',
      'Backyard,Turdus merula,1,Common Blackbird',
    ].join('\n');

    const result = parseLifeListCsv(csv);

    expect(result.accepted).toEqual(['Turdus merula_Common Blackbird']);
  });

  it('rejects rows with an empty scientific name', () => {
    const csv = [
      'Common Name,Scientific Name',
      'Common Blackbird,Turdus merula',
      'Mystery Bird,',
    ].join('\n');

    const result = parseLifeListCsv(csv);

    expect(result.accepted).toEqual(['Turdus merula_Common Blackbird']);
    expect(result.rejected).toHaveLength(1);
    expect(result.rejected[0].reason).toMatch(/empty scientific name/i);
    expect(result.rejected[0].rowNumber).toBe(3);
  });

  it('identifies "spuh" placeholders and explains why BirdNET can never match them', () => {
    const csv = ['Common Name,Scientific Name', 'tern sp.,Sterninae sp.'].join('\n');

    const result = parseLifeListCsv(csv);

    expect(result.accepted).toEqual([]);
    expect(result.rejected).toHaveLength(1);
    expect(result.rejected[0].reason).toContain('"Sterninae sp." ("tern sp.")');
    expect(result.rejected[0].reason).toContain('spuh');
    expect(result.rejected[0].reason).toContain('BirdNET always reports a specific species');
  });

  it('identifies eBird "slash" species-pair entries and explains why BirdNET can never match them', () => {
    const csv = [
      'Common Name,Scientific Name',
      'Yellow/Mangrove Warbler,Setophaga aestiva/petechia',
    ].join('\n');

    const result = parseLifeListCsv(csv);

    expect(result.rejected).toHaveLength(1);
    expect(result.rejected[0].reason).toContain(
      '"Setophaga aestiva/petechia" ("Yellow/Mangrove Warbler")'
    );
    expect(result.rejected[0].reason).toContain('slash');
    expect(result.rejected[0].reason).toContain('one specific species per');
  });

  it('identifies hybrid entries and explains why BirdNET can never match them', () => {
    const csv = [
      'Common Name,Scientific Name',
      'Mallard x American Black Duck,Anas platyrhynchos x rubripes',
    ].join('\n');

    const result = parseLifeListCsv(csv);

    expect(result.rejected).toHaveLength(1);
    expect(result.rejected[0].reason).toContain('hybrid');
  });

  it('falls back to a generic reason for unrecognized garbage rows', () => {
    const csv = [
      'Common Name,Scientific Name',
      'Garbage Row,not a species name at all',
      'Common Blackbird,Turdus merula',
    ].join('\n');

    const result = parseLifeListCsv(csv);

    expect(result.accepted).toEqual(['Turdus merula_Common Blackbird']);
    expect(result.rejected).toHaveLength(1);
    expect(result.rejected[0].rowNumber).toBe(2);
    expect(result.rejected[0].reason).toContain(
      'does not look like a single scientific species name'
    );
  });

  it('includes the common name in the empty-scientific-name rejection reason', () => {
    const csv = ['Common Name,Scientific Name', 'Mystery Bird,'].join('\n');

    const result = parseLifeListCsv(csv);

    expect(result.rejected).toHaveLength(1);
    expect(result.rejected[0].reason).toBe('Empty scientific name (common name: "Mystery Bird")');
  });

  it('silently deduplicates repeated species (e.g. multiple sightings)', () => {
    const csv = [
      'Common Name,Scientific Name',
      'Common Blackbird,Turdus merula',
      'Common Blackbird,Turdus merula',
      'common blackbird,turdus merula',
    ].join('\n');

    const result = parseLifeListCsv(csv);

    expect(result.accepted).toHaveLength(1);
    expect(result.rejected).toEqual([]);
  });

  it('handles quoted fields containing commas', () => {
    const csv = [
      'Common Name,Scientific Name,Location',
      'Common Blackbird,Turdus merula,"Springfield, IL, USA"',
    ].join('\n');

    const result = parseLifeListCsv(csv);

    expect(result.accepted).toEqual(['Turdus merula_Common Blackbird']);
  });

  it('falls back to scientific-name-only entries when no Common Name column exists', () => {
    const csv = ['Scientific Name', 'Turdus merula'].join('\n');

    const result = parseLifeListCsv(csv);

    expect(result.accepted).toEqual(['Turdus merula']);
  });

  it('returns empty results for an empty file', () => {
    const result = parseLifeListCsv('');
    expect(result.accepted).toEqual([]);
    expect(result.rejected).toEqual([]);
  });

  it('rejects the whole file when no Scientific Name column is found', () => {
    const csv = ['Common Name,Count', 'Common Blackbird,1'].join('\n');

    const result = parseLifeListCsv(csv);

    expect(result.accepted).toEqual([]);
    expect(result.rejected).toHaveLength(1);
    expect(result.rejected[0].reason).toMatch(/No "Scientific Name" column/);
  });

  it('handles Windows-style CRLF line endings', () => {
    const csv = 'Common Name,Scientific Name\r\nCommon Blackbird,Turdus merula\r\n';

    const result = parseLifeListCsv(csv);

    expect(result.accepted).toEqual(['Turdus merula_Common Blackbird']);
  });
});
