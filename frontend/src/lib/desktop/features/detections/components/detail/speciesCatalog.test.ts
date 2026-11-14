import { afterEach, describe, expect, it, vi } from 'vitest';

// Each test imports a fresh module, so the per-page cache starts empty.
async function freshCatalog() {
  vi.resetModules();
  return import('./speciesCatalog');
}

function respond(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('loadSpeciesCatalog', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('keeps each species once and names a species without a common name by its scientific name', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          respond(200, {
            species: [
              { scientificName: 'Parus major', commonName: 'Great Tit' },
              { scientificName: ' Parus major ', commonName: 'Duplicate' },
              { scientificName: 'Anas crecca', commonName: '  ' },
              { scientificName: '', commonName: 'No scientific name' },
            ],
          })
        )
      )
    );
    const { loadSpeciesCatalog } = await freshCatalog();

    await expect(loadSpeciesCatalog('range')).resolves.toEqual([
      { scientificName: 'Parus major', commonName: 'Great Tit' },
      { scientificName: 'Anas crecca', commonName: 'Anas crecca' },
    ]);
  });

  // Regression: the answer was trusted to have the expected shape, so one malformed entry
  // threw and lost the whole list.
  it('leaves out malformed entries and keeps the rest', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          respond(200, {
            species: [
              null,
              'Parus major',
              { scientificName: 7 },
              { scientificName: 'Anas crecca' },
            ],
          })
        )
      )
    );
    const { loadSpeciesCatalog } = await freshCatalog();

    await expect(loadSpeciesCatalog('range')).resolves.toEqual([
      { scientificName: 'Anas crecca', commonName: 'Anas crecca' },
    ]);
  });

  it('reads an answer without a species list as an empty list', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(respond(200, null)))
    );
    const { loadSpeciesCatalog } = await freshCatalog();

    await expect(loadSpeciesCatalog('all')).resolves.toEqual([]);
  });

  it('requests a list once and shares it', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(respond(200, { species: [] })));
    vi.stubGlobal('fetch', fetchMock);
    const { loadSpeciesCatalog } = await freshCatalog();

    await Promise.all([loadSpeciesCatalog('all'), loadSpeciesCatalog('all')]);
    await loadSpeciesCatalog('all');

    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  // Settings change the range filter's list (location, threshold) without a page reload.
  it('asks again after the lists are forgotten', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(respond(200, { species: [] })));
    vi.stubGlobal('fetch', fetchMock);
    const { loadSpeciesCatalog, forgetSpeciesCatalogs } = await freshCatalog();

    await loadSpeciesCatalog('range');
    forgetSpeciesCatalogs();
    await loadSpeciesCatalog('range');

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('forgets a failed load, so the next opening asks again', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(respond(500, {}))
      .mockResolvedValueOnce(respond(200, { species: [] }));
    vi.stubGlobal('fetch', fetchMock);
    const { loadSpeciesCatalog } = await freshCatalog();

    await expect(loadSpeciesCatalog('range')).rejects.toThrow();
    await expect(loadSpeciesCatalog('range')).resolves.toEqual([]);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
