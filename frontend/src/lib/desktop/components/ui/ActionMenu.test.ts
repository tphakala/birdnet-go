/**
 * ActionMenu Component Tests
 *
 * Tests for the dropdown action menu component that provides various actions
 * for bird detection items including review, toggle visibility, lock/unlock, and delete.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/svelte';
import { tick } from 'svelte';
import ActionMenu from './ActionMenu.svelte';
import type { Detection } from '$lib/types/detection.types';

describe('ActionMenu Component', () => {
  // Mock detection data
  const mockDetection: Detection = {
    id: 1,
    date: '2024-01-15',
    time: '10:30:00',
    source: 'microphone',
    beginTime: '10:29:55',
    endTime: '10:30:05',
    speciesCode: 'amerob',
    commonName: 'American Robin',
    scientificName: 'Turdus migratorius',
    confidence: 0.95,
    verified: 'unverified',
    locked: false,
    review: undefined,
  };

  const mockDetectionLocked: Detection = {
    ...mockDetection,
    locked: true,
  };

  const mockDetectionVerified: Detection = {
    ...mockDetection,
    review: { verified: 'correct' },
  };

  const mockDetectionFalsePositive: Detection = {
    ...mockDetection,
    review: { verified: 'false_positive' },
  };

  // Mock callback functions
  const mockCallbacks = {
    onReview: vi.fn(),
    onToggleSpecies: vi.fn(),
    onToggleLock: vi.fn(),
    onDelete: vi.fn(),
    onMenuOpen: vi.fn(),
    onMenuClose: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  describe('Authentication State', () => {
    it('should show "No actions available" when not authenticated', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: false,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      expect(screen.getByText('No actions available')).toBeInTheDocument();
      expect(screen.queryByText('Review detection')).not.toBeInTheDocument();
      expect(screen.queryByText('Ignore species')).not.toBeInTheDocument();
      expect(screen.queryByText('Lock detection')).not.toBeInTheDocument();
      expect(screen.queryByText('Delete detection')).not.toBeInTheDocument();
    });

    it('should show all actions when authenticated', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          ...mockCallbacks,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      expect(screen.getByText('Review detection')).toBeInTheDocument();
      expect(screen.getByText('Ignore species')).toBeInTheDocument();
      expect(screen.getByText('Lock detection')).toBeInTheDocument();
      expect(screen.getByText('Delete detection')).toBeInTheDocument();
      expect(screen.queryByText('No actions available')).not.toBeInTheDocument();
    });

    it('should default to authenticated when isAuthenticated prop is not provided', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          ...mockCallbacks,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      expect(screen.getByText('Review detection')).toBeInTheDocument();
      expect(screen.queryByText('No actions available')).not.toBeInTheDocument();
    });
  });

  describe('Menu Behavior', () => {
    it('should toggle menu open and closed on button click', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');

      // Initially closed
      expect(screen.queryByRole('menu')).not.toBeInTheDocument();

      // Open menu
      await fireEvent.click(menuButton);
      expect(screen.getByRole('menu')).toBeInTheDocument();
      expect(menuButton).toHaveAttribute('aria-expanded', 'true');

      // Close menu
      await fireEvent.click(menuButton);
      await tick();
      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
      expect(menuButton).toHaveAttribute('aria-expanded', 'false');
    });

    it('should close menu when clicking outside', async () => {
      const { container } = render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          onMenuClose: mockCallbacks.onMenuClose,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      expect(screen.getByRole('menu')).toBeInTheDocument();

      // Click outside
      await fireEvent.click(container);
      await tick();

      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
      expect(mockCallbacks.onMenuClose).toHaveBeenCalledTimes(1);
    });

    it('should close menu when pressing Escape', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          onMenuClose: mockCallbacks.onMenuClose,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      expect(screen.getByRole('menu')).toBeInTheDocument();

      // Press Escape
      await fireEvent.keyDown(document, { key: 'Escape' });
      await tick();

      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
      expect(mockCallbacks.onMenuClose).toHaveBeenCalledTimes(1);
      expect(document.activeElement).toBe(menuButton);
    });

    it('should call onMenuOpen when opening menu', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          onMenuOpen: mockCallbacks.onMenuOpen,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      expect(mockCallbacks.onMenuOpen).toHaveBeenCalledTimes(1);
    });

    it('should stop event propagation when clicking menu button', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');

      // Create a synthetic event with stopPropagation tracked
      const event = new MouseEvent('click', { bubbles: true, cancelable: true });
      const stopPropagationSpy = vi.spyOn(event, 'stopPropagation');

      // Dispatch the event
      menuButton.dispatchEvent(event);
      await tick();

      // The component should call stopPropagation on the event
      expect(stopPropagationSpy).toHaveBeenCalled();

      // Menu should be open after click
      expect(screen.getByRole('menu')).toBeInTheDocument();
    });
  });

  describe('Action Callbacks', () => {
    it('should call onReview callback and close menu', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          onReview: mockCallbacks.onReview,
          onMenuClose: mockCallbacks.onMenuClose,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      const reviewButton = screen.getByText('Review detection');
      await fireEvent.click(reviewButton);

      expect(mockCallbacks.onReview).toHaveBeenCalledTimes(1);
      expect(mockCallbacks.onMenuClose).toHaveBeenCalledTimes(1);
      await tick();
      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    });

    it('should call onToggleSpecies callback and close menu', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          isExcluded: false,
          onToggleSpecies: mockCallbacks.onToggleSpecies,
          onMenuClose: mockCallbacks.onMenuClose,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      const toggleButton = screen.getByText('Ignore species');
      await fireEvent.click(toggleButton);

      expect(mockCallbacks.onToggleSpecies).toHaveBeenCalledTimes(1);
      expect(mockCallbacks.onMenuClose).toHaveBeenCalledTimes(1);
    });

    it('should call onToggleLock callback and close menu', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          onToggleLock: mockCallbacks.onToggleLock,
          onMenuClose: mockCallbacks.onMenuClose,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      const lockButton = screen.getByText('Lock detection');
      await fireEvent.click(lockButton);

      expect(mockCallbacks.onToggleLock).toHaveBeenCalledTimes(1);
      expect(mockCallbacks.onMenuClose).toHaveBeenCalledTimes(1);
    });

    it('should call onDelete callback and close menu', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          onDelete: mockCallbacks.onDelete,
          onMenuClose: mockCallbacks.onMenuClose,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      const deleteButton = screen.getByText('Delete detection');
      await fireEvent.click(deleteButton);

      expect(mockCallbacks.onDelete).toHaveBeenCalledTimes(1);
      expect(mockCallbacks.onMenuClose).toHaveBeenCalledTimes(1);
    });
  });

  describe('Conditional Rendering', () => {
    it('should not show delete button when detection is locked', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetectionLocked,
          isAuthenticated: true,
          ...mockCallbacks,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      expect(screen.queryByText('Delete detection')).not.toBeInTheDocument();
      expect(screen.getByText('Unlock detection')).toBeInTheDocument();
    });

    it('should show delete button when detection is not locked', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          ...mockCallbacks,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      expect(screen.getByText('Delete detection')).toBeInTheDocument();
      expect(screen.getByText('Lock detection')).toBeInTheDocument();
    });

    it('should show correct text for locked/unlocked state', async () => {
      const { unmount } = render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
        },
      });

      let menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);
      expect(screen.getByText('Lock detection')).toBeInTheDocument();

      await fireEvent.click(menuButton); // Close
      unmount();

      // Render with locked detection
      render(ActionMenu, {
        props: {
          detection: mockDetectionLocked,
          isAuthenticated: true,
        },
      });

      menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);
      expect(screen.getByText('Unlock detection')).toBeInTheDocument();
    });

    it('should show correct text based on isExcluded state', async () => {
      const { rerender } = render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          isExcluded: false,
        },
      });

      let menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);
      expect(screen.getByText('Ignore species')).toBeInTheDocument();

      await fireEvent.click(menuButton); // Close

      // Update with excluded state
      await rerender({
        detection: mockDetection,
        isAuthenticated: true,
        isExcluded: true,
      });

      menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);
      expect(screen.getByText('Show species')).toBeInTheDocument();
    });

    it('should show verification badge for verified detection', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetectionVerified,
          isAuthenticated: true,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      const reviewItem = screen.getByText('Review detection').closest('div');
      expect(reviewItem?.querySelector('.badge-success')).toBeInTheDocument();
      expect(reviewItem?.textContent).toContain('✓');
    });

    it('should show error badge for false positive detection', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetectionFalsePositive,
          isAuthenticated: true,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      const reviewItem = screen.getByText('Review detection').closest('div');
      expect(reviewItem?.querySelector('.badge-error')).toBeInTheDocument();
      expect(reviewItem?.textContent).toContain('✗');
    });
  });

  describe('Accessibility', () => {
    it('should have proper ARIA attributes', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      expect(menuButton).toHaveAttribute('aria-haspopup', 'true');
      expect(menuButton).toHaveAttribute('aria-expanded', 'false');

      await fireEvent.click(menuButton);
      expect(menuButton).toHaveAttribute('aria-expanded', 'true');

      const menu = screen.getByRole('menu');
      expect(menu).toBeInTheDocument();

      const menuItems = screen.getAllByRole('menuitem');
      expect(menuItems.length).toBeGreaterThan(0);
    });

    it('should have correct button type for all action buttons', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      const menuItems = screen.getAllByRole('menuitem');
      menuItems.forEach(item => {
        expect(item.tagName.toLowerCase()).toBe('button');
      });
    });

    it('should handle keyboard navigation', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          onMenuClose: mockCallbacks.onMenuClose,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      expect(screen.getByRole('menu')).toBeInTheDocument();

      // Escape should close menu
      await fireEvent.keyDown(document, { key: 'Escape' });
      await tick();

      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
      expect(mockCallbacks.onMenuClose).toHaveBeenCalledTimes(1);
    });
  });

  describe('Menu Positioning', () => {
    it('should update menu position on window resize', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      const menu = screen.getByRole('menu');
      // The menu has 'fixed' class from the template
      expect(menu).toHaveClass('fixed');

      // Trigger resize event
      fireEvent(window, new Event('resize'));
      await tick();

      // Menu should still be visible
      expect(screen.getByRole('menu')).toBeInTheDocument();
    });

    it('should update menu position on scroll', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      const menu = screen.getByRole('menu');
      expect(menu).toBeInTheDocument();

      // Trigger scroll event
      fireEvent(window, new Event('scroll'));
      await tick();

      // Menu should still be visible
      expect(screen.getByRole('menu')).toBeInTheDocument();
    });
  });

  describe('CSS Classes and Styling', () => {
    it('should apply custom className prop', () => {
      const { container } = render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          className: 'custom-class',
        },
      });

      const dropdown = container.querySelector('.dropdown');
      expect(dropdown).toHaveClass('custom-class');
    });

    it('should apply error styling to delete button', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      const deleteButton = screen.getByText('Delete detection').closest('button');
      expect(deleteButton).toHaveClass('text-error');
    });

    it('should have proper menu styling classes', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      const menu = screen.getByRole('menu');
      expect(menu).toHaveClass(
        'menu',
        'p-2',
        'shadow-lg',
        'bg-base-100',
        'rounded-box',
        'w-52',
        'border',
        'border-base-300'
      );
    });
  });

  describe('Edge Cases', () => {
    it('should handle undefined callbacks gracefully', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          // No callbacks provided
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);

      // Should not throw when clicking actions without callbacks
      const reviewButton = screen.getByText('Review detection');
      await fireEvent.click(reviewButton);

      // Menu should still close
      await tick();
      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    });

    it('should clean up event listeners on unmount', async () => {
      const { unmount } = render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          onMenuClose: mockCallbacks.onMenuClose,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');
      await fireEvent.click(menuButton);
      expect(screen.getByRole('menu')).toBeInTheDocument();

      unmount();

      // After unmount, events should not trigger callbacks
      fireEvent(document, new Event('click'));
      fireEvent.keyDown(document, { key: 'Escape' });

      // onMenuClose should only have been called once during unmount
      expect(mockCallbacks.onMenuClose).toHaveBeenCalledTimes(1);
    });

    it('should handle rapid open/close cycles', async () => {
      render(ActionMenu, {
        props: {
          detection: mockDetection,
          isAuthenticated: true,
          onMenuOpen: mockCallbacks.onMenuOpen,
          onMenuClose: mockCallbacks.onMenuClose,
        },
      });

      const menuButton = screen.getByLabelText('Actions menu');

      // Rapid clicking
      for (let i = 0; i < 5; i++) {
        await fireEvent.click(menuButton);
        await tick();
      }

      // Final state should be open (odd number of clicks)
      expect(screen.getByRole('menu')).toBeInTheDocument();

      // Callbacks should have been called correctly
      expect(mockCallbacks.onMenuOpen).toHaveBeenCalledTimes(3);
      expect(mockCallbacks.onMenuClose).toHaveBeenCalledTimes(2);
    });
  });
});
