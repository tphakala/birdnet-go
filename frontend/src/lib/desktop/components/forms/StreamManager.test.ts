/**
 * StreamManager keeps the model list live while mounted by watching it through
 * the models store, so an install or uninstall reaches an open stream editor.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';

vi.mock('$lib/stores/models.svelte', () => ({
  getAvailableModels: vi.fn(() => []),
  DEFAULT_MODEL_ID: 'birdnet',
  watchModels: vi.fn(() => () => {}),
  modelsLoading: vi.fn(() => false),
}));

vi.mock('$lib/stores/acousticModels.svelte', () => ({
  subscribeAcousticModels: vi.fn(() => () => {}),
  acousticModelAvailability: vi.fn(() => ({ kind: 'unknown' })),
}));

vi.mock('$lib/stores/quietHours.svelte', () => ({
  quietHoursStore: { startPolling: vi.fn(), stopPolling: vi.fn() },
}));

vi.mock('$lib/utils/api', () => ({
  api: { get: vi.fn(() => Promise.resolve([])) },
}));

import { watchModels } from '$lib/stores/models.svelte';
import StreamManager from './StreamManager.svelte';

describe('StreamManager', () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('keeps the model list live while mounted, through watchModels', () => {
    renderTyped(StreamManager, {
      props: { streams: [], disabled: false, onUpdateStreams: vi.fn() },
    });

    expect(watchModels).toHaveBeenCalledTimes(1);
  });
});
