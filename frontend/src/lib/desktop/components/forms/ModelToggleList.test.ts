/**
 * Tests for ModelToggleList, the classifier list of an immediately saved audio
 * source. Each row state of the list decides what a toggle may do, and every
 * change is handed to the parent as the complete new model list.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, screen, fireEvent } from '@testing-library/svelte';
import { createComponentTestFactory, renderTyped } from '../../../../test/render-helpers';
import type { AcousticModelAvailability } from '$lib/types/models';
import type { BackendModel } from '$lib/stores/models.svelte';

vi.mock('$lib/stores/navigation.svelte', () => ({
  handleAppLinkClick: vi.fn(),
}));

import ModelToggleList from './ModelToggleList.svelte';

const BIRDNET: BackendModel = {
  id: 'birdnet',
  name: 'BirdNET v2.4',
  category: 'bird',
  backend: 'ONNX',
  precision: 'INT8',
  state: 'loaded',
};
const PERCH: BackendModel = { id: 'perch_v2', name: 'Perch v2', category: 'bird', state: 'loaded' };
const BAT_PENDING: BackendModel = {
  id: 'bat',
  name: 'Bat model',
  category: 'bat',
  state: 'pending',
};
const BAT_FAILED: BackendModel = { id: 'bat', name: 'Bat model', category: 'bat', state: 'failed' };

const NONE_INSTALLED: AcousticModelAvailability = { kind: 'none', reason: 'none_installed' };

const LAST_SELECTED_KEY = 'settings.audio.models.lastSelectedHelp';
const NOT_LOADED_HELP_KEY = 'settings.audio.models.notLoadedHelp';
const MISSING_KEY = 'settings.audio.models.state.missing';
const MISSING_HELP_KEY = 'settings.audio.models.missingHelp';
const REMOVE_MISSING_ARIA_KEY = 'settings.audio.models.removeMissingAria';
const FOLLOWS_DEFAULTS_KEY = 'settings.audio.models.followsDefaults';
const LIST_FAILED_KEY = 'settings.audio.models.listFailed';
const LOADING_KEY = 'settings.audio.models.loading';
const NONE_TITLE_KEY = 'settings.audio.models.noneEnabledTitle';

type ListProps = Partial<{
  models: BackendModel[];
  selectedModels: string[];
  defaultModels: string[];
  loading: boolean;
  failed: boolean;
  availability: AcousticModelAvailability;
  onChange: (_models: string[]) => void;
  onRetry: () => void;
}>;

function renderList(props: ListProps = {}) {
  return renderTyped(ModelToggleList, {
    props: {
      models: [BIRDNET, PERCH],
      selectedModels: ['birdnet'],
      defaultModels: [],
      sourceName: 'Garden mic',
      onChange: vi.fn(),
      ...props,
    },
  });
}

function toggle(name: string): HTMLElement {
  return screen.getByRole('switch', { name });
}

describe('ModelToggleList', () => {
  afterEach(() => {
    cleanup();
  });

  it('shows the live backend and precision of a loaded model, and nothing when unknown', () => {
    renderList();

    expect(screen.getByText('ONNX · INT8')).toBeInTheDocument();
    // Perch reports no backend, so it gets no badge and no "(...)" suffix.
    expect(toggle('Perch v2')).toBeInTheDocument();
    expect(screen.queryByText(/Perch v2 \(/)).toBeNull();
  });

  it('turns a model on and off by handing the complete new list to the parent', async () => {
    const onChange = vi.fn();
    renderList({ selectedModels: ['birdnet'], onChange });

    await fireEvent.click(toggle('Perch v2'));
    expect(onChange).toHaveBeenLastCalledWith(['birdnet', 'perch_v2']);
  });

  it('keeps the last selected model on and says why', async () => {
    const onChange = vi.fn();
    renderList({ selectedModels: ['birdnet'], onChange });

    const last = toggle('BirdNET v2.4');
    expect(last).toBeChecked();
    expect(last).toHaveAttribute('aria-disabled', 'true');
    expect(last).toHaveAccessibleDescription(expect.stringContaining(LAST_SELECTED_KEY));

    await fireEvent.click(last);
    expect(onChange).not.toHaveBeenCalled();
  });

  it('does not let a pending model be turned on, but lets it be turned off', async () => {
    const onChange = vi.fn();
    const { unmount } = renderList({
      models: [BIRDNET, BAT_PENDING],
      selectedModels: ['birdnet'],
      onChange,
    });

    const pending = toggle('Bat model');
    expect(pending).toHaveAttribute('aria-disabled', 'true');
    expect(pending).toHaveAccessibleDescription(expect.stringContaining(NOT_LOADED_HELP_KEY));
    await fireEvent.click(pending);
    expect(onChange).not.toHaveBeenCalled();
    unmount();

    renderList({ models: [BIRDNET, BAT_PENDING], selectedModels: ['birdnet', 'bat'], onChange });
    await fireEvent.click(toggle('Bat model'));
    expect(onChange).toHaveBeenLastCalledWith(['birdnet']);
  });

  it('does not let a model that failed to load be turned on', async () => {
    const onChange = vi.fn();
    renderList({ models: [BIRDNET, BAT_FAILED], selectedModels: ['birdnet'], onChange });

    expect(screen.getByText('settings.audio.models.state.failed')).toBeInTheDocument();
    await fireEvent.click(toggle('Bat model'));
    expect(onChange).not.toHaveBeenCalled();
  });

  it('shows a selected model that is no longer listed as missing, with a Remove that works while the list is enabled', async () => {
    const onChange = vi.fn();
    renderList({ models: [BIRDNET], selectedModels: ['perch_v2'], onChange });

    // The raw alias is shown, since the list has no name for it.
    expect(screen.getByText('perch_v2')).toBeInTheDocument();
    expect(screen.getByText(MISSING_KEY)).toBeInTheDocument();
    expect(screen.getByText(MISSING_HELP_KEY)).toBeInTheDocument();

    // Removing the last explicit model is allowed: the source then follows the defaults.
    await fireEvent.click(screen.getByRole('button', { name: REMOVE_MISSING_ARIA_KEY }));
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it('shows one row for a missing alias that the config lists twice, and Remove drops both', async () => {
    const onChange = vi.fn();
    renderList({
      models: [BIRDNET],
      selectedModels: ['perch_v2', 'birdnet', 'perch_v2'],
      onChange,
    });

    expect(screen.getAllByText('perch_v2')).toHaveLength(1);
    await fireEvent.click(screen.getByRole('button', { name: REMOVE_MISSING_ARIA_KEY }));
    expect(onChange).toHaveBeenLastCalledWith(['birdnet']);
  });

  it('counts a listed alias once, so the last selected model stays on', async () => {
    const onChange = vi.fn();
    renderList({ models: [BIRDNET], selectedModels: ['birdnet', 'birdnet'], onChange });

    const last = toggle('BirdNET v2.4');
    expect(last).toHaveAttribute('aria-disabled', 'true');
    await fireEvent.click(last);
    expect(onChange).not.toHaveBeenCalled();
  });

  it('turns a missing row back into a toggle when the model returns, still on', async () => {
    const { rerender } = createComponentTestFactory(ModelToggleList, {
      models: [BIRDNET, PERCH],
      selectedModels: ['birdnet', 'perch_v2'],
      defaultModels: [],
      sourceName: 'Garden mic',
      onChange: vi.fn(),
    }).render();
    expect(toggle('Perch v2')).toBeChecked();

    await rerender({ models: [BIRDNET] });
    expect(screen.queryByRole('switch', { name: 'Perch v2' })).toBeNull();
    expect(screen.getByText('perch_v2')).toBeInTheDocument();

    await rerender({ models: [BIRDNET, PERCH] });
    expect(toggle('Perch v2')).toBeChecked();
    expect(screen.queryByText(MISSING_KEY)).toBeNull();
  });

  it('shows the default models as on while the source follows them, and writes an explicit list on the first toggle', async () => {
    const onChange = vi.fn();
    renderList({ selectedModels: [], defaultModels: ['birdnet', 'perch_v2'], onChange });

    expect(screen.getByText(FOLLOWS_DEFAULTS_KEY)).toBeInTheDocument();
    expect(toggle('BirdNET v2.4')).toBeChecked();
    expect(toggle('Perch v2')).toBeChecked();

    await fireEvent.click(toggle('Perch v2'));
    expect(onChange).toHaveBeenLastCalledWith(['birdnet']);
  });

  it('shows a labelled skeleton while the list loads for the first time', () => {
    renderList({ models: [], loading: true });

    expect(screen.getByRole('status')).toHaveTextContent(LOADING_KEY);
    expect(screen.queryByRole('switch')).toBeNull();
  });

  it('explains a failed list fetch, offers a retry and lists the saved models read-only', async () => {
    const onRetry = vi.fn();
    renderList({ models: [], failed: true, selectedModels: ['birdnet', 'perch_v2'], onRetry });

    expect(screen.getByText(LIST_FAILED_KEY)).toBeInTheDocument();
    expect(screen.getByText('birdnet')).toBeInTheDocument();
    expect(screen.getByText('perch_v2')).toBeInTheDocument();
    expect(screen.queryByRole('switch')).toBeNull();

    await fireEvent.click(screen.getByRole('button', { name: 'common.retry' }));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it('explains an empty list with the gallery link when no model is installed', () => {
    renderList({ models: [], selectedModels: [], availability: NONE_INSTALLED });

    expect(screen.getByText(NONE_TITLE_KEY)).toBeInTheDocument();
  });
});
