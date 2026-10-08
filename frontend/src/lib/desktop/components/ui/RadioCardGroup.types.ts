import type { LucideIcon } from '@lucide/svelte';

interface RadioCardOptionBase<T extends string> {
  /** Unique within the group. */
  value: T;
  label: string;
  description?: string;
  /** Second line in a mono font, for example "Confidence threshold: 0.7". */
  detail?: string;
  /** Small pill beside the label, for example "Recommended". */
  badge?: string;
  icon?: LucideIcon;
}

/** A disabled option must say why (frontend/AGENTS.md, No Ambiguous Disabled States). */
export type RadioCardOption<T extends string = string> = RadioCardOptionBase<T> &
  ({ disabled?: false } | { disabled: true; disabledReason: string });
