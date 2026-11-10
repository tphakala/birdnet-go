<script lang="ts">
  import type { ComponentProps } from 'svelte';
  import Card from './Card.svelte';

  interface Props {
    title?: string;
    variant?: ComponentProps<typeof Card>['variant'];
    showHeader?: boolean;
    showFooter?: boolean;
    actionLabel?: string;
  }

  let {
    title = '',
    variant = 'default',
    showHeader = false,
    showFooter = false,
    actionLabel = '',
  }: Props = $props();
</script>

{#snippet header()}
  <div>Custom Header</div>
{/snippet}

{#snippet headerActions()}
  <button type="button">{actionLabel}</button>
{/snippet}

{#snippet footer()}
  <div>Footer content</div>
{/snippet}

<Card
  {title}
  {variant}
  header={showHeader ? header : undefined}
  headerActions={actionLabel ? headerActions : undefined}
  footer={showFooter ? footer : undefined}
>
  Card content
</Card>
