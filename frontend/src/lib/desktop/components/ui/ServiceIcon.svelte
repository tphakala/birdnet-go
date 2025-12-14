<!--
  ServiceIcon Component

  Purpose: Renders notification service icons based on service type.
  This component provides a type-safe way to display service icons
  without using raw HTML injection.

  Props:
  - service: The service type key (discord, telegram, slack, etc.)
  - className: Optional CSS classes for sizing/styling

  @component
-->
<script lang="ts">
  // Import all service icons as raw SVG strings
  import DiscordIcon from '$lib/assets/icons/services/discord.svg?raw';
  import TelegramIcon from '$lib/assets/icons/services/telegram.svg?raw';
  import SlackIcon from '$lib/assets/icons/services/slack.svg?raw';
  import NtfyIcon from '$lib/assets/icons/services/ntfy.svg?raw';
  import GotifyIcon from '$lib/assets/icons/services/gotify.svg?raw';
  import PushoverIcon from '$lib/assets/icons/services/pushover.svg?raw';
  import IftttIcon from '$lib/assets/icons/services/ifttt.svg?raw';
  import WebhookIcon from '$lib/assets/icons/services/webhook.svg?raw';
  import CustomIcon from '$lib/assets/icons/services/custom.svg?raw';

  // Service type definition
  export type ServiceType =
    | 'discord'
    | 'telegram'
    | 'ntfy'
    | 'gotify'
    | 'pushover'
    | 'slack'
    | 'ifttt'
    | 'webhook'
    | 'custom';

  interface Props {
    service: ServiceType;
    className?: string;
  }

  let { service, className = 'size-5' }: Props = $props();

  // Map service keys to their SVG content
  const serviceIcons: Record<ServiceType, string> = {
    discord: DiscordIcon,
    telegram: TelegramIcon,
    slack: SlackIcon,
    ntfy: NtfyIcon,
    gotify: GotifyIcon,
    pushover: PushoverIcon,
    ifttt: IftttIcon,
    webhook: WebhookIcon,
    custom: CustomIcon,
  };

  // Get the icon for the current service
  let iconSvg = $derived(serviceIcons[service] || CustomIcon);
</script>

<!--
  Note: We use {@html} here because the SVG icons are static assets
  imported at build time, not user-generated content. The icons are
  trusted and sanitized by the build process.
-->
<span class="{className} shrink-0 [&>svg]:size-full [&>svg]:block">
  {@html iconSvg}
</span>
