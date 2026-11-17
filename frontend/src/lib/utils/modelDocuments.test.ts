import { describe, it, expect } from 'vitest';
import { refusalDocuments, splitEntryDocuments } from './modelDocuments';
import type { CatalogDocument } from '$lib/types/models';
import { catalogEntry } from '../../test/fixtures/catalog';

const SHA_A = 'a'.repeat(64);
const SHA_B = 'b'.repeat(64);

function doc(overrides: Partial<CatalogDocument>): CatalogDocument {
  return {
    repo: 'org/model',
    kind: 'acceptable_use',
    requiresAgreement: true,
    agreed: false,
    sha256: SHA_A,
    ...overrides,
  };
}

describe('splitEntryDocuments', () => {
  it('asks only for hosted documents that need an agreement not yet recorded', () => {
    const policy = doc({});
    const terms = doc({ kind: 'terms', sha256: SHA_B });
    const agreed = doc({ kind: 'other', sha256: 'c'.repeat(64), agreed: true });
    const license = doc({ kind: 'license', requiresAgreement: false, sha256: 'd'.repeat(64) });
    const external = doc({ kind: 'notice', sha256: undefined, url: 'https://example.org' });

    const split = splitEntryDocuments(
      catalogEntry({ documents: [policy, terms, agreed, license, external] })
    );

    expect(split.outstanding).toEqual([
      { repo: 'org/model', kind: 'acceptable_use', sha256: SHA_A },
      { repo: 'org/model', kind: 'terms', sha256: SHA_B },
    ]);
    expect(split.reference).toEqual([agreed, license, external]);
  });

  it('asks once for a text listed under two kinds', () => {
    const split = splitEntryDocuments(
      catalogEntry({ documents: [doc({}), doc({ kind: 'terms' })] })
    );

    expect(split.outstanding).toEqual([
      { repo: 'org/model', kind: 'acceptable_use', sha256: SHA_A },
    ]);
  });

  it('splits an entry without documents into two empty lists', () => {
    expect(splitEntryDocuments(catalogEntry())).toEqual({ outstanding: [], reference: [] });
  });
});

describe('refusalDocuments', () => {
  it('names nothing when the refusal carries no usable document list', () => {
    expect(refusalDocuments(undefined)).toEqual([]);
    expect(refusalDocuments({ name: 'Model' })).toEqual([]);
    expect(refusalDocuments({ documents: 'not a list' })).toEqual([]);
    expect(
      refusalDocuments({
        documents: [null, 'x', { repo: 'r', kind: 'k' }, { repo: 'r', kind: 'k', sha256: '' }],
      })
    ).toEqual([]);
  });

  it('keeps each refused document once, with only the fields the dialog uses', () => {
    expect(
      refusalDocuments({
        documents: [
          { repo: 'r', kind: 'acceptable_use', sha256: SHA_A, extra: true },
          { repo: 'r', kind: 'license', sha256: SHA_B },
          { repo: 'other', kind: 'acceptable_use', sha256: SHA_A },
        ],
      })
    ).toEqual([
      { repo: 'r', kind: 'acceptable_use', sha256: SHA_A },
      { repo: 'r', kind: 'license', sha256: SHA_B },
    ]);
  });
});
