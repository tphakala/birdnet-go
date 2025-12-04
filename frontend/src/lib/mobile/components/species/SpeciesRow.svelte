<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { systemIcons, navigationIcons } from '$lib/utils/icons';

  interface Species {
    commonName: string;
    scientificName: string;
    count: number;
    thumbnailUrl?: string;
  }

  interface Props {
    species: Species;
    onClick?: () => void;
    className?: string;
  }

  let { species, onClick, className = '' }: Props = $props();

  function formatCount(count: number): string {
    return count.toLocaleString();
  }
</script>

<button
  class={cn(
    'flex w-full items-center gap-3 p-3 text-left hover:bg-base-200/50 active:bg-base-200',
    className
  )}
  onclick={onClick}
>
  <!-- Thumbnail -->
  <div class="w-12 h-12 rounded-lg bg-base-200 flex-shrink-0 overflow-hidden">
    {#if species.thumbnailUrl}
      <img
        src={species.thumbnailUrl}
        alt={species.commonName}
        class="w-full h-full object-cover"
        loading="lazy"
      />
    {:else}
      <div class="w-full h-full flex items-center justify-center text-base-content/30">
        {@html systemIcons.bird}
      </div>
    {/if}
  </div>

  <!-- Info -->
  <div class="flex-1 min-w-0">
    <div class="font-medium truncate">{species.commonName}</div>
    <div class="text-sm text-base-content/60">
      {formatCount(species.count)} detections
    </div>
  </div>

  <!-- Chevron -->
  <span class="text-base-content/40">
    {@html navigationIcons.chevronRight}
  </span>
</button>
