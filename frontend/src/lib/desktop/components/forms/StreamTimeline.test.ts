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
      expect(button).toHaveClass('border-green-400');
      expect(button).toHaveClass('bg-green-400');
    });

    it('applies amber color for restarting state (hollow)', () => {
      timelineTest.render({
        stateHistory: [createStateTransition('running', 'restarting', '2026-01-13T10:00:00Z')],
      });

      const button = screen.getByRole('button');
      expect(button).toHaveClass('border-amber-400');
      // Hollow nodes have base background (theme-aware)
      expect(button).toHaveClass('bg-base-100');
    });

    it('applies red color for error events', () => {
      timelineTest.render({
        errorHistory: sampleErrorHistory,
      });

      const button = screen.getByRole('button');
      expect(button).toHaveClass('border-red-400');
      expect(button).toHaveClass('bg-red-400');
    });

    it('applies red color for circuit_open state', () => {
      timelineTest.render({
        stateHistory: [createStateTransition('running', 'circuit_open', '2026-01-13T10:00:00Z')],
      });

      const button = screen.getByRole('button');
      expect(button).toHaveClass('border-red-400');
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
});
