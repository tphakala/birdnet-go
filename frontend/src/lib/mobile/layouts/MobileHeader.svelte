<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { navigationIcons } from '$lib/utils/icons';

  interface Props {
    title: string;
    showBack?: boolean;
    onBack?: () => void;
    className?: string;
  }

  let { title, showBack = false, onBack, className = '' }: Props = $props();

  function handleBack() {
    if (onBack) {
      onBack();
    } else {
      window.history.back();
    }
  }
</script>

<header
  class={cn(
    'sticky top-0 z-20 flex h-14 items-center gap-2 bg-base-100 px-4 border-b border-base-200',
    className
  )}
>
  {#if showBack}
    <button onclick={handleBack} class="btn btn-ghost btn-sm btn-square -ml-2" aria-label="Go back">
      {@html navigationIcons.chevronLeft}
    </button>
  {/if}

  <h1 class="flex-1 text-lg font-semibold truncate">
    {title}
  </h1>
</header>
