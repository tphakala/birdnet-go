import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import ReviewModal from './ReviewModal.svelte';
import type { Detection } from '$lib/types/detection.types';

function createDetection(overrides: Partial<Detection> = {}): Detection {
  return {
    id: 321,
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
    clipName: 'clip_321.wav',
    ...overrides,
  };
}

describe('ReviewModal media section', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders the kept spectrogram image, without a player, for a spectrogram-only detection', () => {
    render(ReviewModal, {
      props: {
        isOpen: true,
        detection: createDetection({ clipName: '', spectrogramOnly: true }),
        onClose: vi.fn(),
      },
    });

    const img = document.querySelector('img.spectrogram-img');
    expect(img).not.toBeNull();
    expect(img?.getAttribute('src')).toContain('/api/v2/spectrogram/321?size=lg&raw=false');
    expect(screen.queryByRole('button', { name: /play/i })).toBeNull();
    expect(document.querySelector('a[download]')).toBeNull();
  });

  it('renders the player, not the image-only view, for a detection with audio', () => {
    render(ReviewModal, {
      props: { isOpen: true, detection: createDetection(), onClose: vi.fn() },
    });

    // Positive control for the play-button selector used by the image-only test.
    expect(screen.getAllByRole('button', { name: /play/i }).length).toBeGreaterThan(0);
  });

  it('renders no media section when the detection has neither audio nor a kept image', () => {
    render(ReviewModal, {
      props: {
        isOpen: true,
        detection: createDetection({ clipName: '' }),
        onClose: vi.fn(),
      },
    });

    expect(document.querySelector('img.spectrogram-img')).toBeNull();
    expect(screen.queryByRole('button', { name: /play/i })).toBeNull();
  });
});
