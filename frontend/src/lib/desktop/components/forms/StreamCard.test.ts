import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, cleanup, fireEvent, render } from '@testing-library/svelte';
import StreamCard from './StreamCard.svelte';
import type { StreamConfig } from '$lib/stores/settings';

// Replace heavy edit-mode children with inert components; the model list is a
// functional stand-in that can clear the selection so the save-path guard is
// exercised.
vi.mock('./ModelCheckboxList.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockModelCheckboxListClearable.svelte'))
    .default,
}));
vi.mock('./SelectDropdown.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('./InlineSlider.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('./Checkbox.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('./QuietHoursEditor.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('./StreamTestButton.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('./StreamTimeline.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('./StreamChannelControls.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('$lib/desktop/components/ui/StatusPill.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));

// Model store: only DEFAULT_MODEL_ID (used by the real defaultModelSelection) and
// modelsLoading are read here. defaultModelSelection itself stays real so the
// mapping the guard relies on is genuinely exercised.
vi.mock('$lib/stores/models.svelte', () => ({
  DEFAULT_MODEL_ID: 'birdnet',
  modelsLoading: vi.fn(() => false),
}));

// The acoustic verdict drives what the empty-list guard rewrites to.
vi.mock('$lib/stores/acousticModels.svelte', () => ({
  acousticModelAvailability: vi.fn(() => ({ kind: 'ready', defaultTargets: ['birdnet'] })),
}));

// The global setup mocks $lib/utils/security without maskUrlCredentials, which
// StreamCard reads for its view-mode URL. Keep the real module here.
vi.mock('$lib/utils/security', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/security')>()),
}));

import { acousticModelAvailability } from '$lib/stores/acousticModels.svelte';

const AVAILABLE_MODELS = [{ id: 'birdnet', name: 'BirdNET v2.4', category: 'bird' }];

function renderCard(onUpdate: (_stream: StreamConfig) => boolean) {
  const stream: StreamConfig = {
    name: 'Front Yard',
    url: 'rtsp://host/stream',
    enabled: true,
    type: 'rtsp',
    models: ['birdnet'],
  };
  // Raw render (not renderTyped) so the streamHealth context StreamCard reads
  // via getContext can be provided through Svelte's mount options.
  return render(StreamCard, {
    context: new Map<string, unknown>([['streamHealth', {}]]),
    props: {
      stream,
      index: 0,
      availableModels: AVAILABLE_MODELS,
      onUpdate,
      onDelete: vi.fn(),
    },
  });
}

// Enter edit mode, clear every model, then save.
async function editClearAndSave() {
  await fireEvent.click(screen.getByRole('button', { name: 'common.edit' }));
  await fireEvent.click(screen.getByTestId('clear-models'));
  await fireEvent.click(screen.getByRole('button', { name: 'common.save' }));
}

describe('StreamCard empty-list save guard (H1 consistency)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it('rewrites a cleared model list to the mapped defaults at N>=1', async () => {
    vi.mocked(acousticModelAvailability).mockReturnValue({
      kind: 'ready',
      defaultTargets: ['birdnet'],
    });

    const onUpdate = vi.fn((_stream: StreamConfig) => true);
    renderCard(onUpdate);

    await editClearAndSave();

    expect(onUpdate).toHaveBeenCalledTimes(1);
    // The guard must restore the classifier defaults, not persist [].
    expect(onUpdate.mock.lastCall?.[0]?.models).toEqual(['birdnet']);
  });

  it('persists an empty model list at N=0 (guard is a no-op)', async () => {
    vi.mocked(acousticModelAvailability).mockReturnValue({
      kind: 'none',
      reason: 'none_installed',
    });

    const onUpdate = vi.fn((_stream: StreamConfig) => true);
    renderCard(onUpdate);

    await editClearAndSave();

    expect(onUpdate).toHaveBeenCalledTimes(1);
    // At N=0 there is nothing to map, so the cleared list saves as [].
    expect(onUpdate.mock.lastCall?.[0]?.models).toEqual([]);
  });
});
