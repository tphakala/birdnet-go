<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { systemIcons, mediaIcons, navigationIcons, actionIcons } from '$lib/utils/icons';

  interface Detection {
    id: number;
    commonName: string;
    scientificName: string;
    confidence: number;
    date: string;
    time: string;
    thumbnailUrl?: string;
  }

  interface Props {
    detection: Detection;
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
      {#if detection.thumbnailUrl}
        <img
          src={detection.thumbnailUrl}
          alt={detection.commonName}
          class="w-full h-full object-cover"
        />
      {:else}
        <div class="w-full h-full flex items-center justify-center text-base-content/30">
          {@html systemIcons.bird}
        </div>
      {/if}
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
      <!-- Spectrogram placeholder -->
      <div
        class="mt-3 h-24 bg-base-200 rounded-lg flex items-center justify-center text-base-content/30"
      >
        Spectrogram
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
