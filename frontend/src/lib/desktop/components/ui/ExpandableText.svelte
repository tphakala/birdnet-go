<!--
  ExpandableText.svelte

  Text clamped to a few lines, with a Show more / Show less toggle that appears only when the
  text is actually cut off.

  Props:
  - text: The text to show
  - lines: Lines shown while collapsed (2, 3 or 4; default 3)
  - subject: Appended to the toggle's accessible name as visually hidden text, so toggles on
    several cards are told apart (for example the card title)
  - className: Classes for the root element
  - textClassName: Classes for the paragraph (size and colour of the text)
  - ...rest: Other HTML attributes, spread onto the root element

  Behaviour:
  - The whole text stays in the DOM and the accessibility tree; collapsing only clamps it.
  - Overflow is measured while collapsed, at mount, when text or lines change, when the
    paragraph is resized (sidebar toggled, window resized) and when a web font finishes loading,
    so the toggle appears or goes away with the layout.
  - A new text starts collapsed, so a stale Show less never stays behind.
-->
<script lang="ts">
  /* global ResizeObserver, FontFaceSet */
  import { untrack } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { safeGet } from '$lib/utils/security';
  import { generateId } from '$lib/utils/uuid';

  type ClampLines = 2 | 3 | 4;

  interface Props extends Omit<HTMLAttributes<HTMLDivElement>, 'class'> {
    text: string;
    /** Lines shown while collapsed. */
    lines?: ClampLines;
    /** Appended to the toggle's accessible name, for example the card title. */
    subject?: string;
    className?: string;
    textClassName?: string;
  }

  let { text, lines = 3, subject, className, textClassName, ...rest }: Props = $props();

  // Literal class names, so Tailwind generates them (a built name like `line-clamp-${n}` is not)
  const CLAMP_CLASSES: Record<ClampLines, string> = {
    2: 'line-clamp-2',
    3: 'line-clamp-3',
    4: 'line-clamp-4',
  };

  // A paragraph that fits can still report a scroll height one pixel above its client height
  // through sub-pixel rounding
  const OVERFLOW_TOLERANCE_PX = 1;

  const textId = generateId('expandable-text');

  let paragraph = $state<HTMLElement>();
  let toggleButton = $state<HTMLElement>();
  let overflowing = $state(false);
  // The text that was expanded; any other text is collapsed, so a new text starts collapsed
  let expandedText = $state<string | null>(null);
  const expanded = $derived(expandedText === text);

  $effect(() => {
    const element = paragraph;
    // Re-measure when the text or the clamp changes
    void text;
    void lines;
    // An unclamped paragraph never overflows: keep the last measurement while expanded
    if (!element || expanded) return;

    const measure = () => {
      const clipped = element.scrollHeight > element.clientHeight + OVERFLOW_TOLERANCE_PX;
      // The toggle goes away with this result: move focus to the text while it still exists
      if (!clipped && document.activeElement === untrack(() => toggleButton)) element.focus();
      overflowing = clipped;
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    // A web font that finishes loading can rewrap the text without resizing the clamped box
    const fonts: FontFaceSet | undefined = document.fonts;
    fonts?.addEventListener('loadingdone', measure);
    return () => {
      observer.disconnect();
      fonts?.removeEventListener('loadingdone', measure);
    };
  });

  // Runs before the DOM update: when the focused toggle is about to go away without a new
  // measurement (a collapse or a new text while nothing is clipped), move focus to the text
  $effect.pre(() => {
    if (overflowing || expanded || !toggleButton) return;
    if (document.activeElement === toggleButton) paragraph?.focus();
  });

  function toggle() {
    expandedText = expanded ? null : text;
  }
</script>

<div {...rest} class={cn(className)}>
  <p
    bind:this={paragraph}
    id={textId}
    tabindex="-1"
    class={cn(
      'rounded-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-primary)]',
      textClassName,
      !expanded && safeGet(CLAMP_CLASSES, lines, CLAMP_CLASSES[3])
    )}
  >
    {text}
  </p>
  {#if overflowing || expanded}
    <button
      bind:this={toggleButton}
      type="button"
      class="inline-flex min-h-6 items-center rounded-sm text-xs font-medium text-[var(--color-base-content)]/80 underline underline-offset-2 hover:text-[var(--color-base-content)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-primary)]"
      aria-expanded={expanded}
      aria-controls={textId}
      onclick={toggle}
    >
      {expanded ? t('common.ui.showLess') : t('common.ui.showMore')}
      {#if subject}<span class="sr-only">&nbsp;{subject}</span>{/if}
    </button>
  {/if}
</div>
