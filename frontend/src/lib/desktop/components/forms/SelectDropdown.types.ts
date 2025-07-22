export interface SelectOption {
  /** The unique value associated with this option */
  value: string;
  /** The display text shown to users for this option */
  label: string;
  /** Whether this option is disabled and cannot be selected */
  disabled?: boolean;
  /** Optional group name for organizing options into sections */
  group?: string;
  /** Optional icon identifier or CSS class to display alongside the option */
  icon?: string;
  /** Optional additional descriptive text shown below the main label */
  description?: string;
}
