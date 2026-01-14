import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup } from '@testing-library/svelte';
import { createComponentTestFactory, screen } from '../../../../test/render-helpers';
import userEvent from '@testing-library/user-event';
import MobileAudioPlayer from './MobileAudioPlayer.svelte';

// Mock the i18n module
vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => {
    const translations: Record<string, string> = {
      'common.aria.closeModal': 'Close modal',
    };
    // eslint-disable-next-line security/detect-object-injection -- Test mock with controlled translation data
    return translations[key] ?? key;
  }),
}));

// Mock AudioPlayer to avoid complex dependencies
vi.mock('./AudioPlayer.svelte', () => ({
  default: vi.fn(),
}));

describe('MobileAudioPlayer', () => {
  let user: ReturnType<typeof userEvent.setup>;
  const mobilePlayerTest = createComponentTestFactory(MobileAudioPlayer);

  beforeEach(() => {
    vi.clearAllMocks();
    user = userEvent.setup();
  });

  afterEach(() => {
    cleanup();
  });

  it('renders the modal dialog', () => {
    mobilePlayerTest.render({
      props: {
        audioUrl: '/api/v2/audio/123',
        speciesName: 'House Sparrow',
      },
    });

    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('displays species name in header', () => {
    mobilePlayerTest.render({
      props: {
        audioUrl: '/api/v2/audio/123',
        speciesName: 'House Sparrow',
      },
    });

    expect(screen.getByText('House Sparrow')).toBeInTheDocument();
  });

  it('calls onClose when close button is clicked', async () => {
    const onClose = vi.fn();

    mobilePlayerTest.render({
      props: {
        audioUrl: '/api/v2/audio/123',
        speciesName: 'House Sparrow',
        onClose,
      },
    });

    const closeButton = screen.getByLabelText('Close modal');
    await user.click(closeButton);

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes on Escape key press', async () => {
    const onClose = vi.fn();

    mobilePlayerTest.render({
      props: {
        audioUrl: '/api/v2/audio/123',
        speciesName: 'House Sparrow',
        onClose,
      },
    });

    await user.keyboard('{Escape}');

    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
