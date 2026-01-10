import ReconnectingEventSource from 'reconnecting-eventsource';
import { toastActions } from './toast';
import type { ToastType, ToastPosition } from './toast';
import { loggers } from '$lib/utils/logger';
import { sanitizeNotificationMessage } from '$lib/utils/notifications';

const logger = loggers.sse;

interface SSENotification {
  message: string;
  type: 'info' | 'success' | 'warning' | 'error';
}

interface SSEToastData {
  id: string;
  message: string;
  type: ToastType;
  duration?: number;
  component?: string;
  timestamp: string;
  action?: {
    label: string;
    url?: string;
    handler?: string;
  };
}

// Callback type for notification events
type NotificationCallback = (notification: Notification) => void;

// Notification type matching NotificationBell's expected format
interface Notification {
  id: string;
  title: string;
  message: string;
  type: 'info' | 'success' | 'warning' | 'error' | 'detection' | 'system';
  priority: 'low' | 'medium' | 'high' | 'critical';
  timestamp: string;
  read: boolean;
  component?: string;
  status?: string;
}

class SSENotificationManager {
  private eventSource: ReconnectingEventSource | null = null;
  private isConnected = false;
  private readonly notificationCallbacks: Set<NotificationCallback> = new Set();

  /**
   * Register a callback to receive raw notification events
   * Used by components like NotificationBell that need the full notification data
   */
  registerNotificationCallback(callback: NotificationCallback): () => void {
    this.notificationCallbacks.add(callback);
    // Return unsubscribe function
    return () => {
      this.notificationCallbacks.delete(callback);
    };
  }

  /**
   * Notify all registered callbacks of a new notification
   */
  private notifyCallbacks(notification: Notification): void {
    this.notificationCallbacks.forEach(callback => {
      try {
        callback(notification);
      } catch (error) {
        logger.error('Error in notification callback', error, {
          component: 'sseNotifications',
          action: 'notifyCallbacks',
        });
      }
    });
  }

  /**
   * Start listening for SSE notifications
   */
  connect(): void {
    if (this.eventSource) {
      return; // Already connected
    }

    try {
      // Create connection to SSE endpoint
      this.eventSource = new ReconnectingEventSource('/api/v2/notifications/stream', {
        max_retry_time: 30000, // Max 30 seconds between reconnection attempts
        withCredentials: true, // Authentication required for notification stream
      });

      this.eventSource.onopen = () => {
        logger.info('SSE notification connection opened');
        this.isConnected = true;
      };

      // Handle general messages (backwards compatibility)
      this.eventSource.onmessage = event => {
        try {
          const notification: SSENotification = JSON.parse(event.data);
          this.handleNotification(notification);
        } catch (error) {
          // Log parsing errors for debugging while ignoring them for backwards compatibility
          logger.warn('SSE general message parsing error (ignored):', error, 'Data:', event.data);
        }
      };

      this.eventSource.onerror = (error: Event) => {
        logger.error('SSE notification error', error, {
          component: 'sseNotifications',
          action: 'connection',
        });
        this.isConnected = false;
        // ReconnectingEventSource handles reconnection automatically
      };

      // Handle connected event
      this.eventSource.addEventListener('connected', (event: Event) => {
        try {
          const messageEvent = event as MessageEvent;
          const data = JSON.parse(messageEvent.data);
          logger.info('SSE connected:', data);
        } catch (error) {
          // Log parsing errors for debugging while ignoring them for connection events
          logger.warn('SSE connected event parsing error (ignored):', error);
        }
      });

      // Handle toast messages
      this.eventSource.addEventListener('toast', (event: Event) => {
        try {
          const messageEvent = event as MessageEvent;
          const toastData: SSEToastData = JSON.parse(messageEvent.data);
          this.handleToast(toastData);
        } catch (error) {
          logger.error('Error processing toast event', error, {
            component: 'sseNotifications',
            action: 'handleToast',
          });
        }
      });

      // Handle heartbeat
      this.eventSource.addEventListener('heartbeat', (event: Event) => {
        try {
          const messageEvent = event as MessageEvent;
          JSON.parse(messageEvent.data);
          // Heartbeat received successfully - could add connection health tracking here
        } catch (error) {
          // Log parsing errors for debugging while ignoring them for heartbeat
          logger.warn('SSE heartbeat parsing error (ignored):', error);
        }
      });

      // Handle notification events for registered callbacks
      this.eventSource.addEventListener('notification', (event: Event) => {
        try {
          const messageEvent = event as MessageEvent;
          const notification: Notification = JSON.parse(messageEvent.data);
          this.notifyCallbacks(notification);
        } catch (error) {
          logger.error('Error processing notification event', error, {
            component: 'sseNotifications',
            action: 'handleNotification',
          });
        }
      });
    } catch (error) {
      logger.error('Failed to create SSE connection', error, {
        component: 'sseNotifications',
        action: 'connect',
      });
      // Try again in 5 seconds
      setTimeout(() => this.connect(), 5000);
    }
  }

  /**
   * Handle incoming SSE notification
   */
  private handleNotification(notification: SSENotification): void {
    // Convert SSE notification to toast
    const toastType = this.mapNotificationType(notification.type);

    // Show toast with appropriate duration
    const duration = notification.type === 'error' ? null : 5000; // Errors don't auto-dismiss

    toastActions.show(sanitizeNotificationMessage(notification.message), toastType, {
      duration,
      showIcon: true,
    });
  }

  /**
   * Handle incoming SSE toast event
   */
  private handleToast(toastData: SSEToastData): void {
    // Show the toast using the toast store
    const actions = toastData.action
      ? [
          {
            label: toastData.action.label,
            onClick: () => {
              if (toastData.action?.url) {
                window.location.href = toastData.action.url;
              } else if (toastData.action?.handler) {
                // Handle custom actions if needed
                logger.debug('Toast action handler:', toastData.action.handler);
              }
            },
          },
        ]
      : undefined;

    toastActions.show(sanitizeNotificationMessage(toastData.message), toastData.type, {
      duration: toastData.duration ?? 5000,
      position: 'top-right' as ToastPosition,
      actions,
    });
  }

  /**
   * Map SSE notification type to toast type
   */
  private mapNotificationType(sseType: string): ToastType {
    switch (sseType) {
      case 'success':
        return 'success';
      case 'warning':
        return 'warning';
      case 'error':
        return 'error';
      case 'info':
      default:
        return 'info';
    }
  }

  /**
   * Disconnect from SSE
   */
  disconnect(): void {
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
      this.isConnected = false;
    }
  }

  /**
   * Check if connected
   */
  getIsConnected(): boolean {
    return this.isConnected;
  }
}

// Create singleton instance
export const sseNotifications = new SSENotificationManager();

// Auto-connect when module is imported
if (typeof window !== 'undefined') {
  // Initialize after a short delay to ensure the app is ready
  setTimeout(() => sseNotifications.connect(), 100);

  // Cleanup on page unload
  window.addEventListener('beforeunload', () => sseNotifications.disconnect());
}
