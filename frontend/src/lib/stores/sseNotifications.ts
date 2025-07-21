import ReconnectingEventSource from 'reconnecting-eventsource';
import { toastActions } from './toast';
import type { ToastType } from './toast';

interface SSENotification {
  message: string;
  type: 'info' | 'success' | 'warning' | 'error';
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
        withCredentials: false,
      });

      this.eventSource.onopen = () => {
        console.log('SSE notification connection opened');
        this.isConnected = true;
      };

      this.eventSource.onmessage = event => {
        try {
          const notification: SSENotification = JSON.parse(event.data);
          this.handleNotification(notification);
        } catch (error) {
          console.error('Failed to parse SSE notification:', error);
        }
      };

      this.eventSource.onerror = (error: Event) => {
        console.error('SSE notification error:', error);
        this.isConnected = false;
        // ReconnectingEventSource handles reconnection automatically
      };
    } catch (error) {
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
  // Only connect in browser environment
  sseNotifications.connect();
}
