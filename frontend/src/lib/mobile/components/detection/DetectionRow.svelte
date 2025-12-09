<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { mediaIcons, navigationIcons, actionIcons } from '$lib/utils/icons';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';

  // Minimal detection interface for mobile - only fields actually used by this component
  interface MobileDetection {
    id: number;
    commonName: string;
    scientificName: string;
    confidence: number;
    date: string;
    time: string;
  }

  interface Props {
    detection: MobileDetection;
    expanded?: boolean;
    onToggle?: () => void;
    onPlay?: () => void;
    onVerify?: () => void;
    onDismiss?: () => void;
    className?: string;
  }

  let {
    detection,
    expanded = false,
    onToggle,
    onPlay,
    onVerify,
    onDismiss,
    className = '',
  }: Props = $props();

  // Compute species image URL from scientificName
  let speciesImageUrl = $derived(
    `/api/v2/media/species-image?name=${encodeURIComponent(detection.scientificName)}`
  );

  // Compute spectrogram URL from detection ID
  let spectrogramUrl = $derived(`/api/v2/spectrogram/${detection.id}`);

  function formatTime(time: string): string {
    // time is in HH:MM:SS format
    const [hours, minutes] = time.split(':');
    const hour = parseInt(hours, 10);
    const ampm = hour >= 12 ? 'PM' : 'AM';
    const displayHour = hour % 12 || 12;
    return `${displayHour}:${minutes} ${ampm}`;
  }

  function formatConfidence(confidence: number): string {
    return `${Math.round(confidence * 100)}%`;
  }
</script>

<div class={cn('bg-base-100', className)}>
  <!-- Compact Row -->
  <div
    class="flex w-full items-center gap-3 p-3 cursor-pointer"
    onclick={onToggle}
    role="button"
    tabindex="0"
    onkeydown={e => {
      if (e.key === 'Enter' || e.key === ' ') onToggle?.();
    }}
  >
    <!-- Thumbnail -->
    <div class="w-12 h-12 rounded-lg bg-base-200 flex-shrink-0 overflow-hidden">
      <img
        src={speciesImageUrl}
        alt={detection.commonName}
        class="w-full h-full object-cover"
        onerror={handleBirdImageError}
        loading="lazy"
      />
    </div>

    <!-- Info -->
    <div class="flex-1 min-w-0">
      <div class="font-medium truncate">{detection.commonName}</div>
      <div class="text-sm text-base-content/60">
        {formatConfidence(detection.confidence)}
      </div>
    </div>

    <!-- Time -->
    <div class="text-sm text-base-content/60">
      {formatTime(detection.time)}
    </div>

    <!-- Play Button -->
    <button
      class="btn btn-ghost btn-sm btn-square"
      onclick={e => {
        e.stopPropagation();
        onPlay?.();
      }}
      aria-label="Play audio"
    >
      {@html mediaIcons.play}
    </button>

    <!-- Expand Chevron -->
    <span class={cn('transition-transform', expanded && 'rotate-180')}>
      {@html navigationIcons.chevronDown}
    </span>
  </div>

  <!-- Expanded Content -->
  {#if expanded}
    <div class="px-3 pb-3 border-t border-base-200">
      <!-- Spectrogram -->
      <div class="mt-3 h-24 bg-base-200 rounded-lg overflow-hidden">
        <img
          src={spectrogramUrl}
          alt="Spectrogram for {detection.commonName}"
          class="w-full h-full object-cover"
          loading="lazy"
        />
      </div>

      <!-- Action Buttons -->
      <div class="flex gap-2 mt-3">
        <button class="btn btn-success btn-sm flex-1" onclick={onVerify}>
          {@html actionIcons.check}
          Verify
        </button>
        <button class="btn btn-ghost btn-sm flex-1" onclick={onDismiss}>
          {@html navigationIcons.close}
          Dismiss
        </button>
      </div>
    </div>
  {/if}
</div>
