import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createComponentTestFactory, screen } from '../../../../test/render-helpers';
import StreamChannelControls from './StreamChannelControls.svelte';

// The global i18n mock returns the key when no translation is registered, so the
// rendered text equals the translation key. Assert on keys to detect which parts
// of the UI rendered.
const LABEL_KEY = 'settings.audio.streams.channelMode.label';
const DETECT_KEY = 'settings.audio.streams.channelMode.detectBest';
const MONO_KEY = 'settings.audio.streams.channelMode.monoNoSelection';
const DOWNMIX_WARN_KEY = 'settings.audio.streams.channelMode.downmixWarning';
const UNTESTED_KEY = 'settings.audio.streams.format.untested';
const FORMAT_MONO_KEY = 'settings.audio.streams.format.mono';

const factory = createComponentTestFactory(StreamChannelControls);

function baseProps(overrides: Record<string, unknown> = {}) {
  return {
    channelMode: 'downmix',
    channels: 0,
    analyzeUrl: 'rtsp://example/stream',
    isAnalyzing: false,
    analysisResult: null,
    analysisError: null,
    onChange: vi.fn(),
    onAnalyze: vi.fn(),
    ...overrides,
  };
}

describe('StreamChannelControls', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('prompts to test and hides the selector when the channel count is unknown', () => {
    factory.render(baseProps({ channels: 0, channelMode: 'downmix' }));
    expect(screen.getByText(UNTESTED_KEY)).toBeInTheDocument();
    expect(screen.queryByText(LABEL_KEY)).not.toBeInTheDocument();
    expect(screen.queryByText(DETECT_KEY)).not.toBeInTheDocument();
  });

  it('shows a single-channel note and hides the selector for a mono source', () => {
    factory.render(baseProps({ channels: 1, channelMode: 'downmix' }));
    expect(screen.getByText(FORMAT_MONO_KEY)).toBeInTheDocument();
    expect(screen.getByText(MONO_KEY)).toBeInTheDocument();
    expect(screen.queryByText(LABEL_KEY)).not.toBeInTheDocument();
    expect(screen.queryByText(DETECT_KEY)).not.toBeInTheDocument();
  });

  it('shows the selector, detect button and downmix warning for a multi-channel source', () => {
    factory.render(baseProps({ channels: 2, channelMode: 'downmix' }));
    expect(screen.getByText(LABEL_KEY)).toBeInTheDocument();
    expect(screen.getByText(DETECT_KEY)).toBeInTheDocument();
    expect(screen.getByText(DOWNMIX_WARN_KEY)).toBeInTheDocument();
    expect(screen.queryByText(MONO_KEY)).not.toBeInTheDocument();
  });

  it('keeps the selector visible for a left-configured stream even when offline (unknown channels)', () => {
    factory.render(baseProps({ channels: 0, channelMode: 'left' }));
    expect(screen.getByText(LABEL_KEY)).toBeInTheDocument();
    // No live probe, so the analysis (detect button) stays hidden.
    expect(screen.queryByText(DETECT_KEY)).not.toBeInTheDocument();
    // Format is still unknown without a probe.
    expect(screen.getByText(UNTESTED_KEY)).toBeInTheDocument();
  });
});
