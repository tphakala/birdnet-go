/* eslint-disable @typescript-eslint/no-unnecessary-condition */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  createComponentTestFactory,
  screen,
  fireEvent,
  waitFor,
} from '../../../../test/render-helpers';
import userEvent from '@testing-library/user-event';
import AudioPlayer from './AudioPlayer.svelte';

describe('AudioPlayer', () => {
  let mockPlay: ReturnType<typeof vi.fn>;
  let mockPause: ReturnType<typeof vi.fn>;
  const eventHandlers: Record<string, EventListener[]> = {};
  const audioPlayerTest = createComponentTestFactory(AudioPlayer);

  beforeEach(() => {
    vi.useFakeTimers();

    // Mock HTMLMediaElement methods
    mockPlay = vi.fn().mockResolvedValue(undefined);
    mockPause = vi.fn();

    window.HTMLMediaElement.prototype.play = mockPlay;
    window.HTMLMediaElement.prototype.pause = mockPause;

    // Mock addEventListener to store handlers
    window.HTMLMediaElement.prototype.addEventListener = vi.fn(
      (event: string, handler: EventListener) => {
        if (!eventHandlers[event]) {
          eventHandlers[event] = [];
        }
        eventHandlers[event].push(handler);
      }
    );

    window.HTMLMediaElement.prototype.removeEventListener = vi.fn(
      (event: string, handler: EventListener) => {
        if (eventHandlers[event]) {
          const index = eventHandlers[event].indexOf(handler);
          if (index > -1) {
            eventHandlers[event].splice(index, 1);
          }
        }
      }
    );

    // Mock audio properties
    Object.defineProperty(window.HTMLMediaElement.prototype, 'paused', {
      configurable: true,
      get: vi.fn().mockReturnValue(true),
    });

    Object.defineProperty(window.HTMLMediaElement.prototype, 'duration', {
      configurable: true,
      get: vi.fn().mockReturnValue(120), // 2 minutes
    });

    Object.defineProperty(window.HTMLMediaElement.prototype, 'currentTime', {
      configurable: true,
      get: vi.fn().mockReturnValue(0),
      set: vi.fn(),
    });
  });

  afterEach(() => {
    vi.clearAllTimers();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('renders with audio URL', () => {
    const { container } = audioPlayerTest.render({
      audioUrl: '/audio/test.mp3',
      detectionId: 'test-123',
    });

    const audio = container.querySelector('audio');
    expect(audio).toBeInTheDocument();
    expect(audio).toHaveAttribute('src', '/audio/test.mp3');
  });

  it('renders with spectrogram', () => {
    audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
        showSpectrogram: true,
      },
    });

    const img = screen.getByAltText('Audio spectrogram');
    expect(img).toBeInTheDocument();
    expect(img).toHaveAttribute('src', '/api/v2/spectrogram/test-123');
  });

  it('generates spectrogram URL from detectionId', () => {
    audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: '123',
        width: 600,
        showSpectrogram: true,
      },
    });

    const img = screen.getByAltText('Audio spectrogram');
    expect(img).toHaveAttribute('src', '/api/v2/spectrogram/123?width=600');
  });

  it('shows loading state initially', () => {
    const { container } = audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
        showSpectrogram: true,
      },
    });

    const loadingSpinner = container.querySelector('.loading.loading-spinner');
    expect(loadingSpinner).toBeInTheDocument();
  });

  it('shows play button when paused', () => {
    audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
      },
    });

    const button = screen.getByLabelText('Play');
    expect(button).toBeInTheDocument();
    expect(button).toHaveAttribute('aria-pressed', 'false');
  });

  it('toggles play/pause on button click', async () => {
    audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
      },
    });

    const button = screen.getByLabelText('Play');
    await fireEvent.click(button);

    expect(mockPlay).toHaveBeenCalledTimes(1);
  });

  it('shows pause button when playing', async () => {
    // Mock playing state
    Object.defineProperty(window.HTMLMediaElement.prototype, 'paused', {
      configurable: true,
      get: vi.fn().mockReturnValue(false),
    });

    const { container } = audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
      },
    });

    // Simulate play event
    const audio = container.querySelector('audio');
    if (eventHandlers['play'] && audio) {
      eventHandlers['play'].forEach(handler => handler.call(audio, new Event('play')));
    }

    await waitFor(() => {
      const button = screen.getByLabelText('Pause');
      expect(button).toBeInTheDocument();
      expect(button).toHaveAttribute('aria-pressed', 'true');
    });
  });

  it('formats time correctly', async () => {
    const { container } = audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
      },
    });

    // Simulate loadedmetadata event
    const audio = container.querySelector('audio');
    if (eventHandlers['loadedmetadata'] && audio) {
      eventHandlers['loadedmetadata'].forEach(handler =>
        handler.call(audio, new Event('loadedmetadata'))
      );
    }

    await waitFor(() => {
      expect(screen.getByText('0:00 / 2:00')).toBeInTheDocument();
    });
  });

  it('updates progress during playback', async () => {
    let getCurrentTime = 0;
    Object.defineProperty(window.HTMLMediaElement.prototype, 'currentTime', {
      configurable: true,
      get: () => getCurrentTime,
      set: value => {
        getCurrentTime = value;
      },
    });

    const { container } = audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
      },
    });

    // Simulate play
    const audio = container.querySelector('audio');
    if (eventHandlers['play'] && audio) {
      eventHandlers['play'].forEach(handler => handler.call(audio, new Event('play')));
    }

    // Simulate time progress
    getCurrentTime = 30; // 30 seconds

    vi.advanceTimersByTime(100);

    await waitFor(() => {
      expect(screen.getByText('0:30 / 2:00')).toBeInTheDocument();
    });
  });

  it('seeks when clicking on progress bar', async () => {
    const setCurrentTime = vi.fn();
    Object.defineProperty(window.HTMLMediaElement.prototype, 'currentTime', {
      configurable: true,
      get: () => 0,
      set: setCurrentTime,
    });

    const { container } = audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
      },
    });

    // Load metadata first
    const audio = container.querySelector('audio');
    if (eventHandlers['loadedmetadata'] && audio) {
      eventHandlers['loadedmetadata'].forEach(handler =>
        handler.call(audio, new Event('loadedmetadata'))
      );
    }

    const progressBar = screen.getByRole('slider');

    // Mock getBoundingClientRect
    progressBar.getBoundingClientRect = vi.fn(() => ({
      left: 0,
      right: 200,
      top: 0,
      bottom: 20,
      width: 200,
      height: 20,
      x: 0,
      y: 0,
      toJSON: () => {},
    }));

    // Click at 50% of progress bar
    await fireEvent.click(progressBar, {
      clientX: 100, // 50% of 200px width
    });

    expect(setCurrentTime).toHaveBeenCalledWith(60); // 50% of 120 seconds
  });

  it('seeks when clicking on spectrogram', async () => {
    const setCurrentTime = vi.fn();
    Object.defineProperty(window.HTMLMediaElement.prototype, 'currentTime', {
      configurable: true,
      get: () => 0,
      set: setCurrentTime,
    });

    const { container } = audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        spectrogramUrl: '/spectrogram/test.png',
      },
    });

    // Load metadata
    const audio = container.querySelector('audio');
    if (eventHandlers['loadedmetadata'] && audio) {
      eventHandlers['loadedmetadata'].forEach(handler =>
        handler.call(audio, new Event('loadedmetadata'))
      );
    }

    await waitFor(() => {
      const spectrogramContainer = container.querySelector('.audio-player > div');
      expect(spectrogramContainer).toBeInTheDocument();
    });

    const spectrogramContainer = container.querySelector('.audio-player > div') as HTMLElement;

    // Mock getBoundingClientRect
    spectrogramContainer.getBoundingClientRect = vi.fn(() => ({
      left: 0,
      right: 400,
      top: 0,
      bottom: 200,
      width: 400,
      height: 200,
      x: 0,
      y: 0,
      toJSON: () => {},
    }));

    // Click at 25% of spectrogram
    await fireEvent.click(spectrogramContainer, {
      clientX: 100, // 25% of 400px width
    });

    expect(setCurrentTime).toHaveBeenCalledWith(30); // 25% of 120 seconds
  });

  it('handles keyboard controls', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const setCurrentTime = vi.fn();
    let currentTime = 60;

    Object.defineProperty(window.HTMLMediaElement.prototype, 'currentTime', {
      configurable: true,
      get: () => currentTime,
      set: value => {
        currentTime = value;
        setCurrentTime(value);
      },
    });

    const { container } = audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
      },
    });

    // Load metadata first so duration is set
    const audio = container.querySelector('audio');
    if (eventHandlers['loadedmetadata'] && audio) {
      eventHandlers['loadedmetadata'].forEach(handler =>
        handler.call(audio, new Event('loadedmetadata'))
      );
    }

    await waitFor(() => {
      const progressBar = screen.getByRole('slider');
      expect(progressBar).toHaveAttribute('aria-valuemax', '120');
    });

    const progressBar = screen.getByRole('slider');
    progressBar.focus();

    // Test arrow left (rewind)
    await user.keyboard('{ArrowLeft}');
    expect(setCurrentTime).toHaveBeenCalledWith(55); // 60 - 5

    // Test arrow right (forward)
    currentTime = 55; // Update current time after previous action
    await user.keyboard('{ArrowRight}');
    expect(setCurrentTime).toHaveBeenCalledWith(60); // 55 + 5

    // Test space (play/pause)
    await user.keyboard(' ');
    expect(mockPlay).toHaveBeenCalled();
  });

  it('shows download button by default', () => {
    audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
      },
    });

    const downloadLink = screen.getByLabelText('Download audio file');
    expect(downloadLink).toBeInTheDocument();
    expect(downloadLink).toHaveAttribute('href', '/audio/test.mp3');
    expect(downloadLink).toHaveAttribute('download');
  });

  it('hides download button when disabled', () => {
    audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        showDownload: false,
      },
    });

    expect(screen.queryByLabelText('Download audio file')).not.toBeInTheDocument();
  });

  it('hides spectrogram when disabled', () => {
    audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        spectrogramUrl: '/spectrogram/test.png',
        showSpectrogram: false,
      },
    });

    expect(screen.queryByAltText('Audio spectrogram')).not.toBeInTheDocument();
  });

  it('calls event callbacks', async () => {
    const onPlay = vi.fn();
    const onPause = vi.fn();
    const onEnded = vi.fn();
    const onTimeUpdate = vi.fn();

    const { container } = audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        onPlay,
        onPause,
        onEnded,
        onTimeUpdate,
      },
    });

    const audio = container.querySelector('audio');

    // Test play event
    if (eventHandlers['play'] && audio) {
      eventHandlers['play'].forEach(handler => handler.call(audio, new Event('play')));
    }
    expect(onPlay).toHaveBeenCalledTimes(1);

    // Advance timer to trigger time update
    vi.advanceTimersByTime(100);
    expect(onTimeUpdate).toHaveBeenCalledWith(0, 120);

    // Test pause event
    if (eventHandlers['pause'] && audio) {
      eventHandlers['pause'].forEach(handler => handler.call(audio, new Event('pause')));
    }
    expect(onPause).toHaveBeenCalledTimes(1);

    // Test ended event
    if (eventHandlers['ended'] && audio) {
      eventHandlers['ended'].forEach(handler => handler.call(audio, new Event('ended')));
    }
    expect(onEnded).toHaveBeenCalledTimes(1);
  });

  it('handles audio error', async () => {
    const { container } = audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        spectrogramUrl: '/spectrogram/test.png',
      },
    });

    const audio = container.querySelector('audio');
    if (eventHandlers['error'] && audio) {
      eventHandlers['error'].forEach(handler => handler.call(audio, new Event('error')));
    }

    await waitFor(() => {
      expect(screen.getByText('Failed to load audio')).toBeInTheDocument();
    });

    // Play button should be disabled
    const playButton = screen.getByLabelText('Play');
    expect(playButton).toBeDisabled();
  });

  it('handles spectrogram error', async () => {
    audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        spectrogramUrl: '/spectrogram/test.png',
      },
    });

    const img = screen.getByAltText('Audio spectrogram');
    fireEvent.error(img);

    await waitFor(() => {
      expect(screen.getByText('Failed to load audio')).toBeInTheDocument();
    });
  });

  it('autoplays when enabled', () => {
    audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        autoPlay: true,
      },
    });

    expect(mockPlay).toHaveBeenCalledTimes(1);
  });

  it('applies custom classes', () => {
    const { container } = audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        spectrogramUrl: '/spectrogram/test.png',
        className: 'custom-player',
        spectrogramClassName: 'custom-spectrogram',
        controlsClassName: 'custom-controls',
      },
    });

    const player = container.querySelector('.audio-player');
    expect(player).toHaveClass('custom-player');

    const spectrogram = container.querySelector('.audio-player > div');
    expect(spectrogram).toHaveClass('custom-spectrogram');

    const controls = container.querySelector('.audio-player > div:last-child');
    expect(controls).toHaveClass('custom-controls');
  });

  it('cleans up interval on unmount', async () => {
    const clearIntervalSpy = vi.spyOn(window, 'clearInterval');

    const { container, unmount } = audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
      },
    });

    // Start playing
    const audio = container.querySelector('audio');
    if (eventHandlers['play'] && audio) {
      eventHandlers['play'].forEach(handler => handler.call(audio, new Event('play')));
    }

    unmount();

    expect(clearIntervalSpy).toHaveBeenCalled();
  });

  it('shows position indicator during playback', async () => {
    let currentTime = 0;
    Object.defineProperty(window.HTMLMediaElement.prototype, 'currentTime', {
      configurable: true,
      get: () => currentTime,
      set: value => {
        currentTime = value;
      },
    });

    const { container } = audioPlayerTest.render({
      props: {
        audioUrl: '/audio/test.mp3',
        spectrogramUrl: '/spectrogram/test.png',
      },
    });

    // Load metadata
    const audio = container.querySelector('audio');
    if (eventHandlers['loadedmetadata'] && audio) {
      eventHandlers['loadedmetadata'].forEach(handler =>
        handler.call(audio, new Event('loadedmetadata'))
      );
    }

    // Start playing
    if (eventHandlers['play'] && audio) {
      eventHandlers['play'].forEach(handler => handler.call(audio, new Event('play')));
    }

    // Progress to middle
    currentTime = 60;
    vi.advanceTimersByTime(100);

    await waitFor(() => {
      const indicator = container.querySelector('.absolute.bottom-0.top-0.w-0\\.5') as HTMLElement;
      expect(indicator).toHaveStyle({ left: '50%', opacity: '0.7' });
    });
  });
});
