import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import StreamTestButton from './StreamTestButton.svelte';
import { api } from '$lib/utils/api';
import { t } from '$lib/i18n';

vi.mock('$lib/utils/api', () => ({
  api: { post: vi.fn() },
}));

const BUTTON_KEY = 'settings.audio.streams.test.button';
const COMPATIBLE_KEY = 'settings.audio.streams.test.batCompatible';
const INCOMPATIBLE_KEY = 'settings.audio.streams.test.batIncompatible';
const MIN_RATE_KEY = 'settings.audio.streams.test.minRateWarning';
const LOSSY_KEY = 'settings.audio.streams.codecWarning.lossy';

const MODELS = [
  { id: 'birdnet', name: 'BirdNET', category: 'bird' },
  { id: 'bat', name: 'BattyBirdNET', category: 'bat', minSampleRate: 96000 },
  { id: 'bsgbat', name: 'BSG-BAT', category: 'bat', minSampleRate: 192000 },
];

interface ProbeFixture {
  sampleRate: number;
  codec: string;
  batCompatible: boolean;
}

// The backend marks a probe bat compatible from the 96 kHz floor and the codec alone.
function probe(sampleRate: number, codec = 'pcm_s16le'): ProbeFixture {
  const lossy = codec === 'aac';
  return { sampleRate, codec, batCompatible: sampleRate >= 96000 && !lossy };
}

async function runProbe(selectedModels: string[], result: ProbeFixture) {
  vi.mocked(api.post).mockResolvedValueOnce({ channels: 1, warnings: [], ...result });
  render(StreamTestButton, {
    props: { url: 'rtsp://example.test/stream', models: MODELS, selectedModels },
  });
  await userEvent.setup().click(screen.getByRole('button', { name: new RegExp(BUTTON_KEY) }));
  await waitFor(() => expect(api.post).toHaveBeenCalled());
}

describe('StreamTestButton model minimum rate', () => {
  beforeEach(() => {
    vi.mocked(api.post).mockReset();
    vi.mocked(t).mockClear();
  });
  afterEach(cleanup);

  it('judges a 96 kHz stream compatible for the bat model alone', async () => {
    await runProbe(['bat'], probe(96000));

    expect(await screen.findByText(COMPATIBLE_KEY)).toBeInTheDocument();
    expect(screen.queryByText(MIN_RATE_KEY)).toBeNull();
  });

  it('judges a 96 kHz stream incompatible once BSG-BAT is enabled and says why', async () => {
    await runProbe(['bat', 'bsgbat'], probe(96000));

    expect(await screen.findByText(INCOMPATIBLE_KEY)).toBeInTheDocument();
    expect(screen.queryByText(COMPATIBLE_KEY)).toBeNull();
    expect(screen.getByText(MIN_RATE_KEY)).toBeInTheDocument();
    expect(t).toHaveBeenCalledWith(MIN_RATE_KEY, { rate: '192' });
  });

  it('judges a 192 kHz stream compatible for BSG-BAT', async () => {
    await runProbe(['bsgbat'], probe(192000));

    expect(await screen.findByText(COMPATIBLE_KEY)).toBeInTheDocument();
    expect(screen.queryByText(MIN_RATE_KEY)).toBeNull();
  });

  it('keeps a lossy stream incompatible at a rate above every minimum', async () => {
    await runProbe(['bsgbat'], probe(384000, 'aac'));

    expect(await screen.findByText(INCOMPATIBLE_KEY)).toBeInTheDocument();
    expect(screen.getByText(LOSSY_KEY)).toBeInTheDocument();
    expect(screen.queryByText(MIN_RATE_KEY)).toBeNull();
  });

  it('shows no bat verdict when only a bird model is enabled', async () => {
    await runProbe(['birdnet'], probe(48000));

    await waitFor(() => expect(screen.getByText(/48 kHz/)).toBeInTheDocument());
    expect(screen.queryByText(COMPATIBLE_KEY)).toBeNull();
    expect(screen.queryByText(INCOMPATIBLE_KEY)).toBeNull();
  });
});
