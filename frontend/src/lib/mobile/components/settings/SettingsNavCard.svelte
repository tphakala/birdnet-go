<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { navigationIcons } from '$lib/utils/icons';

  interface Props {
    title: string;
    description?: string;
    iconHtml?: string;
    iconClassName?: string;
    onClick?: () => void;
    className?: string;
  }

  let {
    title,
    description,
    iconHtml,
    iconClassName = 'bg-primary/20 text-primary',
    onClick,
    className = '',
  }: Props = $props();
</script>

<button
  class={cn(
    'flex w-full items-center gap-4 p-4 text-left bg-base-100 hover:bg-base-200/50 active:bg-base-200 rounded-lg',
    className
  )}
  onclick={onClick}
>
  {#if iconHtml}
    <div
      class={cn('w-10 h-10 rounded-full flex items-center justify-center shrink-0', iconClassName)}
    >
      {@html iconHtml}
    </div>
  {/if}
  <div class="flex-1 min-w-0">
    <div class="font-medium">{title}</div>
    {#if description}
      <div class="text-sm text-base-content/60 truncate">{description}</div>
    {/if}
  </div>
  <span class="text-base-content/40">
    {@html navigationIcons.chevronRight}
  </span>
</button>
