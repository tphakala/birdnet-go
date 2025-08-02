<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { safeGet } from '$lib/utils/security';
  import { systemIcons } from '$lib/utils/icons';

  type TimeOfDay = 'day' | 'night' | 'sunrise' | 'sunset' | 'dawn' | 'dusk';
  type IconSize = 'sm' | 'md' | 'lg' | 'xl';

  interface Props {
    datetime?: Date | string | number;
    timeOfDay?: TimeOfDay;
    size?: IconSize;
    showTooltip?: boolean;
    className?: string;
    // Allow common HTML attributes but don't extend from a specific element type
    id?: string;
    role?: string;
    'aria-label'?: string;
    'aria-hidden'?: boolean;
    'data-testid'?: string;
    tabindex?: number;
    title?: string;
    onclick?: (_event: MouseEvent) => void;
    onkeydown?: (_event: KeyboardEvent) => void;
  }

  let {
    datetime,
    timeOfDay,
    size = 'md',
    showTooltip = false,
    className = '',
    id,
    role,
    'aria-label': ariaLabel,
    'aria-hidden': ariaHidden,
    'data-testid': dataTestId,
    tabindex,
    title,
    onclick,
    onkeydown,
  }: Props = $props();

  // Calculate time of day from datetime if not provided
  function calculateTimeOfDay(dt: Date | string | number | undefined): TimeOfDay {
    if (!dt) return 'day';

    const date = dt instanceof Date ? dt : new Date(dt);

    // Check if date is invalid and fallback to 'day'
    if (isNaN(date.getTime())) {
      return 'day';
    }

    const hours = date.getHours();
    const minutes = date.getMinutes();
    const timeInMinutes = hours * 60 + minutes;

    // Define time ranges (can be adjusted based on location/season)
    // These are approximate and would ideally be calculated based on actual sunrise/sunset times
    if (timeInMinutes >= 360 && timeInMinutes < 420) return 'dawn'; // 6:00 - 7:00
    if (timeInMinutes >= 420 && timeInMinutes < 480) return 'sunrise'; // 7:00 - 8:00
    if (timeInMinutes >= 480 && timeInMinutes < 1020) return 'day'; // 8:00 - 17:00
    if (timeInMinutes >= 1020 && timeInMinutes < 1080) return 'sunset'; // 17:00 - 18:00
    if (timeInMinutes >= 1080 && timeInMinutes < 1140) return 'dusk'; // 18:00 - 19:00
    return 'night'; // 19:00 - 6:00
  }

  let currentTimeOfDay = $derived(timeOfDay || calculateTimeOfDay(datetime));

  const sizeClasses: Record<IconSize, string> = {
    sm: 'h-4 w-4',
    md: 'h-5 w-5',
    lg: 'h-6 w-6',
    xl: 'h-8 w-8',
  };

  const colorClasses: Record<TimeOfDay, string> = {
    day: 'text-yellow-500',
    night: 'text-indigo-500',
    sunrise: 'text-orange-500',
    sunset: 'text-red-500',
    dawn: 'text-orange-400',
    dusk: 'text-purple-500',
  };

  const tooltipText: Record<TimeOfDay, string> = {
    day: 'Daytime',
    night: 'Nighttime',
    sunrise: 'Sunrise',
    sunset: 'Sunset',
    dawn: 'Dawn',
    dusk: 'Dusk',
  };

  // Create a reactive common attributes object for spreading
  let commonAttrs = $derived({
    id,
    role,
    'aria-label': ariaLabel,
    'aria-hidden': ariaHidden,
    'data-testid': dataTestId,
    tabindex,
    title:
      title || (showTooltip ? safeGet(tooltipText, currentTimeOfDay, 'Unknown time') : undefined),
    onclick,
    onkeydown,
  });

  let svgClasses = $derived(
    cn(
      safeGet(sizeClasses, size, 'h-6 w-6'),
      safeGet(colorClasses, currentTimeOfDay, 'text-base-content'),
      className
    )
  );

  // Normalize dawn to sunrise and dusk to sunset for icon selection
  let iconType = $derived(
    currentTimeOfDay === 'dawn'
      ? 'sunrise'
      : currentTimeOfDay === 'dusk'
        ? 'sunset'
        : currentTimeOfDay
  );
</script>

<div class="inline-flex items-center">
  {#if iconType === 'day'}
    <svg
      xmlns="http://www.w3.org/2000/svg"
      class={svgClasses}
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
      {...commonAttrs}
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z"
      />
    </svg>
  {:else if iconType === 'night'}
    <svg
      xmlns="http://www.w3.org/2000/svg"
      class={svgClasses}
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
      {...commonAttrs}
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z"
      />
    </svg>
  {:else if iconType === 'sunrise'}
    <svg
      xmlns="http://www.w3.org/2000/svg"
      class={svgClasses}
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      {...commonAttrs}
    >
      <path d="M17 18a5 5 0 0 0-10 0"></path>
      <line x1="12" y1="2" x2="12" y2="9"></line>
      <line x1="4.22" y1="10.22" x2="5.64" y2="11.64"></line>
      <line x1="1" y1="18" x2="3" y2="18"></line>
      <line x1="21" y1="18" x2="23" y2="18"></line>
      <line x1="18.36" y1="11.64" x2="19.78" y2="10.22"></line>
      <line x1="23" y1="22" x2="1" y2="22"></line>
      <polyline points="8 6 12 2 16 6"></polyline>
    </svg>
  {:else if iconType === 'sunset'}
    <svg
      xmlns="http://www.w3.org/2000/svg"
      class={svgClasses}
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      {...commonAttrs}
    >
      <path d="M17 18a5 5 0 0 0-10 0"></path>
      <line x1="12" y1="9" x2="12" y2="2"></line>
      <line x1="4.22" y1="10.22" x2="5.64" y2="11.64"></line>
      <line x1="1" y1="18" x2="3" y2="18"></line>
      <line x1="21" y1="18" x2="23" y2="18"></line>
      <line x1="18.36" y1="11.64" x2="19.78" y2="10.22"></line>
      <line x1="23" y1="22" x2="1" y2="22"></line>
      <polyline points="16 5 12 9 8 5"></polyline>
    </svg>
  {:else}
    <!-- Default clock icon for unknown time -->
    <div
      class={cn(safeGet(sizeClasses, size, 'h-6 w-6'), 'text-gray-400', className)}
      {...commonAttrs}
    >
      {@html systemIcons.clock}
    </div>
  {/if}
</div>
