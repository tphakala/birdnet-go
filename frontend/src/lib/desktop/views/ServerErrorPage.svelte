<script lang="ts">
  import { cn } from '$lib/utils/cn.js';

  interface SecuritySettings {
    enabled: boolean;
    accessAllowed: boolean;
  }

  interface Props {
    className?: string;
    message?: string;
    stackTrace?: string;
    security?: SecuritySettings;
  }

  let {
    className = '',
    message = 'An internal server error occurred.',
    stackTrace = '',
    security = { enabled: false, accessAllowed: true },
  }: Props = $props();

  let showDetails = $derived(!security.enabled || security.accessAllowed);
</script>

<svelte:head>
  <title>500 - Internal Server Error</title>
</svelte:head>

<div class={cn('min-h-screen bg-base-200 flex items-center justify-center p-4', className)}>
  <div class="text-center p-8 rounded-lg bg-base-100 shadow-lg max-w-4xl w-full">
    <h1 class="text-6xl font-bold text-base-content mb-4">500</h1>
    <h2 class="text-3xl font-semibold text-base-content opacity-70 mb-4">Internal Server Error</h2>

    <!-- Error details including stack trace -->
    <div class="mt-8 text-left">
      <h3 class="text-2xl font-semibold text-base-content mb-2">Error Details</h3>
      <pre
        class="bg-base-200 p-4 rounded-sm overflow-x-auto text-sm text-base-content font-mono">{message}</pre>

      {#if showDetails && stackTrace}
        <h3 class="text-2xl font-semibold text-base-content mt-4 mb-2">Stack Trace</h3>
        <pre
          class="bg-base-200 p-4 rounded-sm overflow-x-auto text-sm text-base-content font-mono">{stackTrace}</pre>
      {/if}
    </div>

    <!-- Link Buttons -->
    <div class="mt-8 space-x-4">
      <a
        href="/"
        class="btn btn-primary normal-case text-base font-semibold transition duration-300"
      >
        Go to Dashboard
      </a>
      {#if showDetails}
        <a
          href="https://github.com/tphakala/birdnet-go/issues"
          class="btn btn-accent normal-case text-base font-semibold transition duration-300"
        >
          Report Issue
        </a>
      {:else}
        <a
          href="/login"
          class="btn btn-secondary normal-case text-base font-semibold transition duration-300"
        >
          Login to View Details
        </a>
      {/if}
    </div>
  </div>
</div>
