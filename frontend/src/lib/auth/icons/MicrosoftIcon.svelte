<script lang="ts">
  interface Props {
    class?: string;
  }

  let { class: className = '' }: Props = $props();
</script>

<svg class={className} viewBox="0 0 24 24">
  <path fill="#F25022" d="M1 1h10v10H1z" />
  <path fill="#7FBA00" d="M13 1h10v10H13z" />
  <path fill="#00A4EF" d="M1 13h10v10H1z" />
  <path fill="#FFB900" d="M13 13h10v10H13z" />
</svg>
