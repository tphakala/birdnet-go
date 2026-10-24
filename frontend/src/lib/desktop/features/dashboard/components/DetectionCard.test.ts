import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent, screen } from '@testing-library/svelte';
import DetectionCard from './DetectionCard.svelte';
import type { Detection } from '$lib/types/detection.types';

function createDetection(overrides: Partial<Detection> = {}): Detection {
  return {
    id: 654,
    date: '2024-01-15',
    time: '10:30:00',
    beginTime: '2024-01-15T10:30:00Z',
    endTime: '2024-01-15T10:30:03Z',
    speciesCode: 'amerob',
    commonName: 'American Robin',
    scientificName: 'Turdus migratorius',
    confidence: 0.85,
    verified: 'unverified',
    locked: false,
    clipName: '',
    ...overrides,
  };
}

describe('DetectionCard spectrogram-only detection', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows the kept spectrogram image without play controls', () => {
    const { container } = render(DetectionCard, {
      props: { detection: createDetection({ spectrogramOnly: true }) },
    });

    const img = container.querySelector('img.spectrogram-img');
    expect(img).not.toBeNull();
    expect(img?.getAttribute('src')).toContain('/api/v2/spectrogram/654?size=md&raw=true');
    expect(container.querySelector('.detection-card-inner.compact')).toBeNull();
    // Audio-only controls stay hidden: no play overlay and no audio settings button.
    expect(screen.queryByRole('button', { name: /play/i })).toBeNull();
  });

  it('shows the play control for a detection with audio', () => {
    render(DetectionCard, { props: { detection: createDetection({ clipName: 'clip_654.wav' }) } });

    // Positive control for the play-button selector used by the image-only test.
    expect(screen.getAllByRole('button', { name: /play/i }).length).toBeGreaterThan(0);
  });

  it('shows the unavailable state when the kept image fails to load', async () => {
    const { container } = render(DetectionCard, {
      props: { detection: createDetection({ spectrogramOnly: true }) },
    });

    const img = container.querySelector('img.spectrogram-img');
    expect(img).not.toBeNull();
    if (img) await fireEvent.error(img);

    expect(container.querySelector('img.spectrogram-img')).toBeNull();
    expect(container.querySelector('.spectrogram-unavailable')).not.toBeNull();
  });

  it('keeps the compact layout when there is neither audio nor a kept image', () => {
    const { container } = render(DetectionCard, { props: { detection: createDetection() } });

    expect(container.querySelector('.detection-card-inner.compact')).not.toBeNull();
    expect(container.querySelector('img.spectrogram-img')).toBeNull();
  });
});
