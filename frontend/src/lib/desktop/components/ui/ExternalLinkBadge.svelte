<script lang="ts">
  import { ExternalLink, BookOpen, Leaf, Bird, Globe, AudioLines } from '@lucide/svelte';
  import type { Component } from 'svelte';
  import type { ExternalLink as ExternalLinkData } from '$lib/types/species';
  import { validateProtocolURL } from '$lib/utils/security';

  interface Props {
    link: ExternalLinkData;
  }

  let { link }: Props = $props();

  // Icon hints come from the OpenFauna sources registry / supplementary registry /
  // eBird. Unknown hints fall back to the generic external-link glyph so any future
  // source still renders.
  //
  // A Map, not a plain object: the hint is server-supplied data, and indexing an object
  // literal walks Object.prototype, so a hint of "constructor"/"toString"/"valueOf"
  // would resolve to a truthy non-component, defeat the `??` fallback, and throw when
  // rendered — taking the whole guide panel down instead of degrading to the generic
  // glyph. Matches seasonHighlight's SEASON_ICON and the modal's TAXONOMY_INDENT.
  const ICON_BY_HINT = new Map<string, Component>([
    ['wikipedia', BookOpen],
    ['inaturalist', Leaf],
    ['gbif', Globe],
    ['ebird', Bird],
    ['xeno-canto', AudioLines],
  ]);

  let Icon = $derived(ICON_BY_HINT.get(link.icon ?? '') ?? ExternalLink);

  // Defense in depth: link URLs come from the server-side sources registry, but a
  // javascript:/data: URL must never reach href (matches BirdThumbnailPopup's
  // handling of attribution URLs). An invalid URL suppresses the badge entirely —
  // a link that goes nowhere is worse than no badge.
  let safeUrl = $derived(validateProtocolURL(link.url, ['http', 'https']) ? link.url : undefined);
</script>

{#if safeUrl}
  <a
    href={safeUrl}
    target="_blank"
    rel="noopener noreferrer"
    class="badge badge-sm badge-ghost gap-1"
  >
    <Icon class="h-3 w-3" aria-hidden="true" />
    {link.name}
  </a>
{/if}
