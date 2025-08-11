import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/svelte';
import { tick } from 'svelte';

// Define interfaces and mock class at module level
interface SSEOptions {
  max_retry_time?: number;
  withCredentials?: boolean;
}

class MockReconnectingEventSource {
  url: string;
  options: SSEOptions | undefined;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;

  constructor(url: string, options?: SSEOptions) {
    this.url = url;
    this.options = options;
    // Store instance for test access
    const global = globalThis as typeof globalThis & {
      mockSSEInstance: MockReconnectingEventSource;
    };
    global.mockSSEInstance = this;
  }

  close() {
    // Mock close
  }

  // Helper method for tests to simulate messages
  simulateMessage(data: Record<string, unknown>) {
    if (this.onmessage) {
      this.onmessage(new MessageEvent('message', { data: JSON.stringify(data) }));
    }
  }
}

// Mock ReconnectingEventSource before any imports that might use it
vi.mock('reconnecting-eventsource', () => {
  // Return the class directly in the factory function
  return {
    default: class {
      url: string;
      options: SSEOptions | undefined;
      onopen: ((event: Event) => void) | null = null;
      onmessage: ((event: MessageEvent) => void) | null = null;
      onerror: ((event: Event) => void) | null = null;

      constructor(url: string, options?: SSEOptions) {
        this.url = url;
        this.options = options;
        // Store instance for test access
        const global = globalThis as typeof globalThis & {
          mockSSEInstance: MockReconnectingEventSource;
        };
        global.mockSSEInstance = this as MockReconnectingEventSource;
      }

      close() {
        // Mock close
      }

      // Helper method for tests to simulate messages
      simulateMessage(data: Record<string, unknown>) {
        if (this.onmessage) {
          this.onmessage(new MessageEvent('message', { data: JSON.stringify(data) }));
        }
      }
    },
  };
});

// Mock dependencies
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn(),
    put: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    response: Response;
    userMessage: string;
    isNetworkError: boolean;
    constructor(message: string, status: number, response: Response, isNetworkError = false) {
      super(message);
      this.name = 'ApiError';
      this.status = status;
      this.response = response;
      this.userMessage = message;
      this.isNetworkError = isNetworkError;
    }
  },
}));

vi.mock('$lib/stores/toast', () => ({
  toastActions: {
    error: vi.fn(),
  },
}));

vi.mock('$lib/utils/logger', () => ({
  loggers: {
    ui: {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
  },
}));

vi.mock('$lib/utils/icons', () => ({
  alertIconsSvg: {
    error: '<svg>error</svg>',
    warning: '<svg>warning</svg>',
    info: '<svg>info</svg>',
  },
  systemIcons: {
    bell: '<svg>bell</svg>',
    bellOff: '<svg>bellOff</svg>',
    star: '<svg>star</svg>',
    settingsGear: '<svg>gear</svg>',
  },
}));

vi.mock('$lib/utils/cn', () => ({
  cn: (...args: Array<string | boolean | undefined | null>) => args.filter(Boolean).join(' '),
}));

import NotificationBell from './NotificationBell.svelte';
import { api, ApiError } from '$lib/utils/api';
import { toastActions } from '$lib/stores/toast';

interface TestNotification {
  id: string;
  type: 'error' | 'warning' | 'info' | 'detection' | 'system';
  title: string;
  message: string;
  timestamp: string;
  read: boolean;
  priority: 'critical' | 'high' | 'medium' | 'low';
  component?: string;
}

describe('NotificationBell Component', () => {
  let mockNotifications: TestNotification[];

  beforeEach(() => {
    // Reset all mocks
    vi.clearAllMocks();

    // Setup localStorage mock
    const localStorageMock = {
      getItem: vi.fn().mockReturnValue('false'),
      setItem: vi.fn(),
      removeItem: vi.fn(),
      clear: vi.fn(),
    };
    Object.defineProperty(globalThis, 'localStorage', {
      value: localStorageMock,
      writable: true,
    });

    // Setup Audio mock
    globalThis.Audio = vi.fn().mockImplementation(() => ({
      play: vi.fn().mockResolvedValue(undefined),
      load: vi.fn(),
      addEventListener: vi.fn(),
      volume: 0.5,
      currentTime: 0,
      preload: 'auto',
    }));

    // Setup Notification mock
    const NotificationMock = vi.fn() as unknown as typeof Notification & {
      permission: NotificationPermission;
      requestPermission: ReturnType<typeof vi.fn>;
    };
    NotificationMock.permission = 'default';
    NotificationMock.requestPermission = vi.fn().mockResolvedValue('granted');
    globalThis.Notification = NotificationMock;

    // Default notifications
    mockNotifications = [
      {
        id: '1',
        type: 'info',
        title: 'Test Notification 1',
        message: 'This is a test message',
        timestamp: new Date().toISOString(),
        read: false,
        priority: 'medium',
        component: 'TestComponent',
      },
      {
        id: '2',
        type: 'warning',
        title: 'Test Warning',
        message: 'This is a warning',
        timestamp: new Date(Date.now() - 3600000).toISOString(), // 1 hour ago
        read: true,
        priority: 'high',
      },
    ];

    // Mock API responses
    vi.mocked(api.get).mockResolvedValue({ notifications: mockNotifications });
    vi.mocked(api.put).mockResolvedValue({});
  });

  afterEach(() => {
    cleanup();
    const global = globalThis as typeof globalThis & { mockSSEInstance?: unknown };
    delete global.mockSSEInstance;
  });

  describe('Basic Functionality', () => {
    it('should render the notification bell button', async () => {
      render(NotificationBell);

      const button = screen.getByRole('button', { name: /notifications/i });
      expect(button).toBeInTheDocument();
    });

    it('should load and display notifications on mount', async () => {
      render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalledWith('/api/v2/notifications?limit=20&status=unread');
      });
    });

    it('should show unread count badge', async () => {
      render(NotificationBell);

      await waitFor(() => {
        const badge = screen.getByText('1'); // One unread notification
        expect(badge).toBeInTheDocument();
      });
    });

    it('should toggle dropdown when bell is clicked', async () => {
      const { container } = render(NotificationBell);

      const button = screen.getByRole('button', { name: /notifications/i });

      // Initially closed
      expect(container.querySelector('#notification-dropdown')).not.toBeInTheDocument();

      // Click to open
      await fireEvent.click(button);
      await tick();

      expect(container.querySelector('#notification-dropdown')).toBeInTheDocument();

      // Click to close
      await fireEvent.click(button);
      await tick();

      expect(container.querySelector('#notification-dropdown')).not.toBeInTheDocument();
    });

    it('should display notifications in dropdown', async () => {
      render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      expect(screen.getByText('Test Notification 1')).toBeInTheDocument();
      expect(screen.getByText('This is a test message')).toBeInTheDocument();
      expect(screen.getByText('Test Warning')).toBeInTheDocument();
    });

    it('should mark notification as read when clicked', async () => {
      render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      const notification = screen.getByText('Test Notification 1').closest('[role="menuitem"]');
      if (notification) {
        await fireEvent.click(notification);
      }

      await waitFor(() => {
        expect(api.put).toHaveBeenCalledWith('/api/v2/notifications/1/read');
      });
    });

    it('should mark all as read', async () => {
      render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      const markAllButton = screen.getByText('Mark all as read');
      await fireEvent.click(markAllButton);

      await waitFor(() => {
        expect(api.put).toHaveBeenCalledWith('/api/v2/notifications/1/read');
      });
    });
  });

  describe('Message Deduplication', () => {
    it('should deduplicate identical notifications', async () => {
      const { container } = render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const global = globalThis as typeof globalThis & {
        mockSSEInstance: MockReconnectingEventSource;
      };
      const sseInstance = global.mockSSEInstance;

      // Send first notification
      const notification1 = {
        eventType: 'notification',
        id: 'new-1',
        type: 'info',
        title: 'Duplicate Test',
        message: 'This message will be duplicated',
        timestamp: new Date().toISOString(),
        read: false,
        priority: 'medium',
      };

      sseInstance.simulateMessage(notification1);
      await tick();

      // Open dropdown to check
      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      const notifications = container.querySelectorAll('[role="menuitem"]');
      const initialCount = notifications.length;

      // Send duplicate notification with different ID and timestamp
      const notification2 = {
        ...notification1,
        id: 'new-2',
        timestamp: new Date(Date.now() + 1000).toISOString(),
      };

      sseInstance.simulateMessage(notification2);
      await tick();

      // Should still have same number of notifications (deduplicated)
      const updatedNotifications = container.querySelectorAll('[role="menuitem"]');
      expect(updatedNotifications.length).toBe(initialCount);

      // Should only see the message once
      const duplicateMessages = screen.getAllByText('This message will be duplicated');
      expect(duplicateMessages.length).toBe(1);
    });

    it('should update timestamp for duplicate notifications', async () => {
      // Set up test with empty mock data to avoid interference
      vi.mocked(api.get).mockResolvedValue({ notifications: [] });

      const { container } = render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const global = globalThis as typeof globalThis & {
        mockSSEInstance: MockReconnectingEventSource;
      };
      const sseInstance = global.mockSSEInstance;

      // Send first notification (1 hour ago)
      const oldTimestamp = new Date(Date.now() - 3600000).toISOString();
      const notification1 = {
        eventType: 'notification',
        id: 'time-1',
        type: 'info',
        title: 'Time Test',
        message: 'Testing timestamp update',
        timestamp: oldTimestamp,
        read: false,
        priority: 'medium',
      };

      sseInstance.simulateMessage(notification1);
      await tick();

      // Open dropdown
      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      // Should show old timestamp initially
      expect(container.querySelector('time')?.textContent).toContain('h ago');

      // Send duplicate with new timestamp
      const newTimestamp = new Date().toISOString();
      const notification2 = {
        ...notification1,
        id: 'time-2',
        timestamp: newTimestamp,
      };

      sseInstance.simulateMessage(notification2);
      await tick();

      // Should show updated timestamp - the notification now has the newer timestamp
      // and should be sorted to maintain newest-first order
      const timeElement = container.querySelector('time');
      expect(timeElement?.textContent).toContain('just now');

      // Verify there's still only one notification with this message (deduplicated)
      const messages = screen.getAllByText('Testing timestamp update');
      expect(messages.length).toBe(1);
    });

    it('should escalate priority for duplicate notifications', async () => {
      render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const global = globalThis as typeof globalThis & {
        mockSSEInstance: MockReconnectingEventSource;
      };
      const sseInstance = global.mockSSEInstance;

      // Send low priority notification
      const notification1 = {
        eventType: 'notification',
        id: 'priority-1',
        type: 'info',
        title: 'Priority Test',
        message: 'Testing priority escalation',
        timestamp: new Date().toISOString(),
        read: false,
        priority: 'low',
      };

      sseInstance.simulateMessage(notification1);
      await tick();

      // Open dropdown
      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      // Check initial priority
      let priorityBadge = screen.getByText('low');
      expect(priorityBadge).toBeInTheDocument();

      // Send duplicate with higher priority
      const notification2 = {
        ...notification1,
        id: 'priority-2',
        priority: 'critical',
      };

      sseInstance.simulateMessage(notification2);
      await tick();

      // Priority should be escalated to critical
      priorityBadge = screen.getByText('critical');
      expect(priorityBadge).toBeInTheDocument();

      // Low priority badge should be gone
      expect(screen.queryByText('low')).not.toBeInTheDocument();
    });

    it('should preserve read status for duplicate notifications', async () => {
      const { container } = render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const global = globalThis as typeof globalThis & {
        mockSSEInstance: MockReconnectingEventSource;
      };
      const sseInstance = global.mockSSEInstance;

      // Send unread notification
      const notification1 = {
        eventType: 'notification',
        id: 'read-1',
        type: 'info',
        title: 'Read Status Test',
        message: 'Testing read status preservation',
        timestamp: new Date().toISOString(),
        read: false,
        priority: 'medium',
      };

      sseInstance.simulateMessage(notification1);
      await tick();

      // Open dropdown and mark as read
      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      const notificationElement = screen.getByText('Read Status Test').closest('[role="menuitem"]');
      if (notificationElement) {
        await fireEvent.click(notificationElement);
      }

      await waitFor(() => {
        expect(api.put).toHaveBeenCalledWith('/api/v2/notifications/read-1/read');
      });

      // Send duplicate notification (would normally be unread)
      const notification2 = {
        ...notification1,
        id: 'read-2',
        read: false, // Trying to set as unread
      };

      // Re-open dropdown
      await fireEvent.click(button);
      await tick();

      sseInstance.simulateMessage(notification2);
      await tick();

      // Should maintain read status (not add to unread count)
      const unreadBadge = container.querySelector('.bg-error');
      const unreadCount = unreadBadge?.textContent;

      // Original test data has 1 unread, we added 1 and marked it read, so should be 1
      expect(unreadCount).toBe('1');
    });

    it('should move deduplicated notifications to top', async () => {
      const { container } = render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const global = globalThis as typeof globalThis & {
        mockSSEInstance: MockReconnectingEventSource;
      };
      const sseInstance = global.mockSSEInstance;

      // Send multiple notifications
      const notifications = [
        {
          eventType: 'notification',
          id: 'pos-1',
          type: 'info',
          title: 'First',
          message: 'First message',
          timestamp: new Date().toISOString(),
          read: false,
          priority: 'medium',
        },
        {
          eventType: 'notification',
          id: 'pos-2',
          type: 'warning',
          title: 'Second',
          message: 'Second message',
          timestamp: new Date().toISOString(),
          read: false,
          priority: 'medium',
        },
        {
          eventType: 'notification',
          id: 'pos-3',
          type: 'error',
          title: 'Third',
          message: 'Third message',
          timestamp: new Date().toISOString(),
          read: false,
          priority: 'medium',
        },
      ];

      // Send all notifications
      for (const notif of notifications) {
        sseInstance.simulateMessage(notif);
        await tick();
      }

      // Open dropdown
      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      // Get initial order
      let notificationElements = container.querySelectorAll('[role="menuitem"] h4');
      expect(notificationElements[0].textContent).toBe('Third'); // Most recent first
      expect(notificationElements[1].textContent).toBe('Second');
      expect(notificationElements[2].textContent).toBe('First');

      // Send duplicate of first notification
      const duplicateFirst = {
        ...notifications[0],
        id: 'pos-1-dup',
        timestamp: new Date(Date.now() + 10000).toISOString(),
      };

      sseInstance.simulateMessage(duplicateFirst);
      await tick();

      // Check new order - First should now be at top
      notificationElements = container.querySelectorAll('[role="menuitem"] h4');
      expect(notificationElements[0].textContent).toBe('First'); // Moved to top
      expect(notificationElements[1].textContent).toBe('Third');
      expect(notificationElements[2].textContent).toBe('Second');
    });

    it('should handle deduplication with different notification types correctly', async () => {
      render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const global = globalThis as typeof globalThis & {
        mockSSEInstance: MockReconnectingEventSource;
      };
      const sseInstance = global.mockSSEInstance;

      // Send notification with type 'info'
      const notification1 = {
        eventType: 'notification',
        id: 'type-1',
        type: 'info',
        title: 'Same Title',
        message: 'Same Message',
        timestamp: new Date().toISOString(),
        read: false,
        priority: 'medium',
      };

      sseInstance.simulateMessage(notification1);
      await tick();

      // Send notification with same title/message but different type
      const notification2 = {
        ...notification1,
        id: 'type-2',
        type: 'error', // Different type
      };

      sseInstance.simulateMessage(notification2);
      await tick();

      // Open dropdown
      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      // Should have both notifications (not deduplicated due to different type)
      const sameMessages = screen.getAllByText('Same Message');
      expect(sameMessages.length).toBe(2);
    });
  });

  describe('SSE Connection', () => {
    it('should handle SSE connection events', async () => {
      render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const global = globalThis as typeof globalThis & {
        mockSSEInstance: MockReconnectingEventSource;
      };
      const sseInstance = global.mockSSEInstance;

      // Test various SSE event types
      const events = [
        { eventType: 'connected', clientId: 'test-client' },
        { eventType: 'heartbeat' },
        { eventType: 'toast', message: 'Toast message' }, // Should be ignored
      ];

      for (const event of events) {
        sseInstance.simulateMessage(event);
        await tick();
      }

      // Toast events should not add notifications
      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      expect(screen.queryByText('Toast message')).not.toBeInTheDocument();
    });

    it('should handle SSE errors gracefully', async () => {
      render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const global = globalThis as typeof globalThis & {
        mockSSEInstance: MockReconnectingEventSource;
      };
      const sseInstance = global.mockSSEInstance;

      // Simulate error
      if (sseInstance.onerror) {
        sseInstance.onerror(new Event('error'));
      }

      // Component should still be functional
      const button = screen.getByRole('button', { name: /notifications/i });
      expect(button).toBeInTheDocument();
    });

    it('should handle malformed SSE messages', async () => {
      render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const global = globalThis as typeof globalThis & {
        mockSSEInstance: MockReconnectingEventSource;
      };
      const sseInstance = global.mockSSEInstance;

      // Send malformed JSON
      if (sseInstance.onmessage) {
        sseInstance.onmessage(new MessageEvent('message', { data: 'invalid json' }));
      }

      await tick();

      // Component should still be functional
      const button = screen.getByRole('button', { name: /notifications/i });
      expect(button).toBeInTheDocument();
    });
  });

  describe('Empty State', () => {
    it('should show empty state when no notifications', async () => {
      vi.mocked(api.get).mockResolvedValue({ notifications: [] });

      render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      expect(screen.getByText('No notifications')).toBeInTheDocument();
    });
  });

  describe('Loading State', () => {
    it('should show loading state while fetching notifications', async () => {
      // Make API call hang
      vi.mocked(api.get).mockImplementation(() => new Promise(() => {}));

      render(NotificationBell);

      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      const loadingElement = screen.getByRole('status');
      expect(loadingElement).toBeInTheDocument();
    });
  });

  describe('Authentication Handling', () => {
    it('should handle unauthenticated users gracefully', async () => {
      const mockResponse = new Response('Unauthorized', { status: 401 });
      const authError = new ApiError('Unauthorized', 401, mockResponse);
      vi.mocked(api.get).mockRejectedValue(authError);

      render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      // Should not show error toast for 401
      expect(toastActions.error).not.toHaveBeenCalled();

      // Component should still render
      const button = screen.getByRole('button', { name: /notifications/i });
      expect(button).toBeInTheDocument();
    });
  });

  describe('Navigation', () => {
    it('should navigate to notifications page when notification clicked', async () => {
      const onNavigate = vi.fn();

      render(NotificationBell, {
        props: { onNavigateToNotifications: onNavigate },
      });

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      const notification = screen.getByText('Test Notification 1').closest('[role="menuitem"]');
      if (notification) {
        await fireEvent.click(notification);
      }

      expect(onNavigate).toHaveBeenCalled();
    });

    it('should navigate to notifications page via View all button', async () => {
      const onNavigate = vi.fn();

      render(NotificationBell, {
        props: { onNavigateToNotifications: onNavigate },
      });

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      const viewAllButton = screen.getByText('View all notifications');
      await fireEvent.click(viewAllButton);

      expect(onNavigate).toHaveBeenCalled();
    });
  });

  describe('Accessibility', () => {
    it('should have proper ARIA attributes', async () => {
      const { container } = render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const button = screen.getByRole('button', { name: /notifications/i });

      expect(button).toHaveAttribute('aria-label', 'Notifications');
      expect(button).toHaveAttribute('aria-expanded', 'false');
      expect(button).toHaveAttribute('aria-haspopup', 'menu');

      await fireEvent.click(button);

      expect(button).toHaveAttribute('aria-expanded', 'true');

      const dropdown = container.querySelector('#notification-dropdown');
      // Role should be 'menu' when there are notifications
      expect(dropdown).toHaveAttribute('role', 'menu');
    });

    it('should support keyboard navigation', async () => {
      render(NotificationBell);

      await waitFor(() => {
        expect(api.get).toHaveBeenCalled();
      });

      const button = screen.getByRole('button', { name: /notifications/i });
      await fireEvent.click(button);

      const notification = screen.getByText('Test Notification 1').closest('[role="menuitem"]');

      // Test Enter key
      if (notification) {
        await fireEvent.keyDown(notification, { key: 'Enter' });
      }

      await waitFor(() => {
        expect(api.put).toHaveBeenCalled();
      });
    });
  });
});
