import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  createComponentTestFactory,
  screen,
  fireEvent,
  waitFor,
} from '../../../../test/render-helpers';
import StreamTimeline from './StreamTimeline.svelte';
import type { ErrorContext, StateTransition } from './StreamManager.svelte';

// Mock scrollIntoView which is not available in jsdom
beforeEach(() => {
  vi.clearAllMocks();
  Element.prototype.scrollIntoView = vi.fn();
});

describe('StreamTimeline', () => {
  const timelineTest = createComponentTestFactory(StreamTimeline);

  // Test data factories
  const createStateTransition = (
    fromState: string,
    toState: string,
    timestamp: string,
    reason?: string
  ): StateTransition => ({
    from_state: fromState,
    to_state: toState,
    timestamp,
    reason,
  });

  const createErrorContext = (
    errorType: string,
    primaryMessage: string,
    timestamp: string,
    options: Partial<ErrorContext> = {}
  ): ErrorContext => ({
    error_type: errorType,
    primary_message: primaryMessage,
    user_facing_msg: options.user_facing_msg ?? primaryMessage,
    timestamp,
    should_open_circuit: options.should_open_circuit ?? false,
    should_restart: options.should_restart ?? false,
    troubleshooting_steps: options.troubleshooting_steps,
    target_host: options.target_host,
    target_port: options.target_port,
  });

  // Sample test data
  const sampleStateHistory: StateTransition[] = [
    createStateTransition('stopped', 'running', '2026-01-13T10:00:00Z'),
    createStateTransition('running', 'restarting', '2026-01-13T10:05:00Z', 'Data timeout'),
    createStateTransition('restarting', 'running', '2026-01-13T10:05:30Z'),
  ];

  const sampleErrorHistory: ErrorContext[] = [
    createErrorContext('connection_timeout', 'Connection timed out', '2026-01-13T10:03:00Z', {
      target_host: '192.168.1.100',
      target_port: 554,
      troubleshooting_steps: ['Check network connectivity', 'Verify camera is powered on'],
    }),
  ];

  describe('Empty State', () => {
    it('shows empty message when no history provided', () => {
      timelineTest.render({});

      expect(screen.getByText('No state or error history available.')).toBeInTheDocument();
    });

    it('shows empty message when both arrays are empty', () => {
      timelineTest.render({
        stateHistory: [],
        errorHistory: [],
      });

      expect(screen.getByText('No state or error history available.')).toBeInTheDocument();
    });
  });

  describe('Timeline Rendering', () => {
    it('renders timeline with state history only', () => {
      timelineTest.render({
        stateHistory: sampleStateHistory,
      });

      // Should not show empty message
      expect(screen.queryByText('No state or error history available.')).not.toBeInTheDocument();

      // Should render state labels (may have multiple 'running' states)
      expect(screen.getAllByText('running').length).toBeGreaterThan(0);
      expect(screen.getByText('restarting')).toBeInTheDocument();
    });

    it('renders timeline with error history only', () => {
      timelineTest.render({
        errorHistory: sampleErrorHistory,
      });

      expect(screen.queryByText('No state or error history available.')).not.toBeInTheDocument();
      expect(screen.getByText('Error')).toBeInTheDocument();
    });

    it('renders merged timeline with both state and error history', () => {
      timelineTest.render({
        stateHistory: sampleStateHistory,
        errorHistory: sampleErrorHistory,
      });

      // Should have both state labels and error labels (may have multiple 'running' states)
      expect(screen.getAllByText('running').length).toBeGreaterThan(0);
      expect(screen.getByText('Error')).toBeInTheDocument();
    });

    it('renders timeline nodes as buttons', () => {
      timelineTest.render({
        stateHistory: sampleStateHistory,
      });

      const buttons = screen.getAllByRole('button');
      // Should have one button per state transition
      expect(buttons.length).toBe(sampleStateHistory.length);
    });

    it('formats timestamps in 24-hour format', () => {
      timelineTest.render({
        stateHistory: [createStateTransition('stopped', 'running', '2026-01-13T14:30:00Z')],
      });

      // Should display time (format may vary by locale, but should be present)
      // Since we use Intl.DateTimeFormat with hour12: false, it should be 24-hour format
      const timeElements = screen.getAllByText(/\d{1,2}:\d{2}/);
      expect(timeElements.length).toBeGreaterThan(0);
    });

    it('limits to last 10 events', () => {
      const manyStates: StateTransition[] = Array.from({ length: 15 }, (_, i) =>
        createStateTransition(
          'state' + i,
          'state' + (i + 1),
          `2026-01-13T${String(10 + i).padStart(2, '0')}:00:00Z`
        )
      );

      timelineTest.render({
        stateHistory: manyStates,
      });

      const buttons = screen.getAllByRole('button');
      expect(buttons.length).toBe(10);
    });
  });

  describe('Node Colors', () => {
    it('applies green color for running state', () => {
      timelineTest.render({
        stateHistory: [createStateTransition('stopped', 'running', '2026-01-13T10:00:00Z')],
      });

      const button = screen.getByRole('button');
      expect(button).toHaveClass('border-[var(--color-success)]');
      expect(button).toHaveClass('bg-[var(--color-success)]');
    });

    it('applies amber color for restarting state (hollow)', () => {
      timelineTest.render({
        stateHistory: [createStateTransition('running', 'restarting', '2026-01-13T10:00:00Z')],
      });

      const button = screen.getByRole('button');
      expect(button).toHaveClass('border-[var(--color-warning)]');
      // Hollow nodes have base background (theme-aware)
      expect(button).toHaveClass('bg-[var(--color-base-100)]');
    });

    it('applies red color for error events', () => {
      timelineTest.render({
        errorHistory: sampleErrorHistory,
      });

      const button = screen.getByRole('button');
      expect(button).toHaveClass('border-[var(--color-error)]');
      expect(button).toHaveClass('bg-[var(--color-error)]');
    });

    it('applies red color for circuit_open state', () => {
      timelineTest.render({
        stateHistory: [createStateTransition('running', 'circuit_open', '2026-01-13T10:00:00Z')],
      });

      const button = screen.getByRole('button');
      expect(button).toHaveClass('border-[var(--color-error)]');
    });
  });

  describe('Popover Interaction', () => {
    it('opens popover on node click', async () => {
      timelineTest.render({
        stateHistory: sampleStateHistory,
      });

      const buttons = screen.getAllByRole('button');
      await fireEvent.click(buttons[0]);

      // Should show popover with state change title
      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      });
    });

    it('closes popover on second click of same node', async () => {
      timelineTest.render({
        stateHistory: sampleStateHistory,
      });

      const buttons = screen.getAllByRole('button');
      await fireEvent.click(buttons[0]);

      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      });

      await fireEvent.click(buttons[0]);

      await waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      });
    });

    it('closes popover on Escape key', async () => {
      timelineTest.render({
        stateHistory: sampleStateHistory,
      });

      const buttons = screen.getAllByRole('button');
      await fireEvent.click(buttons[0]);

      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      });

      await fireEvent.keyDown(window, { key: 'Escape' });

      await waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      });
    });
  });

  describe('Date Parsing Safety', () => {
    it('filters out invalid timestamps', () => {
      const invalidStates: StateTransition[] = [
        createStateTransition('stopped', 'running', '2026-01-13T10:00:00Z'),
        createStateTransition('running', 'stopped', 'invalid-timestamp'),
        createStateTransition('stopped', 'running', ''),
      ];

      timelineTest.render({
        stateHistory: invalidStates,
      });

      // Should only render one valid event
      const buttons = screen.getAllByRole('button');
      expect(buttons.length).toBe(1);
    });

    it('handles undefined timestamps gracefully', () => {
      const statesWithUndefined = [
        { from_state: 'stopped', to_state: 'running', timestamp: undefined as unknown as string },
      ];

      timelineTest.render({
        stateHistory: statesWithUndefined,
      });

      // Should show empty message since all timestamps are invalid
      expect(screen.getByText('No state or error history available.')).toBeInTheDocument();
    });
  });

  describe('Accessibility', () => {
    it('nodes have accessible labels', () => {
      timelineTest.render({
        stateHistory: sampleStateHistory,
      });

      const buttons = screen.getAllByRole('button');
      buttons.forEach(button => {
        expect(button).toHaveAttribute('aria-label');
        expect(button.getAttribute('aria-label')).toMatch(/Event at \d{1,2}:\d{2}/);
      });
    });

    it('nodes are keyboard accessible', async () => {
      timelineTest.render({
        stateHistory: sampleStateHistory,
      });

      const buttons = screen.getAllByRole('button');
      buttons[0].focus();

      // Native button elements trigger click on Enter/Space keys.
      // In jsdom, fireEvent.keyDown doesn't trigger this native behavior,
      // so we use fireEvent.click which is what actually happens in browsers.
      await fireEvent.click(buttons[0]);

      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      });
    });
  });

  describe('Event Sorting', () => {
    it('sorts events by timestamp ascending', () => {
      const unsortedStates: StateTransition[] = [
        createStateTransition('state1', 'state2', '2026-01-13T12:00:00Z'),
        createStateTransition('state0', 'state1', '2026-01-13T10:00:00Z'),
        createStateTransition('state2', 'state3', '2026-01-13T11:00:00Z'),
      ];

      timelineTest.render({
        stateHistory: unsortedStates,
      });

      // Get all state labels in order
      const stateLabels = screen.getAllByText(/state\d/);
      // First should be state1 (from 10:00), last should be state2 (from 12:00)
      expect(stateLabels[0]).toHaveTextContent('state1');
      expect(stateLabels[2]).toHaveTextContent('state2');
    });
  });

  describe('Duplicate Timestamps', () => {
    // Regression coverage for BIRDNET-GO-1A0: Svelte 5 threw `each_key_duplicate`
    // when multiple events shared the same millisecond timestamp and the #each
    // key was `event.timestamp.getTime()`. The composite key must include the
    // event type and data discriminator to stay unique.
    const SHARED_TIMESTAMP = '2026-04-14T10:00:00.000Z';

    it('renders a state transition and an error sharing the same timestamp', () => {
      const stateHistory: StateTransition[] = [
        createStateTransition('idle', 'running', SHARED_TIMESTAMP),
      ];
      const errorHistory: ErrorContext[] = [
        createErrorContext('connection_failed', 'Connection refused', SHARED_TIMESTAMP),
      ];

      expect(() =>
        timelineTest.render({
          stateHistory,
          errorHistory,
        })
      ).not.toThrow();

      // Both events should be rendered as distinct nodes.
      const buttons = screen.getAllByRole('button');
      expect(buttons.length).toBe(2);
    });

    it('renders two state events with identical timestamp but different to_state', () => {
      const stateHistory: StateTransition[] = [
        createStateTransition('idle', 'running', SHARED_TIMESTAMP),
        createStateTransition('running', 'restarting', SHARED_TIMESTAMP),
      ];

      expect(() =>
        timelineTest.render({
          stateHistory,
        })
      ).not.toThrow();

      const buttons = screen.getAllByRole('button');
      expect(buttons.length).toBe(2);
    });

    it('renders two error events with identical timestamp but different error_type', () => {
      const errorHistory: ErrorContext[] = [
        createErrorContext('connection_failed', 'Connection refused', SHARED_TIMESTAMP),
        createErrorContext('timeout', 'Operation timed out', SHARED_TIMESTAMP),
      ];

      expect(() =>
        timelineTest.render({
          errorHistory,
        })
      ).not.toThrow();

      const buttons = screen.getAllByRole('button');
      expect(buttons.length).toBe(2);
    });

    it('renders identical events sharing timestamp, type, and discriminator', () => {
      // Worst case: two events with timestamp, type, and discriminator all
      // identical. The per-duplicate ordinal in the composite key keeps each
      // block entry unique without relying on the sliding-window render index.
      const errorHistory: ErrorContext[] = [
        createErrorContext('connection_failed', 'Connection refused', SHARED_TIMESTAMP),
        createErrorContext('connection_failed', 'Connection refused', SHARED_TIMESTAMP),
      ];

      expect(() =>
        timelineTest.render({
          errorHistory,
        })
      ).not.toThrow();

      const buttons = screen.getAllByRole('button');
      expect(buttons.length).toBe(2);
    });

    it('gives identical-tuple duplicates distinct accessible names', () => {
      // Screen-reader regression (CodeRabbit round-3): when timestamp, type,
      // and discriminator all match, aria-label must include an ordinal
      // tiebreaker so the duplicates are distinguishable. First occurrence
      // keeps the plain label; subsequent ones get "(2)", "(3)", …
      const errorHistory: ErrorContext[] = [
        createErrorContext('connection_failed', 'Connection refused', SHARED_TIMESTAMP),
        createErrorContext('connection_failed', 'Connection refused', SHARED_TIMESTAMP),
        createErrorContext('connection_failed', 'Connection refused', SHARED_TIMESTAMP),
      ];

      timelineTest.render({ errorHistory });

      const buttons = screen.getAllByRole('button');
      expect(buttons.length).toBe(3);

      const labels = buttons.map(btn => btn.getAttribute('aria-label'));
      const unique = new Set(labels);
      expect(unique.size).toBe(3);

      // First duplicate has no tiebreaker; second and third have (2) / (3).
      expect(labels[0]).not.toMatch(/\(\d+\)$/);
      expect(labels[1]).toMatch(/\(2\)$/);
      expect(labels[2]).toMatch(/\(3\)$/);
    });

    it('toggles the popover on click for events sharing a timestamp', async () => {
      // Regression: earlier implementation used the render index in the
      // composite key and stored the clicked index, so clicking two events
      // that collided on timestamp could fail to close the popover.
      const stateHistory: StateTransition[] = [
        createStateTransition('idle', 'running', SHARED_TIMESTAMP),
      ];
      const errorHistory: ErrorContext[] = [
        createErrorContext('connection_failed', 'Connection refused', SHARED_TIMESTAMP),
      ];

      timelineTest.render({ stateHistory, errorHistory });

      const buttons = screen.getAllByRole('button');
      expect(buttons.length).toBe(2);

      // Click the first node — popover opens.
      await fireEvent.click(buttons[0]);
      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      });

      // Click the same node again — popover closes (composite-key equality).
      await fireEvent.click(buttons[0]);
      await waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      });

      // Click the second node — popover opens for the other event.
      await fireEvent.click(buttons[1]);
      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      });
    });
  });

  describe('Sliding-window key stability', () => {
    // Regression: earlier implementation included the render index in the
    // each-key. Because timelineEvents is capped at the last 10 events, any
    // new event shifts the indices of all older survivors and forces Svelte
    // to recreate their DOM nodes. This test confirms two events that
    // survive a window shift keep stable keys and therefore stable elements.
    it('keeps surviving events on stable keys as older events drop off', async () => {
      const base = new Date('2026-04-14T10:00:00.000Z').getTime();
      const ts = (offset: number) => new Date(base + offset).toISOString();

      // Render with 10 events so the window is full.
      const initialHistory: StateTransition[] = Array.from({ length: 10 }, (_, i) =>
        createStateTransition('idle', `state${i}`, ts(i * 1000))
      );
      const rendered = timelineTest.render({ stateHistory: initialHistory });

      const initialButtons = screen.getAllByRole('button');
      expect(initialButtons.length).toBe(10);
      const survivorBefore = initialButtons[9]; // newest at the end

      // Append one more event — the oldest drops off.
      const withShift: StateTransition[] = [
        ...initialHistory,
        createStateTransition('idle', 'state10', ts(10 * 1000)),
      ];
      rendered.rerender({ stateHistory: withShift });

      await waitFor(() => {
        const after = screen.getAllByRole('button');
        expect(after.length).toBe(10);
      });

      const afterButtons = screen.getAllByRole('button');
      // state10 is now the newest; the previously-newest survivor (state9)
      // should still be represented by the same DOM node because its key
      // did not change.
      const survivorAfter = afterButtons[8];
      expect(survivorAfter).toBe(survivorBefore);
    });

    // Regression: earlier revisions left `selectedKey` / `selectedEvent`
    // set after the selected event dropped out of the sliding window, so
    // the popover remained mounted against a detached node (CodeRabbit
    // round-3). The $effect must reconcile the selection and close the
    // popover when the selected event is no longer in the visible window.
    it('closes the popover when the selected event drops out of the window', async () => {
      const base = new Date('2026-04-14T10:00:00.000Z').getTime();
      const ts = (offset: number) => new Date(base + offset).toISOString();

      const initialHistory: StateTransition[] = Array.from({ length: 10 }, (_, i) =>
        createStateTransition('idle', `state${i}`, ts(i * 1000))
      );
      const rendered = timelineTest.render({ stateHistory: initialHistory });

      // Open the popover on the OLDEST visible event — the one that will
      // drop off when a new event arrives.
      const initialButtons = screen.getAllByRole('button');
      await fireEvent.click(initialButtons[0]);
      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      });

      // Push 3 new events so the original oldest falls outside slice(-10).
      const withShift: StateTransition[] = [
        ...initialHistory,
        createStateTransition('idle', 'state10', ts(10 * 1000)),
        createStateTransition('idle', 'state11', ts(11 * 1000)),
        createStateTransition('idle', 'state12', ts(12 * 1000)),
      ];
      rendered.rerender({ stateHistory: withShift });

      // The selected event is gone — the popover must close automatically
      // rather than linger against a detached DOM node.
      await waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      });
    });
  });
});
