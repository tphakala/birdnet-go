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
    common_name: 'American Robin',
    scientific_name: 'Turdus migratorius',
    confidence: 0.85,
    locked: false,
    source_type: 'microphone',
    source_name: 'default',
    clip_name: 'clip_001.wav',
    spectrogram_path: '/spectrograms/clip_001.png',
    ...overrides,
  } as Detection;
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
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    expect(screen.getByText('✓')).toBeInTheDocument();
  });

  it('shows false positive badge when detection is verified as false positive', async () => {
    render(ActionMenu, {
      props: {
        detection: createMockDetection({
          verified: 'false_positive',
        }),
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    expect(screen.getByText('✗')).toBeInTheDocument();
  });

  it('handles action without callback gracefully', async () => {
    // Test that clicking action without callback doesn't throw
    render(ActionMenu, {
      props: {
        detection: createMockDetection(),
        // No callbacks provided
      },
    });

    const button = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(button);

    // Click review without callback - should not throw
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
});
