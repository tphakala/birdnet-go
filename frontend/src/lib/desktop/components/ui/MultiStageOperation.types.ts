/**
 * Represents a single stage in a multi-stage operation
 */
export interface Stage {
  /** Unique identifier for the stage */
  id: string;
  /** Display title for the stage */
  title: string;
  /** Optional detailed description of what this stage does */
  description?: string;
  /** Current execution status of the stage */
  status: 'pending' | 'in_progress' | 'completed' | 'error' | 'skipped';
  /** Error message if the stage failed */
  error?: string;
  /** Additional status message for the stage */
  message?: string;
  /** Progress percentage (0-100) for stages that support progress tracking */
  progress?: number;
}
