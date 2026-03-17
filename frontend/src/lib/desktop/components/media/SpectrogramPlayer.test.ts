/**
 * Tests for SpectrogramPlayer component.
 *
 * Mocks useAudioPlayback to return controlled state, avoiding the complexity
 * of mocking the Audio constructor. Mocks useDelayedLoading to control
 * spectrogram loading/error states independently.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createComponentTestFactory, screen, fireEvent } from '../../../../test/render-helpers';
import SpectrogramPlayer from './SpectrogramPlayer.svelte';

// --- Mock state that tests can mutate before each render ---

let mockAudioState = {
  isPlaying: false,
  currentTime: 0,
  duration: 0,
  progress: 0,
  isLoading: false,
  error: null as string | null,
  audioElement: null,
  gainValue: 0,
  filterFreq: 20,
  playbackSpeed: 1,
  audioContextAvailable: true,
  togglePlayPause: vi.fn(),
  seek: vi.fn(),
  setAudioUrl: vi.fn(),
  updateGain: vi.fn(),
  updateFilter: vi.fn(),
  setPlaybackSpeed: vi.fn(),
};

let mockLoadingState = {
  loading: false,
  showSpinner: false,
  error: false,
  setLoading: vi.fn(),
  setError: vi.fn(),
  reset: vi.fn(),
  cleanup: vi.fn(),
};

// Mock useAudioPlayback — return reactive getters backed by mockAudioState
vi.mock('$lib/utils/useAudioPlayback.svelte', () => ({
  useAudioPlayback: vi.fn(() => ({
    get isPlaying() {
      return mockAudioState.isPlaying;
    },
    get currentTime() {
      return mockAudioState.currentTime;
    },
    get duration() {
      return mockAudioState.duration;
    },
    get progress() {
      return mockAudioState.progress;
    },
    get isLoading() {
      return mockAudioState.isLoading;
    },
    get error() {
      return mockAudioState.error;
    },
    get audioElement() {
      return mockAudioState.audioElement;
    },
    get gainValue() {
      return mockAudioState.gainValue;
    },
    get filterFreq() {
      return mockAudioState.filterFreq;
    },
    get playbackSpeed() {
      return mockAudioState.playbackSpeed;
    },
    get audioContextAvailable() {
      return mockAudioState.audioContextAvailable;
    },
    togglePlayPause: mockAudioState.togglePlayPause,
    seek: mockAudioState.seek,
    setAudioUrl: mockAudioState.setAudioUrl,
    updateGain: mockAudioState.updateGain,
    updateFilter: mockAudioState.updateFilter,
    setPlaybackSpeed: mockAudioState.setPlaybackSpeed,
  })),
}));

// Mock useDelayedLoading — return reactive getters backed by mockLoadingState
vi.mock('$lib/utils/delayedLoading.svelte', () => ({
  useDelayedLoading: vi.fn(() => ({
    get loading() {
      return mockLoadingState.loading;
    },
    get showSpinner() {
      return mockLoadingState.showSpinner;
    },
    get error() {
      return mockLoadingState.error;
    },
    setLoading: mockLoadingState.setLoading,
    setError: mockLoadingState.setError,
    reset: mockLoadingState.reset,
    cleanup: mockLoadingState.cleanup,
  })),
}));

// Mock buildAppUrl — return input unchanged
vi.mock('$lib/utils/urlHelpers', () => ({
  buildAppUrl: vi.fn((path: string) => path),
}));

describe('SpectrogramPlayer', () => {
  const spectrogramTest = createComponentTestFactory(SpectrogramPlayer);

  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();

    // Reset audio mock state
    mockAudioState = {
      isPlaying: false,
      currentTime: 0,
      duration: 0,
      progress: 0,
      isLoading: false,
      error: null,
      audioElement: null,
      gainValue: 0,
      filterFreq: 20,
      playbackSpeed: 1,
      audioContextAvailable: true,
      togglePlayPause: vi.fn(),
      seek: vi.fn(),
      setAudioUrl: vi.fn(),
      updateGain: vi.fn(),
      updateFilter: vi.fn(),
      setPlaybackSpeed: vi.fn(),
    };

    // Reset loading mock state — default to loaded (not loading, no error)
    mockLoadingState = {
      loading: false,
      showSpinner: false,
      error: false,
      setLoading: vi.fn(),
      setError: vi.fn(),
      reset: vi.fn(),
      cleanup: vi.fn(),
    };
  });

  afterEach(() => {
    vi.clearAllTimers();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  // ---------------------------------------------------------------
  // 1. Renders spectrogram image with correct URL
  // ---------------------------------------------------------------
  it('renders spectrogram image with correct URL', () => {
    spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
    });

    const img = screen.getByAltText('components.audio.spectrogramAlt');
    expect(img).toBeInTheDocument();
    expect(img.getAttribute('src')).toContain('/api/v2/spectrogram/test-123');
    expect(img.getAttribute('src')).toContain('size=md');
    expect(img.getAttribute('src')).toContain('raw=true');
  });

  // ---------------------------------------------------------------
  // 2. Shows play button overlay
  // ---------------------------------------------------------------
  it('shows play button overlay', () => {
    spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
    });

    const playBtn = screen.getByLabelText('media.audio.play');
    expect(playBtn).toBeInTheDocument();
    expect(playBtn.tagName).toBe('BUTTON');
  });

  // ---------------------------------------------------------------
  // 3. Shows loading spinner when spectrogram loading
  // ---------------------------------------------------------------
  it('shows loading spinner when spectrogram is loading', () => {
    mockLoadingState.showSpinner = true;
    mockLoadingState.loading = true;

    spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
    });

    const spinner = screen.getByRole('status');
    expect(spinner).toBeInTheDocument();
    expect(spinner).toHaveAttribute('aria-label', 'components.audio.spectrogramLoadingAria');
  });

  // ---------------------------------------------------------------
  // 4. Shows error state when spectrogram fails after retries
  // ---------------------------------------------------------------
  it('shows error state when spectrogram fails after retries', () => {
    mockLoadingState.error = true;

    spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
    });

    // When error is true, the spectrogram image should not be rendered
    expect(screen.queryByAltText('components.audio.spectrogramAlt')).not.toBeInTheDocument();

    // The play button should still be visible even when spectrogram errors
    expect(screen.queryByLabelText('media.audio.play')).toBeInTheDocument();
  });

  // ---------------------------------------------------------------
  // 5. Play button toggles play/pause
  // ---------------------------------------------------------------
  it('play button calls togglePlayPause on click', async () => {
    spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
    });

    const playBtn = screen.getByLabelText('media.audio.play');
    await fireEvent.click(playBtn);

    expect(mockAudioState.togglePlayPause).toHaveBeenCalledTimes(1);
  });

  // ---------------------------------------------------------------
  // 6. Progress bar appears during playback
  // ---------------------------------------------------------------
  it('shows progress bar when progress is greater than zero', () => {
    mockAudioState.progress = 42;

    const { container } = spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
    });

    const progressFill = container.querySelector('.progress-fill');
    expect(progressFill).toBeInTheDocument();
    // Svelte's style:width directive sets the inline style attribute
    expect((progressFill as HTMLElement).style.width).toBe('42%');
  });

  it('does not show progress bar when progress is zero', () => {
    mockAudioState.progress = 0;

    const { container } = spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
    });

    const progressTrack = container.querySelector('.progress-track');
    expect(progressTrack).not.toBeInTheDocument();
  });

  // ---------------------------------------------------------------
  // 7. Accessibility: play button has correct aria-label
  // ---------------------------------------------------------------
  it('play button has Play aria-label when paused', () => {
    mockAudioState.isPlaying = false;

    spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
    });

    const btn = screen.getByRole('button');
    expect(btn).toHaveAttribute('aria-label', 'media.audio.play');
  });

  it('play button has Pause aria-label when playing', () => {
    mockAudioState.isPlaying = true;

    spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
    });

    const btn = screen.getByRole('button');
    expect(btn).toHaveAttribute('aria-label', 'media.audio.pause');
  });

  // ---------------------------------------------------------------
  // Additional: encodes special characters in detection ID
  // ---------------------------------------------------------------
  it('encodes special characters in detectionId for spectrogram URL', () => {
    spectrogramTest.render({
      audioUrl: '/api/v2/audio/test/with spaces',
      detectionId: 'test/with spaces',
    });

    const img = screen.getByAltText('components.audio.spectrogramAlt');
    const src = img.getAttribute('src') ?? '';
    expect(src).toContain(encodeURIComponent('test/with spaces'));
  });

  // ---------------------------------------------------------------
  // Additional: uses spectrogramSize prop
  // ---------------------------------------------------------------
  it('passes spectrogramSize to spectrogram URL', () => {
    spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
      spectrogramSize: 'xl',
    });

    const img = screen.getByAltText('components.audio.spectrogramAlt');
    expect(img.getAttribute('src')).toContain('size=xl');
  });

  // ---------------------------------------------------------------
  // Additional: play button disabled when audio is loading
  // ---------------------------------------------------------------
  it('disables play button when audio is loading', () => {
    mockAudioState.isLoading = true;

    spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
    });

    const btn = screen.getByRole('button');
    expect(btn).toBeDisabled();
  });

  // ---------------------------------------------------------------
  // Additional: shows audio error message
  // ---------------------------------------------------------------
  it('shows audio error message when audio.error is set', () => {
    mockAudioState.error = 'media.audio.error';

    spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
    });

    const alert = screen.getByRole('alert');
    expect(alert).toBeInTheDocument();
    expect(alert).toHaveTextContent('media.audio.error');
  });

  // ---------------------------------------------------------------
  // Additional: has correct role=group with aria-label
  // ---------------------------------------------------------------
  it('has role="group" with correct aria-label', () => {
    spectrogramTest.render({
      audioUrl: '/api/v2/audio/test-123',
      detectionId: 'test-123',
    });

    const group = screen.getByRole('group');
    expect(group).toBeInTheDocument();
    expect(group).toHaveAttribute('aria-label', 'media.audio.player');
  });
});
