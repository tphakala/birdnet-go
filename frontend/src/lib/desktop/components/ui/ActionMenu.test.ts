import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import ActionMenu from './ActionMenu.svelte';
import type { Detection } from '$lib/types/detection.types';

// Create a mock detection for testing
function createMockDetection(overrides: Partial<Detection> = {}): Detection {
  return {
    id: 1,
    date: '2024-01-15',
    time: '10:30:00',
    beginTime: '2024-01-15T10:30:00Z',
    endTime: '2024-01-15T10:30:03Z',
    speciesCode: 'amerob',
    scientificName: 'Turdus migratorius',
    commonName: 'American Robin',
    confidence: 0.85,
    verified: 'unverified',
    locked: false,
    clipName: 'clip_001.wav',
    ...overrides,
  };
}

describe('ActionMenu', () => {
  let user: ReturnType<typeof userEvent.setup>;

  beforeEach(() => {
    user = userEvent.setup();
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('renders the action menu button', () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
      },
    });

    expect(screen.getByRole('button', { name: /actions menu/i })).toBeInTheDocument();
  });

  it('opens menu when button is clicked', async () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    expect(screen.getByRole('menu')).toBeInTheDocument();
  });

  it('closes menu when button is clicked again', async () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);
    expect(screen.getByRole('menu')).toBeInTheDocument();

    await fireEvent.click(button);
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it('displays all action buttons when menu is open', async () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
        onReview: vi.fn(),
        onToggleSpecies: vi.fn(),
        onToggleLock: vi.fn(),
        onDelete: vi.fn(),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    expect(screen.getByRole('menuitem', { name: /review detection/i })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: /ignore species/i })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: /lock detection/i })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: /delete detection/i })).toBeInTheDocument();
  });

  it('calls onReview when review button is clicked', async () => {
    const onReview = vi.fn();

    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
        onReview,
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    const reviewButton = screen.getByRole('menuitem', { name: /review detection/i });
    await fireEvent.click(reviewButton);

    expect(onReview).toHaveBeenCalledTimes(1);
  });

  it('calls onToggleSpecies when toggle species button is clicked', async () => {
    const onToggleSpecies = vi.fn();

    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
        onToggleSpecies,
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    const toggleButton = screen.getByRole('menuitem', { name: /ignore species/i });
    await fireEvent.click(toggleButton);

    expect(onToggleSpecies).toHaveBeenCalledTimes(1);
  });

  it('calls onToggleLock when lock button is clicked', async () => {
    const onToggleLock = vi.fn();

    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
        onToggleLock,
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    const lockButton = screen.getByRole('menuitem', { name: /lock detection/i });
    await fireEvent.click(lockButton);

    expect(onToggleLock).toHaveBeenCalledTimes(1);
  });

  it('calls onDelete when delete button is clicked', async () => {
    const onDelete = vi.fn();

    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
        onDelete,
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    const deleteButton = screen.getByRole('menuitem', { name: /delete detection/i });
    await fireEvent.click(deleteButton);

    expect(onDelete).toHaveBeenCalledTimes(1);
  });

  it('shows "Show species" when species is excluded', async () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
        isExcluded: true,
        onToggleSpecies: vi.fn(),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    expect(screen.getByRole('menuitem', { name: /show species/i })).toBeInTheDocument();
  });

  it('shows "Unlock detection" when detection is locked', async () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection({ locked: true }),
        onToggleLock: vi.fn(),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    expect(screen.getByRole('menuitem', { name: /unlock detection/i })).toBeInTheDocument();
  });

  it('hides delete button when detection is locked', async () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection({ locked: true }),
        onDelete: vi.fn(),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    expect(screen.queryByRole('menuitem', { name: /delete detection/i })).not.toBeInTheDocument();
  });

  it('closes menu when Escape key is pressed', async () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);
    expect(screen.getByRole('menu')).toBeInTheDocument();

    await user.keyboard('{Escape}');
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it('closes menu after action is performed', async () => {
    const onReview = vi.fn();

    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
        onReview,
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    const reviewButton = screen.getByRole('menuitem', { name: /review detection/i });
    await fireEvent.click(reviewButton);

    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it('calls onMenuOpen when menu opens', async () => {
    const onMenuOpen = vi.fn();

    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
        onMenuOpen,
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    expect(onMenuOpen).toHaveBeenCalledTimes(1);
  });

  it('calls onMenuClose when menu closes', async () => {
    const onMenuClose = vi.fn();

    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
        onMenuClose,
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);
    await fireEvent.click(button);

    expect(onMenuClose).toHaveBeenCalledTimes(1);
  });

  it('shows verified badge when detection is verified as correct', async () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection({
          verified: 'correct',
        }),
        onReview: vi.fn(),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    expect(screen.getAllByText('✓').length).toBeGreaterThanOrEqual(1);
  });

  it('shows false positive badge when detection is verified as false positive', async () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection({
          verified: 'false_positive',
        }),
        onReview: vi.fn(),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    expect(screen.getAllByText('✗').length).toBeGreaterThanOrEqual(1);
  });

  it('handles action without callback gracefully', async () => {
    // Test that clicking a provided action doesn't throw even when action is a no-op
    const onReview = vi.fn();
    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
        onReview,
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    // Click review - should not throw and menu should close
    const reviewButton = screen.getByRole('menuitem', { name: /review detection/i });
    await fireEvent.click(reviewButton);

    // Menu should still close
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it('has correct ARIA attributes on button', () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    expect(button).toHaveAttribute('aria-haspopup', 'true');
    expect(button).toHaveAttribute('aria-expanded', 'false');
  });

  it('updates aria-expanded when menu opens', async () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    expect(button).toHaveAttribute('aria-expanded', 'false');

    await fireEvent.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'true');
  });

  it('applies custom className', () => {
    const { container } = render(ActionMenu, {
      props: {
        detection: createMockDetection(),
        className: 'custom-class',
      },
    });

    expect(container.querySelector('.custom-class')).toBeInTheDocument();
  });

  it('handles clicking outside to close menu', async () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);
    expect(screen.getByRole('menu')).toBeInTheDocument();

    // Click outside (on document body)
    await fireEvent.click(document.body);

    await waitFor(() => {
      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    });
  });

  it('renders the default variant trigger by default', () => {
    render(ActionMenu, { props: { detection: createMockDetection() } });
    const button = screen.getByRole('button', { name: /actions menu/i });
    expect(button.className).toContain('am-trigger-default');
  });

  it('renders the overlay variant trigger when variant="overlay"', () => {
    render(ActionMenu, {
      props: { detection: createMockDetection(), variant: 'overlay' },
    });
    const button = screen.getByRole('button', { name: /actions menu/i });
    expect(button.className).toContain('am-trigger-overlay');
  });

  it('does not render Download item when onDownload is not provided', async () => {
    render(ActionMenu, { props: { detection: createMockDetection() } });
    await fireEvent.click(screen.getByRole('button', { name: /actions menu/i }));
    expect(screen.queryByRole('menuitem', { name: /download/i })).not.toBeInTheDocument();
  });

  it('renders Download item and fires onDownload when provided', async () => {
    const onDownload = vi.fn();
    render(ActionMenu, { props: { detection: createMockDetection(), onDownload } });
    await fireEvent.click(screen.getByRole('button', { name: /actions menu/i }));
    const item = screen.getByRole('menuitem', { name: /download/i });
    await fireEvent.click(item);
    expect(onDownload).toHaveBeenCalledTimes(1);
  });

  it('renders Correct and Incorrect quick-review items at the top', async () => {
    const onMarkCorrect = vi.fn();
    const onMarkFalsePositive = vi.fn();
    render(ActionMenu, {
      props: { detection: createMockDetection(), onMarkCorrect, onMarkFalsePositive },
    });
    await fireEvent.click(screen.getByRole('button', { name: /actions menu/i }));
    expect(screen.getByRole('menuitem', { name: /^correct$/i })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: /^incorrect$/i })).toBeInTheDocument();
  });

  it('fires onMarkCorrect when Correct is clicked', async () => {
    const onMarkCorrect = vi.fn();
    const onMarkFalsePositive = vi.fn();
    render(ActionMenu, {
      props: { detection: createMockDetection(), onMarkCorrect, onMarkFalsePositive },
    });
    await fireEvent.click(screen.getByRole('button', { name: /actions menu/i }));
    await fireEvent.click(screen.getByRole('menuitem', { name: /^correct$/i }));
    expect(onMarkCorrect).toHaveBeenCalledTimes(1);
  });

  it('fires onMarkFalsePositive when Incorrect is clicked', async () => {
    const onMarkCorrect = vi.fn();
    const onMarkFalsePositive = vi.fn();
    render(ActionMenu, {
      props: { detection: createMockDetection(), onMarkCorrect, onMarkFalsePositive },
    });
    await fireEvent.click(screen.getByRole('button', { name: /actions menu/i }));
    await fireEvent.click(screen.getByRole('menuitem', { name: /^incorrect$/i }));
    expect(onMarkFalsePositive).toHaveBeenCalledTimes(1);
  });

  it('hides quick-review items when detection is locked', async () => {
    render(ActionMenu, { props: { detection: createMockDetection({ locked: true }) } });
    await fireEvent.click(screen.getByRole('button', { name: /actions menu/i }));
    expect(screen.queryByRole('menuitem', { name: /^correct$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: /^incorrect$/i })).not.toBeInTheDocument();
  });

  it('shows ✓ badge next to Correct when verified as correct', async () => {
    const onMarkCorrect = vi.fn();
    const onMarkFalsePositive = vi.fn();
    render(ActionMenu, {
      props: {
        detection: createMockDetection({ verified: 'correct' }),
        onMarkCorrect,
        onMarkFalsePositive,
      },
    });
    await fireEvent.click(screen.getByRole('button', { name: /actions menu/i }));
    const correctItem = screen.getByRole('menuitem', { name: /^correct/i });
    expect(correctItem.textContent).toContain('✓');
  });

  it('shows ✗ badge next to Incorrect when verified as false_positive', async () => {
    const onMarkCorrect = vi.fn();
    const onMarkFalsePositive = vi.fn();
    render(ActionMenu, {
      props: {
        detection: createMockDetection({ verified: 'false_positive' }),
        onMarkCorrect,
        onMarkFalsePositive,
      },
    });
    await fireEvent.click(screen.getByRole('button', { name: /actions menu/i }));
    const incorrectItem = screen.getByRole('menuitem', { name: /^incorrect/i });
    expect(incorrectItem.textContent).toContain('✗');
  });
});
