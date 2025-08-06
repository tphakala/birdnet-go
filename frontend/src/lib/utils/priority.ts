/**
 * Priority utilities for notifications and other prioritized items
 */

export type Priority = 'critical' | 'high' | 'medium' | 'low';

// Priority order mapping - higher number = higher priority
export const PRIORITY_ORDER = {
  critical: 4,
  high: 3,
  medium: 2,
  low: 1,
} as const;

/**
 * Determines which priority is higher between two priorities
 * @param priority1 First priority to compare
 * @param priority2 Second priority to compare
 * @returns The higher priority
 */
export function getHigherPriority(priority1: Priority, priority2: Priority): Priority {
  // eslint-disable-next-line security/detect-object-injection
  const p1Value = PRIORITY_ORDER[priority1];
  // eslint-disable-next-line security/detect-object-injection
  const p2Value = PRIORITY_ORDER[priority2];
  return p1Value >= p2Value ? priority1 : priority2;
}

/**
 * Creates a notification deduplication key from notification properties
 * @param message Notification message
 * @param title Notification title
 * @param type Notification type
 * @returns A string key for deduplication lookup
 */
export function createNotificationKey(message: string, title: string, type: string): string {
  return `${message}|${title}|${type}`;
}
