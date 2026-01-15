import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createComponentTestFactory, screen, fireEvent } from '../../../../test/render-helpers';
import TimelinePopover from './TimelinePopover.svelte';
import type { TimelineEvent } from './StreamTimeline.svelte';
import type { ErrorContext, StateTransition } from './StreamManager.svelte';

// Mock scrollIntoView which is not available in jsdom
beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

describe('TimelinePopover', () => {
  const popoverTest = createComponentTestFactory(TimelinePopover);

  // Create a mock anchor element
  const createMockAnchorEl = (): HTMLElement => {
    const el = document.createElement('button');
    document.body.appendChild(el);
    return el;
  };

  // Test data factories
  const createStateEvent = (
    fromState: string,
    toState: string,
    timestamp: Date,
    reason?: string
  ): TimelineEvent => ({
    type: 'state',
    timestamp,
    data: {
      from_state: fromState,
      to_state: toState,
      timestamp: timestamp.toISOString(),
      reason,
    } as StateTransition,
  });

  const createErrorEvent = (
    errorType: string,
    primaryMessage: string,
    userFacingMsg: string,
    timestamp: Date,
    options: Partial<ErrorContext> = {}
  ): TimelineEvent => ({
    type: 'error',
    timestamp,
    data: {
      error_type: errorType,
      primary_message: primaryMessage,
      user_facing_msg: userFacingMsg,
      timestamp: timestamp.toISOString(),
      should_open_circuit: options.should_open_circuit ?? false,
      should_restart: options.should_restart ?? false,
      troubleshooting_steps: options.troubleshooting_steps,
      target_host: options.target_host,
      target_port: options.target_port,
    } as ErrorContext,
  });

  let mockAnchorEl: HTMLElement;

  beforeEach(() => {
    // Clean up any existing anchor elements
    document.body.innerHTML = '';
    mockAnchorEl = createMockAnchorEl();
  });

  describe('State Event Display', () => {
    it('renders state change title', () => {
      const stateEvent = createStateEvent('stopped', 'running', new Date('2026-01-13T10:00:00Z'));

      popoverTest.render({
        event: stateEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.getByText('State Change: running')).toBeInTheDocument();
    });

    it('displays from and to states', () => {
      const stateEvent = createStateEvent('stopped', 'running', new Date('2026-01-13T10:00:00Z'));

      popoverTest.render({
        event: stateEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.getByText('From:')).toBeInTheDocument();
      expect(screen.getByText('stopped')).toBeInTheDocument();
      expect(screen.getByText('To:')).toBeInTheDocument();
      expect(screen.getByText('running')).toBeInTheDocument();
    });

    it('displays reason when provided', () => {
      const stateEvent = createStateEvent(
        'running',
        'restarting',
        new Date('2026-01-13T10:00:00Z'),
        'Data timeout detected'
      );

      popoverTest.render({
        event: stateEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.getByText('Reason:')).toBeInTheDocument();
      expect(screen.getByText('Data timeout detected')).toBeInTheDocument();
    });

    it('does not show reason section when not provided', () => {
      const stateEvent = createStateEvent('stopped', 'running', new Date('2026-01-13T10:00:00Z'));

      popoverTest.render({
        event: stateEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.queryByText('Reason:')).not.toBeInTheDocument();
    });
  });

  describe('Error Event Display', () => {
    it('renders user-facing error message as title', () => {
      const errorEvent = createErrorEvent(
        'connection_timeout',
        'TCP connection timed out after 30s',
        'Connection timed out',
        new Date('2026-01-13T10:00:00Z')
      );

      popoverTest.render({
        event: errorEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.getByText('Connection timed out')).toBeInTheDocument();
    });

    it('displays primary message', () => {
      const errorEvent = createErrorEvent(
        'connection_timeout',
        'TCP connection timed out after 30s',
        'Connection timed out',
        new Date('2026-01-13T10:00:00Z')
      );

      popoverTest.render({
        event: errorEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.getByText('TCP connection timed out after 30s')).toBeInTheDocument();
    });

    it('displays host and port when provided', () => {
      const errorEvent = createErrorEvent(
        'connection_timeout',
        'Connection failed',
        'Connection timed out',
        new Date('2026-01-13T10:00:00Z'),
        {
          target_host: '192.168.1.100',
          target_port: 554,
        }
      );

      popoverTest.render({
        event: errorEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.getByText('Host: 192.168.1.100:554')).toBeInTheDocument();
    });

    it('displays host without port when port not provided', () => {
      const errorEvent = createErrorEvent(
        'connection_timeout',
        'Connection failed',
        'Connection timed out',
        new Date('2026-01-13T10:00:00Z'),
        {
          target_host: '192.168.1.100',
        }
      );

      popoverTest.render({
        event: errorEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.getByText('Host: 192.168.1.100')).toBeInTheDocument();
    });

    it('displays troubleshooting steps', () => {
      const troubleshootingSteps = [
        'Check network connectivity',
        'Verify camera is powered on',
        'Ensure RTSP port 554 is accessible',
      ];

      const errorEvent = createErrorEvent(
        'connection_timeout',
        'Connection failed',
        'Connection timed out',
        new Date('2026-01-13T10:00:00Z'),
        { troubleshooting_steps: troubleshootingSteps }
      );

      popoverTest.render({
        event: errorEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.getByText('Troubleshooting:')).toBeInTheDocument();
      troubleshootingSteps.forEach(step => {
        expect(screen.getByText(step)).toBeInTheDocument();
      });
    });

    it('does not show troubleshooting section when empty', () => {
      const errorEvent = createErrorEvent(
        'connection_timeout',
        'Connection failed',
        'Connection timed out',
        new Date('2026-01-13T10:00:00Z'),
        { troubleshooting_steps: [] }
      );

      popoverTest.render({
        event: errorEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.queryByText('Troubleshooting:')).not.toBeInTheDocument();
    });

    it('falls back to "Error" when user_facing_msg is empty', () => {
      const errorEvent = createErrorEvent(
        'unknown_error',
        'Some internal error',
        '', // Empty user-facing message
        new Date('2026-01-13T10:00:00Z')
      );

      popoverTest.render({
        event: errorEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.getByText('Error')).toBeInTheDocument();
    });
  });

  describe('Timestamp Display', () => {
    it('formats timestamp with date and time', () => {
      const errorEvent = createErrorEvent(
        'connection_timeout',
        'Connection failed',
        'Connection timed out',
        new Date('2026-01-13T14:30:00Z')
      );

      popoverTest.render({
        event: errorEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      // Should contain formatted date/time (format may vary by locale)
      // We check for presence of time pattern
      const timeElements = screen.getAllByText(/\d{1,2}:\d{2}/);
      expect(timeElements.length).toBeGreaterThan(0);
    });
  });

  describe('Close Functionality', () => {
    it('calls onClose when close button clicked', async () => {
      const onClose = vi.fn();
      const stateEvent = createStateEvent('stopped', 'running', new Date('2026-01-13T10:00:00Z'));

      popoverTest.render({
        event: stateEvent,
        anchorEl: mockAnchorEl,
        onClose,
      });

      const closeButton = screen.getByLabelText('Close');
      await fireEvent.click(closeButton);

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('calls onClose when clicking outside popover', async () => {
      const onClose = vi.fn();
      const stateEvent = createStateEvent('stopped', 'running', new Date('2026-01-13T10:00:00Z'));

      popoverTest.render({
        event: stateEvent,
        anchorEl: mockAnchorEl,
        onClose,
      });

      // Click on document body (outside popover)
      await fireEvent.click(document.body);

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('does not call onClose when clicking inside popover', async () => {
      const onClose = vi.fn();
      const stateEvent = createStateEvent('stopped', 'running', new Date('2026-01-13T10:00:00Z'));

      popoverTest.render({
        event: stateEvent,
        anchorEl: mockAnchorEl,
        onClose,
      });

      const popover = screen.getByRole('dialog');
      await fireEvent.click(popover);

      expect(onClose).not.toHaveBeenCalled();
    });

    it('does not call onClose when clicking anchor element', async () => {
      const onClose = vi.fn();
      const stateEvent = createStateEvent('stopped', 'running', new Date('2026-01-13T10:00:00Z'));

      popoverTest.render({
        event: stateEvent,
        anchorEl: mockAnchorEl,
        onClose,
      });

      await fireEvent.click(mockAnchorEl);

      expect(onClose).not.toHaveBeenCalled();
    });
  });

  describe('Accessibility', () => {
    it('has dialog role', () => {
      const stateEvent = createStateEvent('stopped', 'running', new Date('2026-01-13T10:00:00Z'));

      popoverTest.render({
        event: stateEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });

    it('has aria-labelledby pointing to title', () => {
      const stateEvent = createStateEvent('stopped', 'running', new Date('2026-01-13T10:00:00Z'));

      popoverTest.render({
        event: stateEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      const dialog = screen.getByRole('dialog');
      expect(dialog).toHaveAttribute('aria-labelledby', 'popover-title');
    });

    it('close button has accessible label', () => {
      const stateEvent = createStateEvent('stopped', 'running', new Date('2026-01-13T10:00:00Z'));

      popoverTest.render({
        event: stateEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      expect(screen.getByLabelText('Close')).toBeInTheDocument();
    });
  });

  describe('Styling', () => {
    it('applies correct container classes', () => {
      const stateEvent = createStateEvent('stopped', 'running', new Date('2026-01-13T10:00:00Z'));

      popoverTest.render({
        event: stateEvent,
        anchorEl: mockAnchorEl,
        onClose: vi.fn(),
      });

      const popover = screen.getByRole('dialog');
      // Uses theme-aware colors (bg-base-200 instead of bg-gray-800)
      expect(popover).toHaveClass('absolute', 'z-20', 'bg-base-200', 'border', 'rounded-lg');
    });
  });
});
