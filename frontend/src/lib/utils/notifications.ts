/**
 * Notification deduplication and management utilities
 * Provides consistent deduplication behavior across all notification components
 */

import { getHigherPriority, createNotificationKey, type Priority } from './priority';
import { t } from '$lib/i18n';

/**
 * Window event the bell and the notifications page listen on (and dispatch) for
 * deletes, whether made in this tab or pushed by the server (notification_deleted).
 */
export const NOTIFICATION_DELETED_WINDOW_EVENT = 'notification-deleted';

/**
 * Returns the list without the notification with the given id and whether any
 * unread one remains. An id that is not in the list returns the list unchanged.
 */
export function removeNotificationById(
  notifications: Notification[],
  id: string
): { notifications: Notification[]; hasUnread: boolean } {
  const remaining = notifications.some(n => n.id === id)
    ? notifications.filter(n => n.id !== id)
    : notifications;
  return { notifications: remaining, hasUnread: remaining.some(n => !n.read) };
}

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
  title_key?: string;
  title_params?: Record<string, unknown>;
  message_key?: string;
  message_params?: Record<string, unknown>;
  metadata?: {
    note_id?: number;
    [key: string]: unknown;
  };
}

/**
 * API notification type - matches backend response format
 * The backend sends 'status' field instead of 'read' boolean
 */
export interface ApiNotification {
  id: string;
  type: 'error' | 'warning' | 'info' | 'detection' | 'system';
  title: string;
  message: string;
  timestamp: string;
  priority: Priority;
  status: 'unread' | 'read' | 'acknowledged';
  component?: string;
  title_key?: string;
  title_params?: Record<string, unknown>;
  message_key?: string;
  message_params?: Record<string, unknown>;
  metadata?: {
    note_id?: number;
    [key: string]: unknown;
  };
}

/**
 * Input type for mapping function - accepts both API format and frontend format
 * This is intentionally permissive to handle various notification sources:
 * - Real API responses (have status, no read)
 * - Test mocks (have read, may not have status)
 * - Pre-mapped notifications (have both)
 * - SSE notifications (have status, no read)
 */
type NotificationInput = {
  id: string;
  type: 'error' | 'warning' | 'info' | 'detection' | 'system';
  title: string;
  message: string;
  timestamp: string;
  priority: Priority;
  status?: string;
  read?: boolean;
  component?: string;
  title_key?: string;
  title_params?: Record<string, unknown>;
  message_key?: string;
  message_params?: Record<string, unknown>;
  metadata?: {
    note_id?: number;
    [key: string]: unknown;
  };
};

/**
 * Maps a notification from API format to frontend format
 * The backend sends 'status' (string) but frontend expects 'read' (boolean)
 *
 * This function handles three cases:
 * 1. Already mapped: has 'read' field already set, keep it
 * 2. API response: has 'status' field, derive 'read' from it
 * 3. Neither: default to unread (read: false)
 *
 * @param notification - Notification from API response or pre-mapped notification
 * @returns Notification with 'read' boolean derived from 'status'
 */
export function mapApiNotification(notification: NotificationInput): Notification {
  // If 'read' is already defined (boolean), preserve it
  // This handles test mocks and already-mapped notifications
  if (typeof notification.read === 'boolean') {
    return notification as Notification;
  }

  // Otherwise, derive 'read' from 'status'
  // 'unread' or undefined -> read: false, 'read'/'acknowledged' -> read: true
  return {
    ...notification,
    read: notification.status === 'read' || notification.status === 'acknowledged',
  } as Notification;
}

/**
 * Maps an array of notifications from API format to frontend format
 *
 * @param notifications - Array of notifications from API response or pre-mapped notifications
 * @returns Array of notifications with 'read' boolean derived from 'status'
 */
export function mapApiNotifications(notifications: NotificationInput[]): Notification[] {
  return notifications.map(mapApiNotification);
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
 * @param _debugMode - Reserved for future debug-level filtering
 * @param excludeToasts - Whether to exclude toast notifications
 */
export function shouldShowNotification(
  notification: Notification,
  _debugMode = false,
  excludeToasts = true
): boolean {
  // Never show toast notifications in persistent views if excludeToasts is true
  if (excludeToasts && isToastNotification(notification)) {
    return false;
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
 * Merge and deduplicate notifications arrays.
 *
 * Entries are keyed by id: a new notification with the id of an existing one
 * refreshes it (timestamp, higher priority) and keeps the local read state. A
 * new notification whose text matches an existing entry with a DIFFERENT id
 * collapses into one row that carries the NEW id, so the row always points at
 * the live notification (a superseded id may already be deleted on the server,
 * and acting on it would 404), while the read state and the higher priority
 * carry over so a repeat of already-read text does not re-alert.
 *
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

  const existingById = new Map(existingNotifications.map(n => [n.id, n]));
  const textKeyOf = (n: Notification) => createNotificationKey(n.message, n.title, n.type);

  const result: Notification[] = [];
  const takenIds = new Set<string>();
  const takenTextKeys = new Set<string>();

  // Process new notifications first (they get priority)
  for (const newNotification of newNotifications) {
    if (!shouldShowNotification(newNotification, debugMode, excludeToasts)) {
      continue;
    }

    const textKey = textKeyOf(newNotification);
    if (takenIds.has(newNotification.id) || takenTextKeys.has(textKey)) {
      continue; // Already represented by an earlier (newer) entry
    }

    const sameId = existingById.get(newNotification.id);
    if (sameId) {
      // Same notification: update timestamp, preserve read status, upgrade priority
      result.push({
        ...sameId,
        timestamp: newNotification.timestamp,
        read: sameId.read,
        status: sameId.status,
        priority: getHigherPriority(sameId.priority, newNotification.priority),
      });
    } else {
      // New id. If it repeats an existing entry's text, it supersedes that entry
      // (dropped below via takenTextKeys), keeping its read state and priority.
      const sameText = existingNotifications.find(n => textKeyOf(n) === textKey);
      result.push(
        sameText
          ? {
              ...newNotification,
              read: sameText.read,
              status: sameText.status,
              priority: getHigherPriority(sameText.priority, newNotification.priority),
            }
          : newNotification
      );
    }

    takenIds.add(newNotification.id);
    takenTextKeys.add(textKey);
  }

  // Add remaining existing notifications that were not merged or superseded
  for (const existing of existingNotifications) {
    const textKey = textKeyOf(existing);
    if (takenIds.has(existing.id) || takenTextKeys.has(textKey)) {
      continue;
    }
    result.push(existing);
    takenIds.add(existing.id);
    takenTextKeys.add(textKey);
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
// Notification Translation Helpers
// Translates notification fields using i18n keys with English fallback
// ============================================================================

/**
 * Safely translate a field: use t(key, params) if the key resolves to
 * something other than itself; otherwise fall back to the English text.
 *
 * t() returns the raw key when translations haven't loaded or key is missing,
 * so we detect this and use the fallback instead of showing a dot-notation key.
 */
export function translateField(
  key: string | undefined,
  params: Record<string, unknown> | undefined,
  fallback: string
): string {
  if (!key) return fallback;
  const translated = t(key, params ?? {});
  // t() returns the raw key when translations haven't loaded or key is missing
  return translated === key ? fallback : translated;
}

/**
 * Translate a notification's title and message using i18n keys.
 * Falls back to the English title/message fields when keys are absent
 * or when translations haven't loaded yet.
 */
export function translateNotification(notification: Notification): {
  title: string;
  message: string;
} {
  // Resolve nested translation keys within title params (e.g. rule_name_key)
  // so the rule name itself is translated before being substituted into the title.
  let titleParams = notification.title_params;
  if (
    titleParams &&
    typeof titleParams.rule_name_key === 'string' &&
    typeof titleParams.rule_name === 'string'
  ) {
    const translatedName = translateField(
      titleParams.rule_name_key,
      undefined,
      titleParams.rule_name
    );
    titleParams = { ...titleParams, rule_name: translatedName };
  }

  return {
    title: translateField(notification.title_key, titleParams, notification.title),
    message: translateField(
      notification.message_key,
      notification.message_params,
      notification.message
    ),
  };
}

// ============================================================================
// Notification Context Display
// Extracts displayable context from notification metadata for error details
// ============================================================================

/** Internal metadata keys that should never be shown as context */
const INTERNAL_METADATA_KEYS = new Set([
  // Notification system internals
  'note_id',
  'error_count',
  'first_occurrence',
  'last_occurrence',
  // Toast notification metadata
  'isToast',
  'toastType',
  'toastId',
  'duration',
  'action',
  // Stream worker metadata
  'streamInfo',
]);

/** Detection-specific metadata keys hidden only for detection notifications
 *  (these fields are already displayed elsewhere in the detection UI) */
const DETECTION_METADATA_KEYS = new Set([
  'is_new_species',
  'species',
  'scientific_name',
  'confidence',
  'location',
  'days_since_first_seen',
]);

/**
 * Extracts displayable context entries from notification metadata.
 * Filters out internal keys and detection-specific keys (only for detection
 * notifications where those fields are shown elsewhere in the UI).
 * For error/warning notifications, fields like scientific_name are shown
 * as they provide critical diagnostic context.
 *
 * @param metadata - The notification metadata object
 * @param notificationType - The notification type (e.g. 'error', 'detection')
 * @returns Array of {key, value} pairs for display, or empty array
 */
export function getDisplayableContext(
  metadata: Record<string, unknown> | undefined,
  notificationType?: Notification['type']
): { key: string; value: string }[] {
  if (!metadata) return [];

  const entries: { key: string; value: string }[] = [];
  for (const [key, value] of Object.entries(metadata)) {
    if (
      INTERNAL_METADATA_KEYS.has(key) ||
      (notificationType === 'detection' && DETECTION_METADATA_KEYS.has(key)) ||
      key.startsWith('bg_') ||
      value === undefined ||
      value === null
    ) {
      continue;
    }
    // Skip objects/arrays — only display scalar values
    if (typeof value === 'object') {
      continue;
    }
    entries.push({
      key: key.replace(/_/g, ' '),
      value: String(value),
    });
  }
  return entries;
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
