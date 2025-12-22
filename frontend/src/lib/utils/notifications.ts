/**
 * Notification deduplication and management utilities
 * Provides consistent deduplication behavior across all notification components
 */

import { getHigherPriority, createNotificationKey, type Priority } from './priority';

// Constant for toast notification title - must match backend ToastNotificationTitle
export const TOAST_NOTIFICATION_TITLE = 'Toast Message';

export interface Notification {
  id: string;
  type: 'error' | 'warning' | 'info' | 'detection' | 'system';
  title: string;
  message: string;
  timestamp: string;
  read: boolean;
  priority: Priority;
  component?: string;
  status?: string;
  metadata?: {
    note_id?: number;
    [key: string]: unknown;
  };
}

/**
 * Type guard to identify toast notifications
 * Toast notifications are ephemeral and should only appear as temporary UI toasts
 */
export function isToastNotification(notification: Notification): boolean {
  // Check if the notification has the standard toast title
  // Note: Backend also filters by metadata.isToast, but metadata isn't sent to frontend
  return notification.title === TOAST_NOTIFICATION_TITLE;
}

/**
 * Determines if a notification should be shown based on filters
 * @param notification - The notification to check
 * @param debugMode - Whether debug mode is enabled (shows all notifications)
 * @param excludeToasts - Whether to exclude toast notifications
 */
export function shouldShowNotification(
  notification: Notification,
  debugMode = false,
  excludeToasts = true
): boolean {
  // Never show toast notifications in persistent views if excludeToasts is true
  if (excludeToasts && isToastNotification(notification)) {
    return false;
  }

  // In debug mode, show all notifications including low priority
  if (debugMode) {
    return true;
  }

  // Backend already filters low priority notifications
  // Frontend receives only medium, high, and critical priority notifications
  // No additional filtering needed here
  return true;
}

/**
 * Runtime validation for notification shape
 */
export function isValidNotification(notification: unknown): notification is Notification {
  return (
    typeof notification === 'object' &&
    notification !== null &&
    'message' in notification &&
    'title' in notification &&
    'type' in notification &&
    typeof (notification as Notification).message === 'string' &&
    typeof (notification as Notification).title === 'string' &&
    typeof (notification as Notification).type === 'string'
  );
}

/**
 * Check if notification already exists in array
 */
export function isExistingNotification(
  notification: Notification,
  existingNotifications: Notification[]
): boolean {
  const notificationKey = createNotificationKey(
    notification.message,
    notification.title,
    notification.type
  );
  return existingNotifications.some(
    n => createNotificationKey(n.message, n.title, n.type) === notificationKey
  );
}

/**
 * Merge and deduplicate notifications arrays
 * @param existingNotifications - Current notifications array
 * @param newNotifications - New notifications to merge
 * @param options - Configuration options
 * @returns Deduplicated and sorted notifications array
 */
export function mergeAndDeduplicateNotifications(
  existingNotifications: Notification[],
  newNotifications: Notification[],
  options: {
    limit?: number;
    debugMode?: boolean;
    excludeToasts?: boolean;
  } = {}
): Notification[] {
  const { limit = 20, debugMode = false, excludeToasts = true } = options;

  // Performance optimization: early return if no new notifications
  if (!newNotifications.length) {
    return existingNotifications;
  }

  const result: Notification[] = [];
  const processedKeys = new Set<string>();

  // Process new notifications first (they get priority)
  for (const newNotification of newNotifications) {
    if (!shouldShowNotification(newNotification, debugMode, excludeToasts)) {
      continue;
    }

    const notificationKey = createNotificationKey(
      newNotification.message,
      newNotification.title,
      newNotification.type
    );

    if (processedKeys.has(notificationKey)) {
      continue; // Skip if we've already processed this key
    }

    // Check for duplicate in existing notifications
    const existingNotification = existingNotifications.find(
      n => createNotificationKey(n.message, n.title, n.type) === notificationKey
    );

    if (existingNotification) {
      // Merge with existing: update timestamp, preserve read status, upgrade priority
      const merged: Notification = {
        ...existingNotification,
        timestamp: newNotification.timestamp,
        read: existingNotification.read, // Preserve read status
        status: existingNotification.status, // Preserve status
        priority: getHigherPriority(existingNotification.priority, newNotification.priority),
      };
      result.push(merged);
    } else {
      // Add new notification
      result.push(newNotification);
    }

    processedKeys.add(notificationKey);
  }

  // Add remaining existing notifications that weren't duplicates
  for (const existing of existingNotifications) {
    const notificationKey = createNotificationKey(existing.message, existing.title, existing.type);

    if (!processedKeys.has(notificationKey)) {
      result.push(existing);
      processedKeys.add(notificationKey);
    }
  }

  // Sort by timestamp (newest first) for deterministic order
  const sortedResult = result.sort((a, b) => {
    const timeA = new Date(a.timestamp).getTime();
    const timeB = new Date(b.timestamp).getTime();
    return timeB - timeA; // Descending order (newest first)
  });

  // Apply limit
  return sortedResult.slice(0, limit);
}

/**
 * Deduplicate a single array of notifications (removes duplicates within the array)
 * Useful for cleaning up notifications fetched from API that may contain duplicates
 */
export function deduplicateNotifications(
  notifications: Notification[],
  options: {
    debugMode?: boolean;
    excludeToasts?: boolean;
  } = {}
): Notification[] {
  const { debugMode = false, excludeToasts = true } = options;

  const deduped: Notification[] = [];
  const seenKeys = new Set<string>();

  for (const notification of notifications) {
    if (!shouldShowNotification(notification, debugMode, excludeToasts)) {
      continue;
    }

    const key = createNotificationKey(notification.message, notification.title, notification.type);

    if (!seenKeys.has(key)) {
      seenKeys.add(key);
      deduped.push(notification);
    }
  }

  return deduped;
}

/**
 * Sanitize notification message for UI display by removing URLs
 * This removes image URLs and detection links that are meant for push notifications
 * but should not be displayed in the UI's bell icon or toast notifications
 * @param message - The message to sanitize
 * @returns The sanitized message with URLs removed
 */
export function sanitizeNotificationMessage(message: string): string {
  if (!message) return '';
  return message
    .split('\n')
    .filter(line => {
      const trimmed = line.trim();
      return !trimmed.startsWith('http://') && !trimmed.startsWith('https://');
    })
    .join('\n')
    .trim();
}

// ============================================================================
// Notification Grouping Utilities
// Groups similar notifications by title + component + type (ignoring message content)
// ============================================================================

/**
 * Group structure for clustered notifications
 */
export interface NotificationGroup {
  key: string;
  title: string;
  type: Notification['type'];
  component?: string;
  notifications: Notification[];
  latestTimestamp: string;
  earliestTimestamp: string;
  unreadCount: number;
  highestPriority: Priority;
}

/**
 * Creates a grouping key for notifications based on title, component, and type
 * This key ignores dynamic message content to group similar notifications
 * @param notification - The notification to create a key for
 * @returns A string key for grouping
 */
export function createGroupingKey(notification: Notification): string {
  const component = notification.component ?? 'unknown';
  return `${notification.title}|${component}|${notification.type}`;
}

/**
 * Groups notifications by title + component + type
 * Returns sorted groups (newest first) with each group's notifications sorted internally
 * @param notifications - Array of notifications to group
 * @returns Array of notification groups sorted by latest timestamp
 */
export function groupNotifications(notifications: Notification[]): NotificationGroup[] {
  if (!notifications.length) return [];

  const groupMap = new Map<string, NotificationGroup>();

  for (const notification of notifications) {
    const key = createGroupingKey(notification);

    if (groupMap.has(key)) {
      const group = groupMap.get(key);
      if (!group) continue; // Type guard (should never happen when has() is true)
      group.notifications.push(notification);

      // Update group metadata
      if (new Date(notification.timestamp) > new Date(group.latestTimestamp)) {
        group.latestTimestamp = notification.timestamp;
      }
      if (new Date(notification.timestamp) < new Date(group.earliestTimestamp)) {
        group.earliestTimestamp = notification.timestamp;
      }
      if (!notification.read) {
        group.unreadCount++;
      }
      group.highestPriority = getHigherPriority(group.highestPriority, notification.priority);
    } else {
      groupMap.set(key, {
        key,
        title: notification.title,
        type: notification.type,
        component: notification.component,
        notifications: [notification],
        latestTimestamp: notification.timestamp,
        earliestTimestamp: notification.timestamp,
        unreadCount: notification.read ? 0 : 1,
        highestPriority: notification.priority,
      });
    }
  }

  // Convert to array, sort groups by latest timestamp (newest first)
  const groups = Array.from(groupMap.values());
  groups.sort(
    (a, b) => new Date(b.latestTimestamp).getTime() - new Date(a.latestTimestamp).getTime()
  );

  // Sort notifications within each group (newest first)
  for (const group of groups) {
    group.notifications.sort(
      (a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime()
    );
  }

  return groups;
}
