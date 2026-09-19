/**
 * Tests for the ModelCheckboxList empty and degraded states.
 *
 * Every empty list must explain itself: still loading, no detection model
 * enabled (N=0, saving is allowed), a model that failed to load, or no model
 * enabled without a verdict. Each explanation links to the place that fixes it.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, screen, fireEvent } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import type { AcousticModelAvailability } from '$lib/types/models';

vi.mock('$lib/stores/navigation.svelte', () => ({
  handleAppLinkClick: vi.fn(),
}));

import ModelCheckboxList from './ModelCheckboxList.svelte';

const BIRDNET = { id: 'birdnet', name: 'BirdNET v2.4', category: 'bird' };
const PERCH = { id: 'perch_v2', name: 'Perch v2', category: 'bird' };

const NONE_INSTALLED: AcousticModelAvailability = { kind: 'none', reason: 'none_installed' };
const LOAD_FAILED: AcousticModelAvailability = { kind: 'none', reason: 'load_failed' };

const LOADING_KEY = 'settings.audio.models.loading';
const NONE_TITLE_KEY = 'settings.audio.models.noneEnabledTitle';
const NONE_LINK_KEY = 'settings.audio.models.noneEnabledLink';
const NONE_AVAILABLE_KEY = 'settings.audio.models.noneAvailable';
const LOAD_FAILED_KEY = 'settings.audio.models.loadFailedWarning';
const LOAD_FAILED_LINK_KEY = 'settings.audio.models.loadFailedLink';

function renderList(
  props: Partial<{
    models: Array<{ id: string; name: string; category: string }>;
    selectedModels: string[];
    loading: boolean;
    availability: AcousticModelAvailability;
    onToggle: (_models: string[]) => void;
  }> = {}
) {
  return renderTyped(ModelCheckboxList, {
    props: {
      models: [],
      selectedModels: [],
      onToggle: vi.fn(),
      ...props,
    },
  });
}

describe('ModelCheckboxList', () => {
  afterEach(() => {
    cleanup();
  });

  it('renders one checkbox per model and reports toggles', async () => {
    const onToggle = vi.fn();
    renderList({ models: [BIRDNET, PERCH], selectedModels: ['birdnet'], onToggle });

    expect(screen.getByText('BirdNET v2.4')).toBeInTheDocument();
    expect(screen.getByText('Perch v2')).toBeInTheDocument();

    const boxes = screen.getAllByRole('checkbox');
    expect(boxes).toHaveLength(2);
    await fireEvent.click(boxes[1]);
    expect(onToggle).toHaveBeenCalledWith(['birdnet', 'perch_v2']);
  });

  it('shows a labelled loading line while the list is empty and loading', () => {
    renderList({ loading: true });

    const status = screen.getByRole('status');
    expect(status).toHaveTextContent(LOADING_KEY);
    expect(screen.queryByText(NONE_AVAILABLE_KEY)).toBeNull();
  });

  it('explains N=0 with a gallery link and allows saving', () => {
    renderList({ availability: NONE_INSTALLED });

    const status = screen.getByRole('status');
    expect(status).toHaveTextContent(NONE_TITLE_KEY);
    expect(status).toHaveTextContent('settings.audio.models.noneEnabledHelp');
    const link = screen.getByRole('link', { name: NONE_LINK_KEY });
    expect(link.getAttribute('href')).toContain('/ui/settings/analysis?tab=models');
    expect(screen.queryByText(LOAD_FAILED_KEY)).toBeNull();
  });

  it('warns about a load failure with an AI Models link, even when models are listed', () => {
    renderList({ models: [BIRDNET], selectedModels: [], availability: LOAD_FAILED });

    const status = screen.getByRole('status');
    expect(status).toHaveTextContent(LOAD_FAILED_KEY);
    const link = screen.getByRole('link', { name: LOAD_FAILED_LINK_KEY });
    expect(link.getAttribute('href')).toContain('/ui/system/inference');
    expect(screen.queryByText(NONE_TITLE_KEY)).toBeNull();
  });

  it('explains an empty list without a verdict and links to the gallery', () => {
    renderList({ availability: { kind: 'unknown' } });

    expect(screen.getByRole('status')).toHaveTextContent(NONE_AVAILABLE_KEY);
    expect(screen.getByRole('link', { name: NONE_LINK_KEY })).toBeInTheDocument();
  });

  it('adds no explanation when models are listed and the verdict is ok or unknown', () => {
    renderList({
      models: [BIRDNET],
      selectedModels: ['birdnet'],
      availability: { kind: 'ready', defaultTargets: ['BirdNET_V2.4'] },
    });

    expect(screen.queryByRole('status')).toBeNull();
    expect(screen.queryByRole('link')).toBeNull();
  });

  it('prefers the loading line over the N=0 explanation while a fetch is in flight', () => {
    renderList({ loading: true, availability: NONE_INSTALLED });

    expect(screen.getByRole('status')).toHaveTextContent(LOADING_KEY);
    expect(screen.queryByText(NONE_TITLE_KEY)).toBeNull();
  });
});
