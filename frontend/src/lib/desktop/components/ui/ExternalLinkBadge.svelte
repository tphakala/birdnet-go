<script lang="ts">
  import { ExternalLink, BookOpen, Leaf, Bird, Globe, AudioLines } from '@lucide/svelte';
  import type { Component } from 'svelte';
  import type { ExternalLink as ExternalLinkData } from '$lib/types/species';

  interface Props {
    link: ExternalLinkData;
  }

  let { link }: Props = $props();

  // Icon hints come from the OpenFauna sources registry / supplementary registry /
  // eBird. Unknown hints fall back to the generic external-link glyph so any future
  // source still renders.
  const ICON_BY_HINT: Record<string, Component> = {
    wikipedia: BookOpen,
    inaturalist: Leaf,
    gbif: Globe,
    ebird: Bird,
    'xeno-canto': AudioLines,
  };

  let Icon = $derived(ICON_BY_HINT[link.icon ?? ''] ?? ExternalLink);
</script>

<a
  href={link.url}
  target="_blank"
  rel="noopener noreferrer"
  class="badge badge-sm badge-ghost gap-1"
>
  <Icon class="h-3 w-3" aria-hidden="true" />
  {link.name}
</a>
