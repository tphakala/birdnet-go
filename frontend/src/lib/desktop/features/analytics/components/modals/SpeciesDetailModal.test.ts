import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup } from '@testing-library/svelte';
import { createComponentTestFactory, screen } from '../../../../../../test/render-helpers';
import userEvent from '@testing-library/user-event';
import SpeciesDetailModal from './SpeciesDetailModal.svelte';

// Mock the i18n module
vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => {
    const translations: Record<string, string> = {
      'common.aria.closeModal': 'Close modal',
      'common.close': 'Close',
      'analytics.species.card.detections': 'Detections',
      'analytics.species.card.confidence': 'Confidence',
      'analytics.species.headers.firstDetected': 'First detected',
      'analytics.species.headers.lastDetected': 'Last detected',
    };
    // eslint-disable-next-line security/detect-object-injection -- Test mock with controlled translation data
    return translations[key] ?? key;
  }),
}));

describe('SpeciesDetailModal', () => {
  let user: ReturnType<typeof userEvent.setup>;
  const modalTest = createComponentTestFactory(SpeciesDetailModal);

  const mockSpecies = {
    common_name: 'House Sparrow',
    scientific_name: 'Passer domesticus',
    count: 42,
    avg_confidence: 0.85,
    max_confidence: 0.95,
    first_heard: '2024-01-15T10:30:00',
    last_heard: '2024-01-20T14:45:00',
  };

  beforeEach(() => {
    vi.clearAllMocks();
    user = userEvent.setup();
  });

  afterEach(() => {
    cleanup();
  });

  it('renders when isOpen is true and species is provided', () => {
    modalTest.render({
      props: {
        isOpen: true,
        species: mockSpecies,
      },
    });

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByText('House Sparrow')).toBeInTheDocument();
  });

  it('does not render when isOpen is false', () => {
    modalTest.render({
      props: {
        isOpen: false,
        species: mockSpecies,
      },
    });

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('calls onClose when close button is clicked', async () => {
    const onClose = vi.fn();

    modalTest.render({
      props: {
        isOpen: true,
        species: mockSpecies,
        onClose,
      },
    });

    const closeButton = screen.getByLabelText('Close modal');
    await user.click(closeButton);

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes on Escape key press', async () => {
    const onClose = vi.fn();

    modalTest.render({
      props: {
        isOpen: true,
        species: mockSpecies,
        onClose,
      },
    });

    await user.keyboard('{Escape}');

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes when clicking outside the modal content', async () => {
    const onClose = vi.fn();

    const { container } = modalTest.render({
      props: {
        isOpen: true,
        species: mockSpecies,
        onClose,
      },
    });

    // Click on the overlay (the outer dialog element)
    const overlay = container.querySelector('[role="dialog"]');
    if (overlay) {
      await user.click(overlay);
    }

    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
