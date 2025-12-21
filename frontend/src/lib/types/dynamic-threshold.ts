// Dynamic Threshold Type Definitions
// BG-59: Types for dynamic threshold runtime data and reset controls

/**
 * Represents a single dynamic threshold for a species
 */
export interface DynamicThreshold {
  speciesName: string;
  scientificName: string;
  level: ThresholdLevel;
  currentValue: number;
  baseThreshold: number;
  highConfCount: number;
  expiresAt: string; // ISO 8601 date string
  lastTriggered: string; // ISO 8601 date string
  firstCreated: string; // ISO 8601 date string
  triggerCount: number;
  isActive: boolean;
}

/**
 * Threshold level (0-3)
 * 0 = 100% of base (no adjustment)
 * 1 = 75% of base
 * 2 = 50% of base
 * 3 = 25% of base (minimum)
 */
export type ThresholdLevel = 0 | 1 | 2 | 3;

/**
 * Represents a threshold change event in history
 */
export interface ThresholdEvent {
  id: number;
  speciesName: string;
  previousLevel: ThresholdLevel;
  newLevel: ThresholdLevel;
  previousValue: number;
  newValue: number;
  changeReason: ThresholdChangeReason;
  confidence?: number;
  createdAt: string; // ISO 8601 date string
}

/**
 * Reason for a threshold change
 */
export type ThresholdChangeReason = 'high_confidence' | 'expiry' | 'manual_reset';

/**
 * Aggregate statistics about dynamic thresholds
 */
export interface ThresholdStats {
  totalCount: number;
  activeCount: number;
  atMinimumCount: number;
  levelDistribution: LevelStatItem[];
  validHours: number; // Configured threshold validity period in hours
  minThreshold: number; // Configured minimum threshold value
}

/**
 * Count for a specific level
 */
export interface LevelStatItem {
  level: ThresholdLevel;
  count: number;
}

/**
 * API response for listing thresholds with pagination
 */
export interface ThresholdListResponse {
  data: DynamicThreshold[];
  total: number;
  limit: number;
  offset: number;
}

/**
 * API response for threshold events
 */
export interface ThresholdEventsResponse {
  data: ThresholdEvent[];
  species: string;
  limit: number;
}

/**
 * API response for reset operations
 */
export interface ThresholdResetResponse {
  success: boolean;
  message: string;
  species?: string;
  count?: number;
}

/**
 * Level display configuration
 */
export interface ThresholdLevelDisplay {
  level: ThresholdLevel;
  label: string;
  color: string;
  badgeClass: string;
  progressPercent: number;
}

/**
 * Get display configuration for a threshold level
 */
export function getLevelDisplay(level: ThresholdLevel): ThresholdLevelDisplay {
  const configs: Record<ThresholdLevel, ThresholdLevelDisplay> = {
    0: {
      level: 0,
      label: 'None',
      color: 'gray',
      badgeClass: 'bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-300',
      progressPercent: 0,
    },
    1: {
      level: 1,
      label: 'Low',
      color: 'blue',
      badgeClass: 'bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-300',
      progressPercent: 33,
    },
    2: {
      level: 2,
      label: 'Medium',
      color: 'orange',
      badgeClass: 'bg-orange-100 text-orange-800 dark:bg-orange-900 dark:text-orange-300',
      progressPercent: 66,
    },
    3: {
      level: 3,
      label: 'High',
      color: 'red',
      badgeClass: 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-300',
      progressPercent: 100,
    },
  };
  // Type-safe access: level is constrained to 0 | 1 | 2 | 3
  // eslint-disable-next-line security/detect-object-injection
  return configs[level];
}

/**
 * Get human-readable label for change reason
 */
export function getChangeReasonLabel(reason: ThresholdChangeReason): string {
  const labels: Record<ThresholdChangeReason, string> = {
    high_confidence: 'High confidence detection',
    expiry: 'Threshold expired',
    manual_reset: 'Manual reset',
  };
  // Type-safe access: reason is constrained to ThresholdChangeReason union type
  // eslint-disable-next-line security/detect-object-injection
  return labels[reason];
}

/**
 * Calculate time remaining until threshold expires
 */
export function getTimeRemaining(expiresAt: string): string {
  const now = new Date();
  const expires = new Date(expiresAt);
  const diffMs = expires.getTime() - now.getTime();

  if (diffMs <= 0) {
    return 'Expired';
  }

  const hours = Math.floor(diffMs / (1000 * 60 * 60));
  const minutes = Math.floor((diffMs % (1000 * 60 * 60)) / (1000 * 60));

  if (hours > 24) {
    const days = Math.floor(hours / 24);
    return `${days}d ${hours % 24}h`;
  }
  if (hours > 0) {
    return `${hours}h ${minutes}m`;
  }
  return `${minutes}m`;
}
