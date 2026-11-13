import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent, render, within } from '@testing-library/svelte';
import type { ComponentProps } from 'svelte';
import { screen } from '../../../../../test/render-helpers';
import type { CatalogEntry, DownloadProgress } from '$lib/types/models';
import { CHANNEL_STABLE } from '$lib/utils/variantSelection';
import ModelCardGrid from './ModelCardGrid.svelte';

const PAUSED_ID = 'gallery-action-status';
const CLAMPED_HEIGHT = 50;
const FULL_HEIGHT = 84;
const LONG_DESCRIPTION = 'Model a has a description too long for three lines.';

function entry(id: string, overrides: Partial<CatalogEntry> = {}): CatalogEntry {
  return {
    id,
    name: `Model ${id}`,
    description: `Model ${id} description`,
    author: 'Tester',
    license: 'MIT',
    commercialUse: true,
    category: 'bird',
    region: '',
    speciesCount: 100,
    version: '1.0',
    channel: CHANNEL_STABLE,
    installed: true,
    compatible: true,
    totalSizeBytes: 1_000_000,
    hasGeomodel: false,
    ...overrides,
  };
}

const progress: DownloadProgress = {
  catalogId: 'a',
  status: 'downloading',
  downloadedBytes: 1,
  totalBytes: 2,
  currentFile: 1,
  totalFiles: 1,
};

type GridProps = ComponentProps<typeof ModelCardGrid>;

function renderGrid(props: Partial<GridProps> & Pick<GridProps, 'entries'>) {
  return render(ModelCardGrid, {
    props: {
      mode: 'installed',
      regionNames: new Map(),
      activeAction: null,
      progress: null,
      pausedReasonId: PAUSED_ID,
      onAction: vi.fn(),
      ...props,
    },
  });
}

function card(name: string): HTMLElement {
  return screen.getByRole('article', { name });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ModelCardGrid', () => {
  it('renders nothing for no entries', () => {
    const { container } = renderGrid({ entries: [] });

    // Svelte leaves an anchor comment behind, so count elements rather than nodes
    expect(container.children).toHaveLength(0);
  });

  it('renders one card per entry, in order', () => {
    renderGrid({ entries: [entry('b'), entry('a')] });

    expect(screen.getAllByRole('article')).toHaveLength(2);
    expect(screen.getAllByRole('heading').map(h => h.textContent.trim())).toEqual([
      'Model b',
      'Model a',
    ]);
  });

  it('runs the action on its own card and pauses the other cards', () => {
    renderGrid({
      entries: [entry('a'), entry('b')],
      activeAction: { entryId: 'a', action: 'reinstall' },
      progress,
    });

    const reinstallA = within(card('Model a')).getByRole('button', {
      name: 'analysis.gallery.reinstall Model a',
    });
    expect(reinstallA).toBeDisabled();
    expect(reinstallA).toHaveTextContent('analysis.gallery.reinstalling');
    for (const button of within(card('Model b')).getAllByRole('button')) {
      expect(button).toHaveAttribute('aria-disabled', 'true');
    }
    expect(within(card('Model a')).getByRole('progressbar')).toBeInTheDocument();
    expect(within(card('Model b')).queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('shows no progress bar while a remove runs', () => {
    renderGrid({
      entries: [entry('a')],
      activeAction: { entryId: 'a', action: 'remove' },
      progress,
    });

    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('offers Optimize only on the entries named in optimizeEntryIds', () => {
    renderGrid({ entries: [entry('a'), entry('b')], optimizeEntryIds: new Set(['b']) });

    const optimize = /analysis\.gallery\.optimize\.swap/;
    expect(within(card('Model a')).queryByRole('button', { name: optimize })).toBeNull();
    expect(within(card('Model b')).getByRole('button', { name: optimize })).toBeInTheDocument();
  });

  it('keeps a card expanded when the entries are reordered or another is removed', async () => {
    vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(CLAMPED_HEIGHT);
    vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockImplementation(function (this: Element) {
      return this.textContent === LONG_DESCRIPTION ? FULL_HEIGHT : CLAMPED_HEIGHT;
    });
    const a = entry('a', { description: LONG_DESCRIPTION });
    const b = entry('b');
    const { rerender } = renderGrid({ entries: [a, b] });

    const toggleOfA = () =>
      within(card('Model a')).getByRole('button', { name: /^common\.ui\.show/ });
    await fireEvent.click(toggleOfA());
    expect(toggleOfA()).toHaveAttribute('aria-expanded', 'true');

    await rerender({ entries: [b, a] });
    expect(toggleOfA()).toHaveAttribute('aria-expanded', 'true');

    await rerender({ entries: [a] });
    expect(toggleOfA()).toHaveAttribute('aria-expanded', 'true');
  });
});
