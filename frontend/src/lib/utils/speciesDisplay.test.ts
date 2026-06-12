import { describe, it, expect, vi, beforeEach } from 'vitest';
import { localizeSpeciesName } from './speciesDisplay';
import { localizeScientific } from '$lib/stores/speciesDictionary.svelte';

// Mock the dictionary store so we control forward lookups per test.
vi.mock('$lib/stores/speciesDictionary.svelte', () => ({
  localizeScientific: vi.fn(),
}));

const mockedLocalizeScientific = vi.mocked(localizeScientific);

describe('localizeSpeciesName', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('returns the dictionary hit when the scientific name is found (dictionary wins)', () => {
    mockedLocalizeScientific.mockReturnValue('Peukaloinen');

    const result = localizeSpeciesName('Troglodytes troglodytes', 'Eurasian Wren');

    expect(result).toBe('Peukaloinen');
    expect(mockedLocalizeScientific).toHaveBeenCalledWith('Troglodytes troglodytes');
  });

  it('falls back to the server common name when the dictionary misses', () => {
    mockedLocalizeScientific.mockReturnValue(undefined);

    const result = localizeSpeciesName('Troglodytes troglodytes', 'Eurasian Wren');

    expect(result).toBe('Eurasian Wren');
  });

  it('falls back to the scientific name when there is no server common name', () => {
    mockedLocalizeScientific.mockReturnValue(undefined);

    const result = localizeSpeciesName('Troglodytes troglodytes');

    expect(result).toBe('Troglodytes troglodytes');
  });

  it('returns an empty string when both scientific and common names are empty', () => {
    mockedLocalizeScientific.mockReturnValue(undefined);

    const result = localizeSpeciesName('', '');

    expect(result).toBe('');
  });

  it('does not attempt a dictionary lookup when the scientific name is empty', () => {
    const result = localizeSpeciesName(undefined, 'Eurasian Wren');

    expect(result).toBe('Eurasian Wren');
    expect(mockedLocalizeScientific).not.toHaveBeenCalled();
  });
});
