import ReconnectingEventSource from 'reconnecting-eventsource';
import { toastActions } from './toast';
import type { ToastType, ToastPosition } from './toast';
import { loggers } from '$lib/utils/logger';
import { buildAppUrl, isRelativePath } from '$lib/utils/urlHelpers';
import {
  sanitizeNotificationMessage,
  isValidNotification,
  type Notification,
} from '$lib/utils/notifications';

const logger = loggers.sse;

// SSE connection configuration
const SSE_ENDPOINT = '/api/v2/notifications/stream';
const SSE_MAX_RETRY_MS = 30000;
const SSE_RETRY_DELAY_MS = 5000;
const SSE_INIT_DELAY_MS = 100;
const TOAST_DEFAULT_DURATION_MS = 5000;
const TOAST_DEFAULT_POSITION: ToastPosition = 'top-right';

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
      this.eventSource = new ReconnectingEventSource(SSE_ENDPOINT, {
        max_retry_time: SSE_MAX_RETRY_MS,
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
          // Avoid logging raw payload to prevent leaking sensitive content
          logger.warn('SSE general message parsing error (ignored)', error, {
            component: 'sseNotifications',
            action: 'onmessage',
            dataLength: typeof event.data === 'string' ? event.data.length : undefined,
          });
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
        const messageEvent = event as MessageEvent;
        logger.info('SSE connected:', messageEvent.data);
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

      // Handle notification events for registered callbacks
      this.eventSource.addEventListener('notification', (event: Event) => {
        try {
          const messageEvent = event as MessageEvent;
          const parsed: unknown = JSON.parse(messageEvent.data);
          if (!isValidNotification(parsed)) {
            logger.warn('Invalid notification event payload (ignored)', null, {
              component: 'sseNotifications',
              action: 'handleNotification',
            });
            return;
          }
          this.notifyCallbacks(parsed);
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
      // Retry after SSE_RETRY_DELAY_MS
      setTimeout(() => this.connect(), SSE_RETRY_DELAY_MS);
    }
  }

  /**
   * Handle incoming SSE notification
   */
  private handleNotification(notification: SSENotification): void {
    // Convert SSE notification to toast
    const toastType = this.mapNotificationType(notification.type);

    // Show toast with appropriate duration
    const duration = notification.type === 'error' ? null : TOAST_DEFAULT_DURATION_MS;

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
                // Use buildAppUrl for internal URLs to support reverse proxy scenarios
                const url = toastData.action.url;
                window.location.href = isRelativePath(url) ? buildAppUrl(url) : url;
              } else if (toastData.action?.handler) {
                // Handle custom actions if needed
                logger.debug('Toast action handler:', toastData.action.handler);
              }
            },
          },
        ]
      : undefined;

    toastActions.show(sanitizeNotificationMessage(toastData.message), toastData.type, {
      duration: toastData.duration ?? TOAST_DEFAULT_DURATION_MS,
      position: TOAST_DEFAULT_POSITION,
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
  setTimeout(() => sseNotifications.connect(), SSE_INIT_DELAY_MS);

  // Cleanup on page unload
  window.addEventListener('beforeunload', () => sseNotifications.disconnect());
}
