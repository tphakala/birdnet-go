<!--
  SettingsNote Component
  
  A reusable informational note component for settings pages that matches 
  the consistent styling used throughout the application.
  
  Features:
  - Standard note styling (mt-4 p-4 bg-base-200 text-sm rounded-lg)
  - Support for rich content via Svelte 5 snippets
  - Optional icon support
  - Consistent spacing and typography
  - Theme-compatible background colors
  - Semantic HTML structure
  
  Props:
  - className: Additional CSS classes (optional)
  - icon: Optional SVG icon snippet
  - children: Main content snippet (required)
  
  Usage:
  <SettingsNote>
    <p>Your note content here...</p>
    <p class="mt-2">Additional content with <a href="..." class="link link-primary">links</a>.</p>
  </SettingsNote>
-->
<script lang="ts">
  interface Props {
    className?: string;
    icon?: import('svelte').Snippet;
    children: import('svelte').Snippet;
  }

  let { className = '', icon, children }: Props = $props();
</script>

<div class="mt-4 p-4 bg-base-200 text-sm rounded-lg {className}">
  {#if icon}
    <div class="flex gap-3">
      <div class="flex-shrink-0">
        {@render icon()}
      </div>
      <div class="flex-1">
        {@render children()}
      </div>
    </div>
  {:else}
    <div>
      {@render children()}
    </div>
  {/if}
</div>
