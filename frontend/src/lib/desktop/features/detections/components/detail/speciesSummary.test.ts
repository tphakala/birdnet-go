import { afterEach, describe, expect, it, vi } from 'vitest';

// Each test imports a fresh module, so the shared cache starts empty.
async function freshSummary() {
  vi.resetModules();
  return import('./speciesSummary');
}

function respond(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const rows = [
  { scientific_name: 'Turdus merula', count: 12 },
  { scientific_name: 'Parus major', count: 3 },
];

describe('loadSpeciesSummary', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('returns the species row, or null for a species without one', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(respond(200, rows)))
    );
    const { loadSpeciesSummary } = await freshSummary();

    await expect(loadSpeciesSummary('Parus major')).resolves.toEqual(rows[1]);
    await expect(loadSpeciesSummary('Anas crecca')).resolves.toBeNull();
  });

  it('shares one request among callers and keeps the rows for a few minutes', async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn(() => Promise.resolve(respond(200, rows)));
    vi.stubGlobal('fetch', fetchMock);
    const { loadSpeciesSummary } = await freshSummary();

    await Promise.all([loadSpeciesSummary('Turdus merula'), loadSpeciesSummary('Parus major')]);
    await loadSpeciesSummary('Turdus merula');
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // Still kept just inside the five minutes, asked again just past them.
    vi.advanceTimersByTime(299_999);
    await loadSpeciesSummary('Turdus merula');
    expect(fetchMock).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(2);
    await loadSpeciesSummary('Turdus merula');
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('does not keep a failed request, so the next call asks again', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(respond(500, {}))
      .mockResolvedValueOnce(respond(200, rows));
    vi.stubGlobal('fetch', fetchMock);
    const { loadSpeciesSummary } = await freshSummary();

    await expect(loadSpeciesSummary('Turdus merula')).rejects.toThrow();
    await expect(loadSpeciesSummary('Turdus merula')).resolves.toEqual(rows[0]);
  });

  it('reads an answer that is not a list as no rows', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(respond(200, { rows })))
    );
    const { loadSpeciesSummary } = await freshSummary();

    await expect(loadSpeciesSummary('Turdus merula')).resolves.toBeNull();
  });
});
