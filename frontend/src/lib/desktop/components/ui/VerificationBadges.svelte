<script lang="ts">
  import StatusPill from './StatusPill.svelte';
  import type { StatusVariant, StatusSize } from './StatusPill.svelte';
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import { CircleCheck, X, Lock, CircleHelp, MessageSquare } from '@lucide/svelte';
  import type { Detection } from '$lib/types/detection.types';

  interface Props {
    detection: Detection;
    size?: StatusSize;
    className?: string;
  }

  let { detection, size = 'sm', className = '' }: Props = $props();

  const iconSizeMap: Record<StatusSize, string> = {
    xs: 'size-2.5',
    sm: 'size-3',
    md: 'size-3.5',
  };

  let iconClass = $derived(iconSizeMap[size]);

  let verificationVariant = $derived<StatusVariant>(
    detection.verified === 'correct'
      ? 'success'
      : detection.verified === 'false_positive'
        ? 'error'
        : 'neutral'
  );

  let verificationLabel = $derived(
    detection.verified === 'correct'
      ? t('common.review.status.verifiedCorrect')
      : detection.verified === 'false_positive'
        ? t('common.review.status.falsePositive')
        : t('common.review.status.notReviewed')
  );

  let hasIcon = $derived(
    detection.verified === 'correct' || detection.verified === 'false_positive'
  );
</script>

<div class={cn('flex flex-wrap items-center gap-1.5', className)} role="status">
  <StatusPill variant={verificationVariant} label={verificationLabel} {size} showDot={!hasIcon}>
    {#snippet leadingIcon()}
      {#if detection.verified === 'correct'}
        <CircleCheck class={iconClass} />
      {:else if detection.verified === 'false_positive'}
        <X class={iconClass} />
      {/if}
    {/snippet}
  </StatusPill>

  {#if detection.locked}
    <StatusPill variant="warning" label={t('common.review.status.locked')} {size} showDot={false}>
      {#snippet leadingIcon()}
        <Lock class={iconClass} />
      {/snippet}
    </StatusPill>
  {/if}

  {#if detection.unlikely}
    <StatusPill variant="warning" label={t('common.review.status.unlikely')} {size} showDot={false}>
      {#snippet leadingIcon()}
        <CircleHelp class={iconClass} />
      {/snippet}
    </StatusPill>
  {/if}

  {#if detection.comments && detection.comments.length > 0}
    <StatusPill variant="info" label={t('common.review.form.comment')} {size} showDot={false}>
      {#snippet leadingIcon()}
        <MessageSquare class={iconClass} />
      {/snippet}
    </StatusPill>
  {/if}
</div>
