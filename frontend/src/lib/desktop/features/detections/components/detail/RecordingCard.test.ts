import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, screen } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { tick } from 'svelte';
import { renderTyped } from '../../../../../../test/render-helpers';
import { seedSavedRealtime } from '../../../../../../test/settings-store-helpers';
import { settingsStore, type DenoiseSettings } from '$lib/stores/settings';
import RecordingCard from './RecordingCard.svelte';
import type { DetectionDetailData } from './detailTypes';

vi.mock('$lib/desktop/components/media/AudioPlayer.svelte');

const detection: DetectionDetailData = {
  id: 7,
  date: '2026-11-13',
  time: '23:27:37',
  beginTime: '2026-11-13T23:27:29+02:00',
  endTime: '2026-11-13T23:27:56+02:00',
  speciesCode: 'dog',
  scientificName: 'Canis familiaris',
  commonName: 'Dog',
  confidence: 0.77,
  verified: 'correct',
  locked: false,
  clipName: 'clips/dog.wav',
};

const initialStore = get(settingsStore);

function savePlaybackDenoise(playbackDenoise: DenoiseSettings) {
  seedSavedRealtime({ dashboard: { playbackDenoise } });
}

describe('RecordingCard download link', () => {
  beforeEach(() => settingsStore.set(initialStore));

  afterEach(() => {
    cleanup();
    settingsStore.set(initialStore);
  });

  it('follows a denoise default saved while the page is open', async () => {
    savePlaybackDenoise({ enabled: true, strength: 'light' });
    renderTyped(RecordingCard, { props: { detection, speciesName: 'Dog' } });
    const link = screen.getByRole('link', { name: /media\.audio\.download/ });
    expect(link.getAttribute('href')).toBe('/api/v2/audio/7?denoise=light');

    savePlaybackDenoise({ enabled: false, strength: 'light' });
    await tick();

    expect(link.getAttribute('href')).toBe('/api/v2/audio/7');
  });
});
