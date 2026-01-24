<!--
  Merlin Page Component
  
  Purpose: Displays information about BirdNET-Go including overview, key features,
  credits, technology stack, license, and version information.
  
  Features:
  - Live spectrogram of audio input stream
  - Merlin-like dashboard that updates every second
  
  Props: None - This is a standalone page component
  
  Usage:
  This component is rendered as a page view in the main application router.
  It provides static content with internationalization support via the t() function.
  
  @component
-->
<script lang="ts">
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import { Star, Bird, Globe, Info, Clock, FileText, CircleCheck, User } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  // TypeScript interface for version settings
  interface VersionSettings {
    version: string;
    buildDate: string;
  }

  // PERFORMANCE OPTIMIZATION: Removed unnecessary $effect() that called empty fetchVersionInfo()
  // In Svelte 5, $effect() runs on every component mount and when dependencies change.
  // Calling empty functions creates unnecessary reactive overhead.

  // Settings data (version info) - will use fallback translations for now
  let settings = $state<VersionSettings>({
    version: '',
    buildDate: '',
  });
</script>

<div class="col-span-12 space-y-4">
    
  <Card title="Sound ID" className="bg-base-100 shadow-sm">
  </Card>

</div>
