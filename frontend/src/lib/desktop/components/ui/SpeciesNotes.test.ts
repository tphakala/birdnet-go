import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import SpeciesNotes from './SpeciesNotes.svelte';
import type { SpeciesNoteData } from '$lib/types/species';

// Controllable auth store shared across tests.
const mocks = vi.hoisted(() => {
  let value = true;
  const subs = new Set<(v: boolean) => void>();
  return {
    authStore: {
      subscribe(fn: (v: boolean) => void) {
        fn(value);
        subs.add(fn);
        return () => subs.delete(fn);
      },
      set(v: boolean) {
        value = v;
        subs.forEach(fn => fn(value));
      },
    },
  };
});

vi.mock('$lib/utils/auth', () => ({ isAuthenticated: mocks.authStore }));
vi.mock('$lib/utils/formatters', () => ({ formatDate: (s: string) => s }));
vi.mock('$lib/utils/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(message: string, status: number) {
      super(message);
      this.status = status;
    }
  },
}));

import { api } from '$lib/utils/api';

function note(overrides: Partial<SpeciesNoteData> = {}): SpeciesNoteData {
  return {
    id: 1,
    entry: 'A note about this bird.',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

describe('SpeciesNotes', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.authStore.set(true);
  });

  it('renders existing notes', async () => {
    vi.mocked(api.get).mockResolvedValue([note({ entry: 'Heard at dawn.' })] as never);
    render(SpeciesNotes, { props: { scientificName: 'Turdus merula' } });
    expect(await screen.findByText('Heard at dawn.', {}, { timeout: 5000 })).toBeInTheDocument();
  });

  it('shows the empty state when there are no notes', async () => {
    vi.mocked(api.get).mockResolvedValue([] as never);
    render(SpeciesNotes, { props: { scientificName: 'Turdus merula' } });
    expect(
      await screen.findByText('analytics.species.notes.empty', {}, { timeout: 5000 })
    ).toBeInTheDocument();
  });

  it('hides the editor when not authenticated', async () => {
    mocks.authStore.set(false);
    vi.mocked(api.get).mockResolvedValue([note()] as never);
    render(SpeciesNotes, { props: { scientificName: 'Turdus merula' } });
    await screen.findByText('A note about this bird.', {}, { timeout: 5000 });
    // No add button / textarea for unauthenticated users.
    expect(screen.queryByText('analytics.species.notes.save')).not.toBeInTheDocument();
  });

  it('adds a note when authenticated', async () => {
    vi.mocked(api.get).mockResolvedValue([] as never);
    vi.mocked(api.post).mockResolvedValue(note({ id: 2, entry: 'New note.' }) as never);
    render(SpeciesNotes, { props: { scientificName: 'Turdus merula' } });

    const textarea = await screen.findByPlaceholderText(
      'analytics.species.notes.placeholder',
      {},
      { timeout: 5000 }
    );
    // Type into the textarea (fireEvent.input drives Svelte's bind:value) and submit.
    await fireEvent.input(textarea, { target: { value: 'New note.' } });
    const saveButton = screen.getByText('analytics.species.notes.save').closest('button');
    await fireEvent.click(saveButton as HTMLButtonElement);

    await screen.findByText('New note.', {}, { timeout: 5000 });
    expect(api.post).toHaveBeenCalledWith(
      '/api/v2/species/Turdus%20merula/notes',
      expect.objectContaining({ entry: 'New note.' })
    );
  });
});
