import type { Component } from 'svelte';

export interface SelectOption {
  /** The unique value associated with this option */
  value: string;
  /** The display text shown to users for this option */
  label: string;
  /** Whether this option is disabled and cannot be selected */
  disabled?: boolean;
  /** Optional group name for organizing options into sections */
  group?: string;
  /**
   * Optional icon to display alongside the option.
   * Can be a string (emoji, text) or a Svelte component (Lucide icon, custom component)
   */
  icon?: string | Component;
  /** Optional additional descriptive text shown below the main label */
  description?: string;
}

/** Visual style variants for the dropdown trigger */
export type SelectDropdownVariant = 'button' | 'select';
