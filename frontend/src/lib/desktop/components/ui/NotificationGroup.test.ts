import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import NotificationGroup from './NotificationGroup.svelte';
import type { NotificationGroup as NotificationGroupType, Notification } from '$lib/utils/notifications';

// Create mock notification for testing
function createMockNotification(overrides: Partial<Notification> = {}): Notification {
  return {
    id: 'notif-1',
    type: 'info',
    message: 'Test notification message',
    timestamp: new Date().toISOString(),
    priority: 'medium',
    read: false,
    status: 'unread',
    ...overrides,
  };
}

// Create mock notification group for testing
function createMockGroup(overrides: Partial<NotificationGroupType> = {}): NotificationGroupType {
  const notifications = overrides.notifications ?? [createMockNotification()];
  return {
    key: 'test-group',
    title: 'Test Group',
    type: 'info',
    component: 'Test',
    notifications,
    unreadCount: notifications.filter(n => !n.read).length,
    earliestTimestamp: notifications[notifications.length - 1]?.timestamp || new Date().toISOString(),
    latestTimestamp: notifications[0]?.timestamp || new Date().toISOString(),
    highestPriority: 'medium',
    ...overrides,
  };
}

describe('NotificationGroup', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date('2024-01-15T12:00:00Z'));
  });

  afterEach(() => {
    vi.clearAllMocks();
    vi.useRealTimers();
  });

  it('renders the group title', () => {
    render(NotificationGroup, {
      props: {
        group: createMockGroup({ title: 'System Alerts' }),
      },
    });

    expect(screen.getByText('System Alerts')).toBeInTheDocument();
  });

  it('shows notification count badge when multiple notifications', () => {
    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          notifications: [
            createMockNotification({ id: '1' }),
            createMockNotification({ id: '2' }),
            createMockNotification({ id: '3' }),
          ],
        }),
      },
    });

    expect(screen.getByText('3')).toBeInTheDocument();
  });

  it('does not show count badge for single notification', () => {
    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          notifications: [createMockNotification()],
        }),
      },
    });

    // Should not show "1" badge
    expect(screen.queryByText('1')).not.toBeInTheDocument();
  });

  it('expands when header is clicked', async () => {
    render(NotificationGroup, {
      props: {
        group: createMockGroup(),
      },
    });

    const expandButton = screen.getByRole('button', { expanded: false });
    await fireEvent.click(expandButton);

    expect(screen.getByRole('button', { expanded: true })).toBeInTheDocument();
  });

  it('shows expanded content when defaultOpen is true', () => {
    const notification = createMockNotification({ message: 'Expanded content' });
    render(NotificationGroup, {
      props: {
        group: createMockGroup({ notifications: [notification] }),
        defaultOpen: true,
      },
    });

    expect(screen.getByText('Expanded content')).toBeInTheDocument();
  });

  it('hides expanded content when defaultOpen is false', () => {
    const notification = createMockNotification({ message: 'Hidden content' });
    render(NotificationGroup, {
      props: {
        group: createMockGroup({ notifications: [notification] }),
        defaultOpen: false,
      },
    });

    expect(screen.queryByText('Hidden content')).not.toBeInTheDocument();
  });

  it('displays component badge when provided', () => {
    render(NotificationGroup, {
      props: {
        group: createMockGroup({ component: 'Audio' }),
      },
    });

    expect(screen.getByText('Audio')).toBeInTheDocument();
  });

  it('displays priority badge', () => {
    render(NotificationGroup, {
      props: {
        group: createMockGroup({ highestPriority: 'high' }),
      },
    });

    expect(screen.getByText('high')).toBeInTheDocument();
  });

  it('displays unread count when there are unread notifications', () => {
    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          notifications: [
            createMockNotification({ id: '1', read: false }),
            createMockNotification({ id: '2', read: true }),
          ],
          unreadCount: 1,
        }),
      },
    });

    // Should show unread indicator (translation key in test)
    expect(screen.getByText(/notifications\.groups\.unread/i)).toBeInTheDocument();
  });

  it('calls onMarkAllRead when mark all read button clicked', async () => {
    const onMarkAllRead = vi.fn();

    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          notifications: [
            createMockNotification({ id: 'a', read: false }),
            createMockNotification({ id: 'b', read: false }),
          ],
          unreadCount: 2,
        }),
        onMarkAllRead,
      },
    });

    const markAllButton = screen.getByLabelText(/mark.*read/i);
    await fireEvent.click(markAllButton);

    expect(onMarkAllRead).toHaveBeenCalledWith(['a', 'b']);
  });

  it('calls onDismissAll when dismiss all button clicked', async () => {
    const onDismissAll = vi.fn();

    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          notifications: [
            createMockNotification({ id: 'x' }),
            createMockNotification({ id: 'y' }),
          ],
        }),
        onDismissAll,
      },
    });

    const dismissButton = screen.getByLabelText(/dismiss.*all/i);
    await fireEvent.click(dismissButton);

    expect(onDismissAll).toHaveBeenCalledWith(['x', 'y']);
  });

  it('calls onMarkAsRead when individual mark as read button clicked', async () => {
    const onMarkAsRead = vi.fn();

    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          notifications: [createMockNotification({ id: 'test-id', read: false })],
        }),
        defaultOpen: true,
        onMarkAsRead,
      },
    });

    const markButton = screen.getByLabelText(/mark.*read/i);
    await fireEvent.click(markButton);

    expect(onMarkAsRead).toHaveBeenCalledWith('test-id');
  });

  it('calls onDelete when delete button clicked', async () => {
    const onDelete = vi.fn();

    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          notifications: [createMockNotification({ id: 'del-id' })],
        }),
        defaultOpen: true,
        onDelete,
      },
    });

    const deleteButton = screen.getByLabelText(/delete/i);
    await fireEvent.click(deleteButton);

    expect(onDelete).toHaveBeenCalledWith('del-id');
  });

  it('calls onAcknowledge when acknowledge button clicked for read notification', async () => {
    const onAcknowledge = vi.fn();

    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          notifications: [createMockNotification({ id: 'ack-id', read: true, status: 'read' })],
          unreadCount: 0,
        }),
        defaultOpen: true,
        onAcknowledge,
      },
    });

    const ackButton = screen.getByLabelText(/acknowledge/i);
    await fireEvent.click(ackButton);

    expect(onAcknowledge).toHaveBeenCalledWith('ack-id');
  });

  it('renders correct icon for error type', () => {
    const { container } = render(NotificationGroup, {
      props: {
        group: createMockGroup({ type: 'error' }),
      },
    });

    // Error type should have error styling
    expect(container.querySelector('.bg-error\\/20')).toBeInTheDocument();
  });

  it('renders correct icon for warning type', () => {
    const { container } = render(NotificationGroup, {
      props: {
        group: createMockGroup({ type: 'warning' }),
      },
    });

    expect(container.querySelector('.bg-warning\\/20')).toBeInTheDocument();
  });

  it('renders correct icon for detection type', () => {
    const { container } = render(NotificationGroup, {
      props: {
        group: createMockGroup({ type: 'detection' }),
      },
    });

    expect(container.querySelector('.bg-success\\/20')).toBeInTheDocument();
  });

  it('handles detection notification click', async () => {
    const onNotificationClick = vi.fn();
    const notification = createMockNotification({
      type: 'detection',
      metadata: { note_id: 123 },
    });

    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          type: 'detection',
          notifications: [notification],
        }),
        defaultOpen: true,
        onNotificationClick,
      },
    });

    // The notification content area should be clickable
    const notificationContent = screen.getByText(notification.message);
    const clickTarget = notificationContent.closest('[role="button"]') ?? notificationContent;
    await fireEvent.click(clickTarget);

    expect(onNotificationClick).toHaveBeenCalledWith(notification);
  });

  it('formats relative time correctly for recent timestamps', () => {
    const notification = createMockNotification({
      timestamp: new Date('2024-01-15T11:55:00Z').toISOString(), // 5 minutes ago
    });

    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          notifications: [notification],
          latestTimestamp: notification.timestamp,
        }),
        defaultOpen: true,
      },
    });

    // Should show time-related translation key (there can be multiple time elements)
    expect(screen.getAllByText(/notifications\.timeAgo\.minutesAgo/i).length).toBeGreaterThan(0);
  });

  it('applies border styling for unread notifications', () => {
    const { container } = render(NotificationGroup, {
      props: {
        group: createMockGroup({
          unreadCount: 1,
          notifications: [createMockNotification({ read: false })],
        }),
      },
    });

    // Should have primary border for unread
    expect(container.querySelector('.border-primary')).toBeInTheDocument();
  });

  it('shows just now for very recent notifications', () => {
    const notification = createMockNotification({
      timestamp: new Date('2024-01-15T11:59:50Z').toISOString(), // 10 seconds ago
    });

    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          notifications: [notification],
          latestTimestamp: notification.timestamp,
        }),
        defaultOpen: true,
      },
    });

    // Should show "just now" translation key (can have multiple)
    expect(screen.getAllByText(/notifications\.timeAgo\.justNow/i).length).toBeGreaterThan(0);
  });

  it('applies custom className', () => {
    const { container } = render(NotificationGroup, {
      props: {
        group: createMockGroup(),
        className: 'custom-notification-group',
      },
    });

    expect(container.querySelector('.custom-notification-group')).toBeInTheDocument();
  });

  it('handles keyboard navigation on clickable notifications', async () => {
    const onNotificationClick = vi.fn();
    const notification = createMockNotification({
      type: 'detection',
      metadata: { note_id: 456 },
    });

    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          type: 'detection',
          notifications: [notification],
        }),
        defaultOpen: true,
        onNotificationClick,
      },
    });

    // Find the notification row by its text content, then the clickable parent
    const notificationText = screen.getByText(notification.message);
    const notificationRow = notificationText.closest('[role="button"]') as Element;
    expect(notificationRow).toBeInTheDocument();

    await fireEvent.keyDown(notificationRow, { key: 'Enter' });
    expect(onNotificationClick).toHaveBeenCalled();
  });

  it('does not show group actions for single notification', () => {
    render(NotificationGroup, {
      props: {
        group: createMockGroup({
          notifications: [createMockNotification()],
        }),
      },
    });

    // Mark all and dismiss all should not appear for single notification
    expect(screen.queryByLabelText(/dismiss.*all/i)).not.toBeInTheDocument();
  });

  it('shows time element for multiple notifications', () => {
    vi.setSystemTime(new Date('2024-01-15T15:00:00Z'));

    const { container } = render(NotificationGroup, {
      props: {
        group: createMockGroup({
          notifications: [
            createMockNotification({ id: '1', timestamp: '2024-01-15T14:00:00Z' }),
            createMockNotification({ id: '2', timestamp: '2024-01-15T13:00:00Z' }),
          ],
          earliestTimestamp: '2024-01-15T13:00:00Z',
          latestTimestamp: '2024-01-15T14:00:00Z',
        }),
      },
    });

    // Should render a time element with the latest timestamp
    const timeElement = container.querySelector('time[datetime="2024-01-15T14:00:00Z"]');
    expect(timeElement).toBeInTheDocument();
  });
});
