/**
 * SourceModelBadges: the classifier badges of a source card header. A warning
 * badge shows when a selected model is missing or not loaded (not while the list
 * loads or after it failed), and the summary names the selected models, the
 * defaults, or says why there are none.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import type { BackendModel } from '$lib/stores/models.svelte';
import SourceModelBadges from './SourceModelBadges.svelte';

const WARNING_KEY = 'settings.audio.models.warningBadge';
const NONE_BADGE_KEY = 'settings.audio.models.noneBadge';
const DEFAULT_BADGE_KEY = 'settings.audio.models.defaultBadge';
const DEFAULT_PENDING_KEY = 'settings.audio.models.defaultPendingBadge';

const BIRDNET: BackendModel = { id: 'birdnet', name: 'BirdNET v2.4', category: 'bird' };
const PERCH: BackendModel = { id: 'perch_v2', name: 'Perch v2', category: 'bird' };

function renderBadges(props: Partial<Parameters<typeof render<typeof SourceModelBadges>>[1]> = {}) {
  const available = [BIRDNET, PERCH];
  return render(SourceModelBadges, {
    props: {
      selectedModels: ['birdnet'],
      defaultModels: [],
      availableModels: available,
      modelOptions: available.map(m => ({ value: m.id, label: m.name })),
      availability: { kind: 'unknown' },
      listLoading: false,
      listFailed: false,
      ...props,
    },
  });
}

describe('SourceModelBadges', () => {
  afterEach(() => {
    cleanup();
  });

  it('names the selected models and shows no warning when all are loaded', () => {
    renderBadges({ selectedModels: ['birdnet', 'perch_v2'] });

    expect(screen.getByText('BirdNET v2.4, Perch v2')).toBeInTheDocument();
    expect(screen.queryByText(WARNING_KEY)).toBeNull();
  });

  it('warns about a selected model that is missing, failed or pending', () => {
    for (const selected of [
      { models: ['gone'], available: [BIRDNET] },
      { models: ['birdnet'], available: [{ ...BIRDNET, state: 'failed' as const }] },
      { models: ['birdnet'], available: [{ ...BIRDNET, state: 'pending' as const }] },
    ]) {
      const view = renderBadges({
        selectedModels: selected.models,
        availableModels: selected.available,
      });
      expect(screen.getByText(WARNING_KEY)).toBeInTheDocument();
      view.unmount();
    }
  });

  it('gives no warning while the list loads or after it failed', () => {
    const loading = renderBadges({ selectedModels: ['gone'], listLoading: true });
    expect(screen.queryByText(WARNING_KEY)).toBeNull();
    loading.unmount();

    renderBadges({ selectedModels: ['gone'], listFailed: true });
    expect(screen.queryByText(WARNING_KEY)).toBeNull();
  });

  it('summarises an empty selection by the defaults, by the none verdict, or with a hedge', () => {
    const ready = renderBadges({
      selectedModels: [],
      defaultModels: ['birdnet'],
      availability: { kind: 'ready', defaultTargets: ['BirdNET_V2.4'] },
    });
    expect(screen.getByText(DEFAULT_BADGE_KEY)).toBeInTheDocument();
    ready.unmount();

    const none = renderBadges({
      selectedModels: [],
      availability: { kind: 'none', reason: 'none_installed' },
    });
    expect(screen.getByText(NONE_BADGE_KEY)).toBeInTheDocument();
    none.unmount();

    renderBadges({ selectedModels: [], availability: { kind: 'unknown' } });
    expect(screen.getByText(DEFAULT_PENDING_KEY)).toBeInTheDocument();
  });
});
