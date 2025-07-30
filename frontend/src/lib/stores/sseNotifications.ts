import ReconnectingEventSource from 'reconnecting-eventsource';
import { toastActions } from './toast';
import type { ToastType, ToastPosition } from './toast';

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

class SSENotificationManager {
  private eventSource: ReconnectingEventSource | null = null;
  private isConnected = false;

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
        // eslint-disable-next-line no-console
        console.log('SSE notification connection opened');
        this.isConnected = true;
      };

      // Handle general messages (backwards compatibility)
      this.eventSource.onmessage = event => {
        try {
          const notification: SSENotification = JSON.parse(event.data);
          this.handleNotification(notification);
        } catch (error) {
          // Log parsing errors for debugging while ignoring them for backwards compatibility
          // eslint-disable-next-line no-console
          console.warn('SSE general message parsing error (ignored):', error, 'Data:', event.data);
        }
      };

      this.eventSource.onerror = (error: Event) => {
        // eslint-disable-next-line no-console
        console.error('SSE notification error:', error);
        this.isConnected = false;
        // ReconnectingEventSource handles reconnection automatically
      };

      // Handle connected event
      this.eventSource.addEventListener('connected', (event: Event) => {
        try {
          const messageEvent = event as MessageEvent;
          const _data = JSON.parse(messageEvent.data);
          // Debug logging - can be removed for production
          // console.log('SSE connected:', data);
        } catch (error) {
          // Log parsing errors for debugging while ignoring them for connection events
          // eslint-disable-next-line no-console
          console.warn('SSE connected event parsing error (ignored):', error);
        }
      });

      // Handle toast messages
      this.eventSource.addEventListener('toast', (event: Event) => {
        try {
          const messageEvent = event as MessageEvent;
          const toastData: SSEToastData = JSON.parse(messageEvent.data);
          this.handleToast(toastData);
        } catch (error) {
          // eslint-disable-next-line no-console
          console.error('Error processing toast event:', error);
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
          // eslint-disable-next-line no-console
          console.warn('SSE heartbeat parsing error (ignored):', error);
        }
      });
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error('Failed to create SSE connection:', error);
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

    toastActions.show(notification.message, toastType, {
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
                // Debug logging - can be removed for production
                // console.log('Toast action handler:', toastData.action.handler);
              }
            },
          },
        ]
      : undefined;

    toastActions.show(toastData.message, toastData.type, {
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
