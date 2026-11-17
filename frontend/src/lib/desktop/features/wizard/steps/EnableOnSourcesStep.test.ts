import { describe, it, expect, beforeEach, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import { deferred } from '../../../../../test/async-helpers';
import type { AcousticModelAvailability } from '$lib/types/models';
import type { InstalledModelContext } from '../types';

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: {
    get: vi.fn().mockResolvedValue([]),
    post: vi.fn(),
  },
}));

vi.mock('$lib/stores/settings', async () => {
  const { createSettingsMock } = await import('./stepTestUtils');
  return createSettingsMock({ realtime: { audio: { sources: [] }, rtsp: { streams: [] } } });
});

// The verdict and the topology stream are not under test; the default targets are
const acoustic = vi.hoisted(() => {
  const state: { availability: AcousticModelAvailability } = {
    availability: { kind: 'unknown' },
  };
  return state;
});
vi.mock('$lib/stores/acousticModels.svelte', () => ({
  acousticModelAvailability: () => acoustic.availability,
  subscribeAcousticModels: () => () => {},
  watchAcousticModels: () => () => {},
  onTopologyChanged: () => () => {},
}));

const wizard = vi.hoisted(() => {
  const state: { model: InstalledModelContext | null } = { model: null };
  return state;
});
vi.mock('../wizardState.svelte', () => ({
  wizardState: {
    get installedModel() {
      return wizard.model;
    },
    skip: vi.fn(),
  },
}));

import EnableOnSourcesStep from './EnableOnSourcesStep.svelte';
import { settingsActions, settingsStore } from '$lib/stores/settings';
import type { AudioSourceConfig, SettingsFormData, StreamConfig } from '$lib/stores/settings';
import { api } from '$lib/utils/api';
import { resetModelsForTest, type BackendModel } from '$lib/stores/models.svelte';
import { renderStep } from './stepTestUtils';
import { toastActions } from '$lib/stores/toast';
import { t } from '$lib/i18n';

const KEY = 'wizard.steps.enableOnSources';

const BIRDNET: BackendModel = {
  id: 'birdnet',
  registryId: 'BirdNET_V2.4',
  name: 'BirdNET v2.4',
  category: 'bird',
  state: 'loaded',
};
const PERCH: BackendModel = {
  id: 'perch_v2',
  registryId: 'Perch_V2',
  name: 'Perch v2',
  category: 'bird',
  state: 'loaded',
};
const BAT: BackendModel = {
  id: 'bat',
  registryId: 'Bat',
  name: 'BattyBirdNET',
  category: 'bat',
  state: 'loaded',
};

const PERCH_CONTEXT: InstalledModelContext = {
  catalogId: 'perch-v2',
  name: 'Perch v2',
  category: 'bird',
  registryId: 'Perch_V2',
};
const BAT_CONTEXT: InstalledModelContext = {
  catalogId: 'battybirdnet-eu',
  name: 'BattyBirdNET',
  category: 'bat',
  registryId: 'Bat',
};

function card(name: string, models: string[], sampleRate = 48000): AudioSourceConfig {
  return { name, device: `hw:${name}`, gain: 0, sampleRate, models };
}
function stream(name: string, models: string[]): StreamConfig {
  return { name, url: `rtsp://${name}.example/live`, enabled: true, type: 'rtsp', models };
}

// The step reads only realtime.audio.sources and realtime.rtsp.streams, so the
// settings fixture is deliberately partial; it reads what the server holds, so
// both copies are seeded.
function realtimeOnly(sources: AudioSourceConfig[], streams: StreamConfig[]): SettingsFormData {
  const realtime = { audio: { sources }, rtsp: { streams } };
  const copy: unknown = JSON.parse(JSON.stringify({ realtime }));
  return copy as SettingsFormData;
}

function seed(sources: AudioSourceConfig[], streams: StreamConfig[] = []) {
  settingsStore.update(state => ({
    ...state,
    originalData: realtimeOnly(sources, streams),
    formData: realtimeOnly(sources, streams),
  }));
}

function serveModels(models: BackendModel[]) {
  vi.mocked(api.get).mockImplementation(async () => models);
}

const sourceSwitch = (name: string) => screen.getByRole('switch', { name });

async function renderLoaded(model = PERCH_CONTEXT) {
  wizard.model = model;
  const result = renderStep(EnableOnSourcesStep);
  await screen.findAllByRole('switch');
  return result;
}

describe('EnableOnSourcesStep', () => {
  beforeEach(() => {
    resetModelsForTest();
    vi.mocked(settingsActions.saveSection).mockReset().mockResolvedValue(undefined);
    vi.mocked(toastActions.success).mockClear();
    vi.mocked(t).mockClear();
    acoustic.availability = { kind: 'ready', defaultTargets: ['BirdNET_V2.4', 'Perch_V2'] };
    serveModels([BIRDNET, PERCH]);
    seed([card('Garden', ['birdnet'])], [stream('Feeder', ['birdnet'])]);
  });

  it('shows its loading state until the model list has loaded', async () => {
    const response = deferred<BackendModel[]>();
    vi.mocked(api.get).mockImplementation(() => response.promise);
    wizard.model = PERCH_CONTEXT;

    renderStep(EnableOnSourcesStep);

    expect(await screen.findByText('settings.audio.models.loading')).toBeInTheDocument();
    expect(screen.queryByRole('switch')).toBeNull();

    response.resolve([BIRDNET, PERCH]);

    expect(await screen.findByRole('switch', { name: 'Garden' })).toBeInTheDocument();
    expect(screen.queryByText('settings.audio.models.loading')).toBeNull();
  });

  it('appends the alias to an explicit list and saves only the changed section', async () => {
    const { leave } = await renderLoaded();

    await fireEvent.click(sourceSwitch('Garden'));
    await leave();

    expect(settingsActions.saveSection).toHaveBeenCalledTimes(1);
    expect(settingsActions.saveSection).toHaveBeenCalledWith('audio', {
      sources: [card('Garden', ['birdnet', 'perch_v2'])],
    });
  });

  it('turns every source on with the all sources switch, saving each section once', async () => {
    const { leave } = await renderLoaded();

    await fireEvent.click(sourceSwitch(`${KEY}.allSources`));
    await leave();

    expect(settingsActions.saveSection).toHaveBeenCalledWith('audio', {
      sources: [card('Garden', ['birdnet', 'perch_v2'])],
    });
    expect(settingsActions.saveSection).toHaveBeenCalledWith('rtsp', {
      streams: [stream('Feeder', ['birdnet', 'perch_v2'])],
    });
    expect(settingsActions.saveSection).toHaveBeenCalledTimes(2);
  });

  it('does not rewrite a source that follows the defaults and already runs the model', async () => {
    seed([card('Default', []), card('Garden', ['birdnet'])]);
    const { leave } = await renderLoaded();

    const followsDefaults = sourceSwitch('Default');
    expect(followsDefaults).toBeChecked();
    expect(followsDefaults).toHaveAttribute('aria-disabled', 'true');

    await fireEvent.click(followsDefaults);
    await fireEvent.click(sourceSwitch('Garden'));
    await leave();

    expect(settingsActions.saveSection).toHaveBeenCalledTimes(1);
    expect(settingsActions.saveSection).toHaveBeenCalledWith('audio', {
      sources: [card('Default', []), card('Garden', ['birdnet', 'perch_v2'])],
    });
  });

  it('writes the defaults plus the model for a default following source that does not run it', async () => {
    // Bat models are schedule gated, so they are not default targets (orchestrator.go DefaultTargets)
    serveModels([BIRDNET, PERCH, BAT]);
    seed([card('Default', [])]);
    const { leave } = await renderLoaded(BAT_CONTEXT);

    await fireEvent.click(sourceSwitch('Default'));
    await leave();

    expect(settingsActions.saveSection).toHaveBeenCalledWith('audio', {
      sources: [card('Default', ['birdnet', 'perch_v2', 'bat'])],
    });
  });

  it('stops after the sound card save when the wizard closes during it', async () => {
    const { leave } = await renderLoaded();
    vi.mocked(settingsActions.saveSection).mockImplementation(async () => {
      // Not now pressed while the sound card save runs
      wizard.model = null;
    });

    await fireEvent.click(sourceSwitch(`${KEY}.allSources`));
    await leave();

    expect(settingsActions.saveSection).toHaveBeenCalledTimes(1);
    expect(settingsActions.saveSection).toHaveBeenCalledWith('audio', expect.anything());
    // The sound card that did change is still reported
    expect(t).toHaveBeenCalledWith(`${KEY}.enabledToast`, { name: 'Perch v2', count: 1 });
    expect(toastActions.success).toHaveBeenCalledTimes(1);
  });

  it('reports the sound cards that changed when the stream save then fails', async () => {
    const { leave } = await renderLoaded();
    vi.mocked(settingsActions.saveSection).mockImplementation(async section => {
      if (section === 'rtsp') throw new Error('save rtsp failed');
    });

    await fireEvent.click(sourceSwitch(`${KEY}.allSources`));
    await expect(leave()).rejects.toThrow('save rtsp failed');

    expect(t).toHaveBeenCalledWith(`${KEY}.enabledToast`, { name: 'Perch v2', count: 1 });
    expect(toastActions.success).toHaveBeenCalledTimes(1);
  });

  it('saves nothing when the user leaves without turning a source on', async () => {
    const { leave } = await renderLoaded();

    await leave();

    expect(settingsActions.saveSection).not.toHaveBeenCalled();
  });

  it('offers no switches when the model is not loaded', async () => {
    serveModels([BIRDNET, { ...PERCH, state: 'failed' }]);
    const onValidChange = vi.fn();
    wizard.model = PERCH_CONTEXT;

    const { leave } = renderStep(EnableOnSourcesStep, { onValidChange });

    expect(await screen.findByText(`${KEY}.notLoadedFailed`)).toBeInTheDocument();
    expect(screen.queryByRole('switch')).toBeNull();
    expect(onValidChange).toHaveBeenLastCalledWith(true);
    await leave();
    expect(settingsActions.saveSection).not.toHaveBeenCalled();
  });

  it('says so when every source already runs the model', async () => {
    seed([card('Garden', ['birdnet', 'perch_v2'])], [stream('Feeder', [])]);
    wizard.model = PERCH_CONTEXT;

    renderStep(EnableOnSourcesStep);

    expect(await screen.findByText(`${KEY}.allRunning`)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByRole('switch', { name: `${KEY}.allSources` })).toBeNull()
    );
  });

  it('points to the audio settings when no source is configured', async () => {
    seed([], []);
    wizard.model = PERCH_CONTEXT;

    renderStep(EnableOnSourcesStep);

    expect(await screen.findByText(`${KEY}.noSources`)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: `${KEY}.openAudioSettings` })).toBeInTheDocument();
  });
});
