<!--
  Notifications Settings Page Component

  Purpose: Configure notification testing and debugging features for BirdNET-Go
  including test notification generation for new species detections and push
  notification provider management.

  Features:
  - Test new species notification generation
  - Notification template customization
  - Push notification provider management (Shoutrrr)
  - Provider add/edit/delete functionality
  - Notification type filtering per provider

  Props: None - This is a page component that uses global settings stores

  Performance Optimizations:
  - API state management for notification testing
  - Reactive change detection with $derived
  - Progress tracking for test notification generation

  @component
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import SettingsButton from '$lib/desktop/features/settings/components/SettingsButton.svelte';
  import {
    Info,
    CircleCheck,
    XCircle,
    Bell,
    Send,
    Plus,
    Pencil,
    Trash2,
    ExternalLink,
  } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import ServiceIcon from '$lib/desktop/components/ui/ServiceIcon.svelte';
  import type { ServiceType } from '$lib/desktop/components/ui/ServiceIcon.svelte';
  import type {
    PushProviderConfig,
    PushSettings,
    NotificationSettings,
    WebhookEndpointConfig,
    WebhookAuthConfig,
  } from '$lib/stores/settings';
  import { safeArrayAccess, safeRegexTest } from '$lib/utils/security';
  import { api, ApiError } from '$lib/utils/api';

  // Template settings state
  let templateConfig = $state<{
    title: string;
    message: string;
  } | null>(null);
  let loadingTemplate = $state(false);
  let savingTemplate = $state(false);
  let templateStatusMessage = $state('');
  let templateStatusType = $state<'info' | 'success' | 'error'>('info');

  let editedTitle = $state('');
  let editedMessage = $state('');

  let hasTemplateChanges = $derived(
    templateConfig !== null &&
      (editedTitle !== templateConfig.title || editedMessage !== templateConfig.message)
  );

  let generating = $state(false);
  let statusMessage = $state('');
  let statusType = $state<'info' | 'success' | 'error'>('info');

  // Push settings state
  let pushSettings = $state<PushSettings>({
    enabled: false,
    providers: [],
    minConfidenceThreshold: 0,
    speciesCooldownMinutes: 0,
  });
  let originalPushSettings = $state<PushSettings | null>(null);
  let loadingPush = $state(false);
  let savingPush = $state(false);
  let pushStatusMessage = $state('');
  let pushStatusType = $state<'info' | 'success' | 'error'>('info');

  // Service-specific form data
  interface ServiceFormData {
    // Discord
    discordWebhookUrl: string;
    // Telegram
    telegramBotToken: string;
    telegramChatId: string;
    // ntfy
    ntfyServer: string;
    ntfyTopic: string;
    ntfyProtocol: 'https' | 'http';
    ntfyUsername: string;
    ntfyPassword: string;
    ntfyCheckHost: string; // host value at time probe was triggered (for race guard)
    ntfyCheckStatus: 'idle' | 'checking' | 'https' | 'http' | 'unreachable';
    // Gotify
    gotifyServer: string;
    gotifyToken: string;
    // Pushover
    pushoverApiToken: string;
    pushoverUserKey: string;
    // Slack
    slackWebhookUrl: string;
    // IFTTT
    iftttWebhookKey: string;
    iftttEventName: string;
    // Webhook
    webhookUrl: string;
    webhookMethod: 'POST' | 'PUT' | 'PATCH';
    webhookAuthType: 'none' | 'bearer' | 'basic';
    webhookBearerToken: string;
    webhookBasicUser: string;
    webhookBasicPass: string;
    // Custom (raw shoutrrr URL)
    customUrl: string;
  }

  // Provider form state
  let showProviderForm = $state(false);
  let editingProviderIndex = $state<number | null>(null);
  let selectedService = $state<ServiceType>('discord');
  let providerFormData = $state<{
    name: string;
    urls: string;
    enabled: boolean;
    filterTypes: string[];
  }>({
    name: '',
    urls: '',
    enabled: true,
    filterTypes: ['detection'],
  });
  let serviceFormData = $state<ServiceFormData>({
    discordWebhookUrl: '',
    telegramBotToken: '',
    telegramChatId: '',
    ntfyServer: 'ntfy.sh',
    ntfyTopic: '',
    ntfyProtocol: 'https',
    ntfyUsername: '',
    ntfyPassword: '',
    ntfyCheckHost: '',
    ntfyCheckStatus: 'idle',
    gotifyServer: '',
    gotifyToken: '',
    pushoverApiToken: '',
    pushoverUserKey: '',
    slackWebhookUrl: '',
    iftttWebhookKey: '',
    iftttEventName: '',
    webhookUrl: '',
    webhookMethod: 'POST',
    webhookAuthType: 'none',
    webhookBearerToken: '',
    webhookBasicUser: '',
    webhookBasicPass: '',
    customUrl: '',
  });
  let testingProvider = $state(false);

  // Available services for the dropdown - extends SelectOption with serviceId for icon rendering
  interface ServiceOption extends SelectOption {
    serviceId: ServiceType;
  }

  const availableServices: ServiceOption[] = [
    { value: 'discord', label: 'Discord', serviceId: 'discord' },
    { value: 'telegram', label: 'Telegram', serviceId: 'telegram' },
    { value: 'slack', label: 'Slack', serviceId: 'slack' },
    { value: 'ntfy', label: 'ntfy', serviceId: 'ntfy' },
    { value: 'gotify', label: 'Gotify', serviceId: 'gotify' },
    { value: 'pushover', label: 'Pushover', serviceId: 'pushover' },
    { value: 'ifttt', label: 'IFTTT', serviceId: 'ifttt' },
    { value: 'webhook', label: 'Webhook', serviceId: 'webhook' },
    { value: 'custom', label: 'Custom URL', serviceId: 'custom' },
  ];

  // Webhook method options
  const webhookMethodOptions = $derived([
    { value: 'POST', label: 'POST' },
    { value: 'PUT', label: 'PUT' },
    { value: 'PATCH', label: 'PATCH' },
  ]);

  // Webhook auth type options
  const webhookAuthOptions = $derived([
    { value: 'none', label: t('settings.notifications.push.services.webhook.auth.none') },
    { value: 'bearer', label: t('settings.notifications.push.services.webhook.auth.bearer') },
    { value: 'basic', label: t('settings.notifications.push.services.webhook.auth.basic') },
  ]);

  // NTFY protocol options
  const ntfyProtocolOptions = $derived([
    { value: 'https', label: 'HTTPS' },
    { value: 'http', label: 'HTTP' },
  ]);

  /** Wraps a bare IPv6 address in brackets for use in URLs. */
  function normalizeNtfyHost(host: string): string {
    const trimmed = host.trim();
    // Only wrap in brackets for bare IPv6 addresses (2+ colons).
    // A single colon means host:port (e.g. 192.168.1.100:8080) — don't wrap.
    const colonCount = (trimmed.match(/:/g) || []).length;
    if (colonCount >= 2 && !trimmed.startsWith('[')) {
      return `[${trimmed}]`;
    }
    return trimmed;
  }

  // Generate shoutrrr URL from service-specific inputs
  function generateShoutrrrUrl(): string {
    switch (selectedService) {
      case 'discord': {
        // Discord webhook URL format: https://discord.com/api/webhooks/{id}/{token}
        // Shoutrrr format: discord://{token}@{id}
        const match = serviceFormData.discordWebhookUrl.match(
          /discord\.com\/api\/webhooks\/(\d+)\/([A-Za-z0-9_-]+)/
        );
        if (match) {
          return `discord://${match[2]}@${match[1]}`;
        }
        return '';
      }
      case 'telegram': {
        // Shoutrrr format: telegram://{token}@telegram?chats={chatId}
        if (serviceFormData.telegramBotToken && serviceFormData.telegramChatId) {
          return `telegram://${serviceFormData.telegramBotToken}@telegram?chats=${serviceFormData.telegramChatId}`;
        }
        return '';
      }
      case 'ntfy': {
        if (!serviceFormData.ntfyTopic) return '';
        const server = serviceFormData.ntfyServer?.trim() || 'ntfy.sh';
        const isPublic = server === 'ntfy.sh';

        // Build user info for auth (encode special chars)
        const user = serviceFormData.ntfyUsername?.trim() || '';
        const pass = serviceFormData.ntfyPassword?.trim() || '';
        const auth = user
          ? pass
            ? `${encodeURIComponent(user)}:${encodeURIComponent(pass)}@`
            : `${encodeURIComponent(user)}@`
          : '';

        // Public ntfy.sh: always HTTPS, no scheme param, never include auth
        if (isPublic) {
          return `ntfy://${serviceFormData.ntfyTopic}`;
        }

        // Custom server: add ?scheme=http when HTTP selected
        const normalizedServer = normalizeNtfyHost(server);
        const schemeParam = serviceFormData.ntfyProtocol === 'http' ? '?scheme=http' : '';
        return `ntfy://${auth}${normalizedServer}/${serviceFormData.ntfyTopic}${schemeParam}`;
      }
      case 'gotify': {
        // Shoutrrr format: gotify://{server}/{token}
        if (serviceFormData.gotifyServer && serviceFormData.gotifyToken) {
          // Remove protocol if user included it
          const server = serviceFormData.gotifyServer.replace(/^https?:\/\//, '');
          return `gotify://${server}/${serviceFormData.gotifyToken}`;
        }
        return '';
      }
      case 'pushover': {
        // Shoutrrr format: pushover://shoutrrr:{apiToken}@{userKey}
        if (serviceFormData.pushoverApiToken && serviceFormData.pushoverUserKey) {
          return `pushover://shoutrrr:${serviceFormData.pushoverApiToken}@${serviceFormData.pushoverUserKey}`;
        }
        return '';
      }
      case 'slack': {
        // Slack webhook URL format: https://hooks.slack.com/services/{token-a}/{token-b}/{token-c}
        // Shoutrrr format: slack://hook:{token-a}-{token-b}-{token-c}@webhook
        const match = serviceFormData.slackWebhookUrl.match(
          /hooks\.slack\.com\/services\/([^/]+)\/([^/]+)\/([^/]+)/
        );
        if (match) {
          return `slack://hook:${match[1]}-${match[2]}-${match[3]}@webhook`;
        }
        return '';
      }
      case 'ifttt': {
        // Shoutrrr format: ifttt://{webhookKey}/?events={eventName}
        if (serviceFormData.iftttWebhookKey && serviceFormData.iftttEventName) {
          return `ifttt://${serviceFormData.iftttWebhookKey}/?events=${serviceFormData.iftttEventName}`;
        }
        return '';
      }
      case 'custom':
        return serviceFormData.customUrl;
      default:
        return '';
    }
  }

  // Check if service form is valid
  let isServiceFormValid = $derived.by(() => {
    switch (selectedService) {
      case 'discord':
        return /discord\.com\/api\/webhooks\/\d+\/[A-Za-z0-9_-]+/.test(
          serviceFormData.discordWebhookUrl
        );
      case 'telegram':
        return (
          serviceFormData.telegramBotToken.length > 0 && serviceFormData.telegramChatId.length > 0
        );
      case 'ntfy':
        return serviceFormData.ntfyTopic.length > 0;
      case 'gotify':
        return serviceFormData.gotifyServer.length > 0 && serviceFormData.gotifyToken.length > 0;
      case 'pushover':
        return (
          serviceFormData.pushoverApiToken.length > 0 && serviceFormData.pushoverUserKey.length > 0
        );
      case 'slack':
        return /hooks\.slack\.com\/services\/[^/]+\/[^/]+\/[^/]+/.test(
          serviceFormData.slackWebhookUrl
        );
      case 'ifttt':
        return (
          serviceFormData.iftttWebhookKey.length > 0 && serviceFormData.iftttEventName.length > 0
        );
      case 'webhook': {
        // URL must be valid http(s)
        if (!/^https?:\/\/.+/i.test(serviceFormData.webhookUrl)) return false;
        // Check auth requirements
        if (serviceFormData.webhookAuthType === 'bearer') {
          return serviceFormData.webhookBearerToken.length > 0;
        }
        if (serviceFormData.webhookAuthType === 'basic') {
          return (
            serviceFormData.webhookBasicUser.length > 0 &&
            serviceFormData.webhookBasicPass.length > 0
          );
        }
        return true; // 'none' auth type
      }
      case 'custom':
        return /^[a-z]+:\/\/.+/i.test(serviceFormData.customUrl);
      default:
        return false;
    }
  });

  // Get service-specific validation error
  let serviceValidationError = $derived.by(() => {
    if (isServiceFormValid) return '';

    switch (selectedService) {
      case 'discord':
        if (serviceFormData.discordWebhookUrl && !isServiceFormValid) {
          return t('settings.notifications.push.services.discord.invalidUrl');
        }
        return '';
      case 'telegram':
        if (
          (serviceFormData.telegramBotToken || serviceFormData.telegramChatId) &&
          !isServiceFormValid
        ) {
          return t('settings.notifications.push.services.telegram.incomplete');
        }
        return '';
      case 'webhook':
        if (serviceFormData.webhookUrl && !isServiceFormValid) {
          if (!/^https?:\/\/.+/i.test(serviceFormData.webhookUrl)) {
            return t('settings.notifications.push.services.webhook.invalidUrl');
          }
          if (serviceFormData.webhookAuthType === 'bearer' && !serviceFormData.webhookBearerToken) {
            return t('settings.notifications.push.services.webhook.tokenRequired');
          }
          if (serviceFormData.webhookAuthType === 'basic') {
            return t('settings.notifications.push.services.webhook.credentialsRequired');
          }
        }
        return '';
      case 'custom':
        if (serviceFormData.customUrl && !isServiceFormValid) {
          return t('settings.notifications.push.form.urls.validation.invalidFormat');
        }
        return '';
      default:
        return '';
    }
  });

  // Track if push settings have unsaved changes
  let hasPushChanges = $derived(
    originalPushSettings !== null &&
      JSON.stringify(pushSettings) !== JSON.stringify(originalPushSettings)
  );

  const templateFields = [
    { name: 'CommonName', description: 'Bird common name (e.g., "Northern Cardinal")' },
    { name: 'ScientificName', description: 'Scientific name (e.g., "Cardinalis cardinalis")' },
    { name: 'Confidence', description: 'Confidence value (0.0 to 1.0)' },
    { name: 'ConfidencePercent', description: 'Confidence as percentage (e.g., "99")' },
    { name: 'DetectionTime', description: 'Time of detection (e.g., "14:30:45")' },
    { name: 'DetectionDate', description: 'Date of detection (e.g., "2024-10-05")' },
    { name: 'Latitude', description: 'GPS latitude coordinate' },
    { name: 'Longitude', description: 'GPS longitude coordinate' },
    { name: 'Location', description: 'Formatted coordinates (e.g., "42.360100, -71.058900")' },
    { name: 'DetectionID', description: 'Detection ID number (e.g., "1234")' },
    {
      name: 'DetectionPath',
      description: 'Relative path to detection (e.g., "/ui/detections/1234")',
    },
    { name: 'DetectionURL', description: 'Full URL to detection in UI' },
    { name: 'ImageURL', description: 'Link to species image' },
    { name: 'DaysSinceFirstSeen', description: 'Number of days since first detected' },
  ];

  const defaultTemplate = {
    title: 'New Species: {{.CommonName}}',
    message:
      '{{.ImageURL}}\n\nFirst detection of {{.CommonName}} ({{.ScientificName}}) with {{.ConfidencePercent}}% confidence at {{.DetectionTime}}.\n\n{{.DetectionURL}}',
  };

  // Load notification settings
  async function loadNotificationSettings() {
    loadingTemplate = true;
    loadingPush = true;

    try {
      const data = await api.get<NotificationSettings>('/api/v2/settings/notification');

      // Load template settings
      if (data.templates?.newSpecies) {
        templateConfig = {
          title: data.templates.newSpecies.title ?? defaultTemplate.title,
          message: data.templates.newSpecies.message ?? defaultTemplate.message,
        };
        editedTitle = templateConfig.title;
        editedMessage = templateConfig.message;
      } else {
        templateConfig = { ...defaultTemplate };
        editedTitle = templateConfig.title;
        editedMessage = templateConfig.message;
      }

      // Load push settings
      if (data.push) {
        pushSettings = {
          enabled: data.push.enabled ?? false,
          providers: data.push.providers ?? [],
          minConfidenceThreshold: data.push.minConfidenceThreshold ?? 0,
          speciesCooldownMinutes: data.push.speciesCooldownMinutes ?? 0,
        };
        originalPushSettings = JSON.parse(JSON.stringify(pushSettings));
      }
    } catch {
      templateConfig = { ...defaultTemplate };
      editedTitle = templateConfig.title;
      editedMessage = templateConfig.message;
    } finally {
      loadingTemplate = false;
      loadingPush = false;
    }
  }

  async function saveTemplateConfig() {
    savingTemplate = true;
    templateStatusMessage = '';

    try {
      await api.patch('/api/v2/settings/notification', {
        templates: {
          newSpecies: {
            title: editedTitle,
            message: editedMessage,
          },
        },
      });

      if (templateConfig) {
        templateConfig.title = editedTitle;
        templateConfig.message = editedMessage;
      }

      templateStatusMessage = t('settings.notifications.templates.saveSuccess');
      templateStatusType = 'success';

      setTimeout(() => {
        templateStatusMessage = '';
      }, 3000);
    } catch (error) {
      templateStatusMessage = t('settings.notifications.templates.saveError', {
        message: (error as Error).message,
      });
      templateStatusType = 'error';

      setTimeout(() => {
        templateStatusMessage = '';
      }, 5000);
    } finally {
      savingTemplate = false;
    }
  }

  function resetTemplates() {
    const confirmReset = window.confirm(t('settings.notifications.templates.resetConfirm'));
    if (!confirmReset) {
      return;
    }

    editedTitle = defaultTemplate.title;
    editedMessage = defaultTemplate.message;
  }

  async function sendTestNewSpeciesNotification() {
    if (hasTemplateChanges) {
      const confirmTest = window.confirm(
        t('settings.notifications.templates.unsavedChangesWarning')
      );
      if (!confirmTest) {
        return;
      }
    }

    generating = true;
    statusMessage = '';
    statusType = 'info';

    updateStatus(t('settings.notifications.testNotification.statusMessages.sending'), 'info');

    try {
      interface TestNotificationResponse {
        title?: string;
      }
      const data = await api.post<TestNotificationResponse>(
        '/api/v2/notifications/test/new-species'
      );
      generating = false;

      updateStatus(
        t('settings.notifications.testNotification.statusMessages.success', {
          species: data.title || 'Northern Cardinal',
        }),
        'success'
      );

      setTimeout(() => {
        statusMessage = '';
        statusType = 'info';
      }, 5000);
    } catch (error) {
      generating = false;
      // Handle 503 specifically
      if (error instanceof ApiError && error.status === 503) {
        updateStatus(
          t('settings.notifications.testNotification.statusMessages.serviceUnavailable'),
          'error'
        );
      } else {
        updateStatus(
          t('settings.notifications.testNotification.statusMessages.error', {
            message: (error as Error).message,
          }),
          'error'
        );
      }

      setTimeout(() => {
        statusMessage = '';
        statusType = 'info';
      }, 10000);
    }
  }

  function updateStatus(message: string, type: 'info' | 'success' | 'error') {
    statusMessage = message;
    statusType = type;
  }

  // Push settings functions
  function togglePushEnabled(enabled: boolean) {
    pushSettings.enabled = enabled;
  }

  function toggleProviderEnabled(index: number) {
    const provider = safeArrayAccess(pushSettings.providers ?? [], index);
    if (provider) {
      provider.enabled = !provider.enabled;
    }
  }

  function resetServiceFormData() {
    serviceFormData = {
      discordWebhookUrl: '',
      telegramBotToken: '',
      telegramChatId: '',
      ntfyServer: 'ntfy.sh',
      ntfyTopic: '',
      ntfyProtocol: 'https',
      ntfyUsername: '',
      ntfyPassword: '',
      ntfyCheckHost: '',
      ntfyCheckStatus: 'idle',
      gotifyServer: '',
      gotifyToken: '',
      pushoverApiToken: '',
      pushoverUserKey: '',
      slackWebhookUrl: '',
      iftttWebhookKey: '',
      iftttEventName: '',
      webhookUrl: '',
      webhookMethod: 'POST',
      webhookAuthType: 'none',
      webhookBearerToken: '',
      webhookBasicUser: '',
      webhookBasicPass: '',
      customUrl: '',
    };
  }

  // Detect service type from existing shoutrrr URL
  function detectServiceFromUrl(url: string): ServiceType {
    if (url.startsWith('discord://')) return 'discord';
    if (url.startsWith('telegram://')) return 'telegram';
    if (url.startsWith('ntfy://')) return 'ntfy';
    if (url.startsWith('ifttt://')) return 'ifttt';
    if (url.startsWith('gotify://')) return 'gotify';
    if (url.startsWith('pushover://')) return 'pushover';
    if (url.startsWith('slack://')) return 'slack';
    return 'custom';
  }

  // Populate service form data from existing shoutrrr URL
  function populateServiceFormFromUrl(url: string) {
    resetServiceFormData();
    const service = detectServiceFromUrl(url);
    selectedService = service;

    // For custom URLs or if parsing fails, just use the raw URL
    if (service === 'custom') {
      serviceFormData.customUrl = url;
      return;
    }

    // Try to parse service-specific data (best effort)
    // Note: We can't fully reverse-engineer webhook URLs from shoutrrr URLs,
    // so for most services we just show the raw URL in custom mode
    switch (service) {
      case 'ntfy': {
        // Handles:
        //   ntfy://topic
        //   ntfy://server/topic
        //   ntfy://server/topic?scheme=http
        //   ntfy://user:pass@server/topic?scheme=http
        /* eslint-disable security/detect-unsafe-regex -- Protected by safeRegexTest length limit */
        const ntfyPattern =
          /^ntfy:\/\/(?:([^:@/?]+)(?::([^@/?]*))?@)?([^/?]+)(?:\/([^?]*))?(?:\?(.*))?$/;
        /* eslint-enable security/detect-unsafe-regex */
        if (safeRegexTest(ntfyPattern, url, 500)) {
          // Non-null assertion safe: safeRegexTest guarantees pattern matches
          const match = url.match(ntfyPattern)!;
          const [, user, pass, hostOrTopic, pathPart, queryString] = match;

          serviceFormData.ntfyUsername = user ? decodeURIComponent(user) : '';
          serviceFormData.ntfyPassword = pass ? decodeURIComponent(pass) : '';

          // Parse scheme from query string
          const params = new URLSearchParams(queryString || '');
          const scheme = params.get('scheme');
          serviceFormData.ntfyProtocol = scheme === 'http' ? 'http' : 'https';

          // Use !== undefined (not truthiness) — empty string path means server-only URL
          if (pathPart !== undefined) {
            // ntfy://server/topic[?...]
            serviceFormData.ntfyServer = hostOrTopic;
            serviceFormData.ntfyTopic = pathPart;
          } else {
            // ntfy://topic — public ntfy.sh shorthand
            serviceFormData.ntfyServer = 'ntfy.sh';
            serviceFormData.ntfyTopic = hostOrTopic;
          }
          serviceFormData.ntfyCheckStatus = 'idle';
          serviceFormData.ntfyCheckHost = '';
        }
        break;
      }
      default:
        // For services where we can't reverse the URL, fall back to custom
        selectedService = 'custom';
        serviceFormData.customUrl = url;
    }
  }

  function openAddProviderForm() {
    editingProviderIndex = null;
    selectedService = 'discord';
    resetServiceFormData();
    providerFormData = {
      name: '',
      urls: '',
      enabled: true,
      filterTypes: ['detection'],
    };
    showProviderForm = true;
  }

  function openEditProviderForm(index: number) {
    const provider = safeArrayAccess(pushSettings.providers ?? [], index);
    if (!provider) return;

    editingProviderIndex = index;
    resetServiceFormData();

    // Handle webhook provider type
    if (provider.type === 'webhook' && provider.endpoints?.[0]) {
      const endpoint = provider.endpoints[0];
      selectedService = 'webhook';
      serviceFormData.webhookUrl = endpoint.url || '';
      serviceFormData.webhookMethod = (endpoint.method as 'POST' | 'PUT' | 'PATCH') || 'POST';
      serviceFormData.webhookAuthType =
        (endpoint.auth?.type as 'none' | 'bearer' | 'basic') || 'none';
      if (endpoint.auth?.type === 'bearer') {
        serviceFormData.webhookBearerToken = endpoint.auth.token || '';
      } else if (endpoint.auth?.type === 'basic') {
        serviceFormData.webhookBasicUser = endpoint.auth.user || '';
        serviceFormData.webhookBasicPass = endpoint.auth.pass || '';
      }
    } else {
      // Populate service form from existing shoutrrr URL
      const existingUrl = provider.urls?.[0] || '';
      populateServiceFormFromUrl(existingUrl);
    }

    providerFormData = {
      name: provider.name,
      urls: provider.urls?.join('\n') || '',
      enabled: provider.enabled,
      filterTypes: provider.filter?.types || ['detection'],
    };
    showProviderForm = true;
  }

  function closeProviderForm() {
    showProviderForm = false;
    editingProviderIndex = null;
  }

  function saveProvider() {
    // Validate service form
    if (!isServiceFormValid) {
      return;
    }

    // Auto-generate name if empty
    let name = providerFormData.name.trim();
    if (!name) {
      const service = availableServices.find(s => s.value === selectedService);
      name = service?.label || 'Provider';
    }

    let provider: PushProviderConfig;

    if (selectedService === 'webhook') {
      // Build webhook endpoint configuration
      const auth: WebhookAuthConfig = { type: serviceFormData.webhookAuthType };
      if (serviceFormData.webhookAuthType === 'bearer') {
        auth.token = serviceFormData.webhookBearerToken;
      } else if (serviceFormData.webhookAuthType === 'basic') {
        auth.user = serviceFormData.webhookBasicUser;
        auth.pass = serviceFormData.webhookBasicPass;
      }

      const endpoint: WebhookEndpointConfig = {
        url: serviceFormData.webhookUrl,
        method: serviceFormData.webhookMethod,
        auth: auth.type !== 'none' ? auth : undefined,
      };

      provider = {
        type: 'webhook',
        enabled: providerFormData.enabled,
        name,
        endpoints: [endpoint],
        filter: {
          types: providerFormData.filterTypes,
        },
      };
    } else {
      // Generate the shoutrrr URL from service form
      const generatedUrl = generateShoutrrrUrl();
      if (!generatedUrl) {
        return;
      }

      provider = {
        type: 'shoutrrr',
        enabled: providerFormData.enabled,
        name,
        urls: [generatedUrl],
        filter: {
          types: providerFormData.filterTypes,
        },
      };
    }

    if (!pushSettings.providers) {
      pushSettings.providers = [];
    }

    if (editingProviderIndex !== null) {
      // Update existing - bounds validated by openEditProviderForm which uses
      // safeArrayAccess to verify the index exists before setting editingProviderIndex
      pushSettings.providers.splice(editingProviderIndex, 1, provider);
    } else {
      // Add new
      pushSettings.providers.push(provider);

      // Auto-enable push if adding first provider
      if (pushSettings.providers.length === 1) {
        pushSettings.enabled = true;
      }
    }

    closeProviderForm();
  }

  function deleteProvider(index: number) {
    const provider = safeArrayAccess(pushSettings.providers ?? [], index);
    if (!provider) return;

    const confirmDelete = window.confirm(
      t('settings.notifications.push.providers.deleteConfirm', { name: provider.name })
    );

    if (confirmDelete) {
      pushSettings.providers = pushSettings.providers?.filter((_, i) => i !== index) ?? [];
    }
  }

  async function savePushSettings() {
    savingPush = true;
    pushStatusMessage = '';

    try {
      await api.patch('/api/v2/settings/notification', {
        push: pushSettings,
      });

      originalPushSettings = JSON.parse(JSON.stringify(pushSettings));

      pushStatusMessage = t('settings.notifications.templates.saveSuccess');
      pushStatusType = 'success';

      setTimeout(() => {
        pushStatusMessage = '';
      }, 3000);
    } catch (error) {
      pushStatusMessage = t('settings.notifications.templates.saveError', {
        message: (error as Error).message,
      });
      pushStatusType = 'error';

      setTimeout(() => {
        pushStatusMessage = '';
      }, 5000);
    } finally {
      savingPush = false;
    }
  }

  async function testPushNotification() {
    // First save if there are unsaved changes
    if (hasPushChanges) {
      const confirmTest = window.confirm(
        t('settings.notifications.templates.unsavedChangesWarning')
      );
      if (!confirmTest) {
        return;
      }
    }

    testingProvider = true;

    try {
      await api.post('/api/v2/notifications/test/new-species');

      pushStatusMessage = t('settings.notifications.push.test.success');
      pushStatusType = 'success';

      setTimeout(() => {
        pushStatusMessage = '';
      }, 5000);
    } catch (error) {
      pushStatusMessage = t('settings.notifications.push.test.error', {
        message: (error as Error).message,
      });
      pushStatusType = 'error';

      setTimeout(() => {
        pushStatusMessage = '';
      }, 5000);
    } finally {
      testingProvider = false;
    }
  }

  async function checkNtfyServer() {
    const host = normalizeNtfyHost(serviceFormData.ntfyServer?.trim() || '');
    if (!host || host === 'ntfy.sh') return;

    // Record which host this probe is for (race guard)
    serviceFormData.ntfyCheckHost = host;
    serviceFormData.ntfyCheckStatus = 'checking';

    try {
      const result = await api.get<{ recommended: string; https: boolean; http: boolean }>(
        `/api/v2/notifications/check-ntfy-server?host=${encodeURIComponent(host)}`
      );

      // Discard result if host changed while probe was in flight
      if (serviceFormData.ntfyCheckHost !== host) return;

      const rec = result.recommended;
      if (rec === 'https' || rec === 'http') {
        serviceFormData.ntfyProtocol = rec;
        serviceFormData.ntfyCheckStatus = rec;
      } else {
        serviceFormData.ntfyCheckStatus = 'unreachable';
      }
    } catch {
      if (serviceFormData.ntfyCheckHost === host) {
        serviceFormData.ntfyCheckStatus = 'unreachable';
      }
    }
  }

  function toggleFilterType(type: string) {
    const index = providerFormData.filterTypes.indexOf(type);
    if (index === -1) {
      providerFormData.filterTypes.push(type);
    } else {
      providerFormData.filterTypes.splice(index, 1);
    }
  }

  onMount(() => {
    loadNotificationSettings();
  });

  // Tab state - Push tab first
  let activeTab = $state('push');

  // Tab definitions - Push first, Templates second
  let tabs = $derived<TabDefinition[]>([
    {
      id: 'push',
      label: t('settings.notifications.push.title'),
      icon: Send,
      content: pushTabContent,
      hasChanges: hasPushChanges,
    },
    {
      id: 'templates',
      label: t('settings.notifications.templates.title'),
      icon: Bell,
      content: templatesTabContent,
      hasChanges: hasTemplateChanges,
    },
  ]);
</script>

<!-- Templates Tab Content -->
{#snippet templatesTabContent()}
  <div class="space-y-4">
    <SettingsSection
      title={t('settings.notifications.templates.title')}
      description={t('settings.notifications.templates.description')}
      defaultOpen={true}
    >
      <div class="space-y-4">
        {#if loadingTemplate}
          <div class="flex justify-center py-4">
            <span
              class="inline-block w-6 h-6 border-4 border-[var(--color-base-300)] border-t-[var(--color-primary)] rounded-full animate-spin"
            ></span>
          </div>
        {:else if templateConfig}
          <div class="rounded-lg bg-[var(--color-base-200)]">
            <div class="p-6">
              <h3 class="text-base font-semibold mb-4">
                {t('settings.notifications.templates.newSpeciesTitle')}
              </h3>

              <div class="space-y-4">
                <div>
                  <label for="template-title" class="block mb-1">
                    <span class="text-sm font-semibold text-[var(--color-base-content)]"
                      >{t('settings.notifications.templates.titleLabel')}</span
                    >
                  </label>
                  <input
                    id="template-title"
                    type="text"
                    bind:value={editedTitle}
                    class="w-full h-10 px-3 font-mono text-sm bg-[var(--color-base-100)] border border-[var(--border-200)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
                    placeholder={t('settings.notifications.templates.titlePlaceholder')}
                  />
                </div>

                <div>
                  <label for="template-message" class="block mb-1">
                    <span class="text-sm font-semibold text-[var(--color-base-content)]"
                      >{t('settings.notifications.templates.messageLabel')}</span
                    >
                  </label>
                  <textarea
                    id="template-message"
                    bind:value={editedMessage}
                    class="w-full px-3 py-2 font-mono text-sm bg-[var(--color-base-100)] border border-[var(--border-200)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors resize-y"
                    rows="6"
                    placeholder={t('settings.notifications.templates.messagePlaceholder')}
                  ></textarea>
                </div>

                {#if templateStatusMessage}
                  <div
                    class="flex items-center gap-2 py-2 px-3 text-sm rounded-lg {templateStatusType ===
                    'success'
                      ? 'bg-[color-mix(in_srgb,var(--color-success)_15%,transparent)] text-[var(--color-success)]'
                      : 'bg-[color-mix(in_srgb,var(--color-error)_15%,transparent)] text-[var(--color-error)]'}"
                    role="alert"
                    aria-live="assertive"
                  >
                    <div class="shrink-0">
                      {#if templateStatusType === 'success'}
                        <CircleCheck class="size-4" />
                      {:else if templateStatusType === 'error'}
                        <XCircle class="size-4" />
                      {/if}
                    </div>
                    <span>{templateStatusMessage}</span>
                  </div>
                {/if}

                {#if statusMessage}
                  <div
                    class="flex items-center gap-2 py-2 px-3 text-sm rounded-lg {statusType ===
                    'info'
                      ? 'bg-[color-mix(in_srgb,var(--color-info)_15%,transparent)] text-[var(--color-info)]'
                      : statusType === 'success'
                        ? 'bg-[color-mix(in_srgb,var(--color-success)_15%,transparent)] text-[var(--color-success)]'
                        : 'bg-[color-mix(in_srgb,var(--color-error)_15%,transparent)] text-[var(--color-error)]'}"
                    role="status"
                    aria-live="polite"
                  >
                    <div class="shrink-0">
                      {#if statusType === 'info'}
                        <Info class="size-4" />
                      {:else if statusType === 'success'}
                        <CircleCheck class="size-4" />
                      {:else if statusType === 'error'}
                        <XCircle class="size-4" />
                      {/if}
                    </div>
                    <span>{statusMessage}</span>
                  </div>
                {/if}

                <div class="flex gap-2 justify-end">
                  <button
                    onclick={resetTemplates}
                    class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-black/5 dark:hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-base-content)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                    disabled={savingTemplate || generating}
                  >
                    {t('settings.notifications.templates.resetButton')}
                  </button>
                  <button
                    onclick={saveTemplateConfig}
                    class="inline-flex items-center justify-center gap-2 h-8 px-3 text-sm font-medium rounded-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed {hasTemplateChanges
                      ? 'bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:opacity-90 focus-visible:ring-[var(--color-primary)]'
                      : 'bg-transparent hover:bg-black/5 dark:hover:bg-white/10 focus-visible:ring-[var(--color-base-content)]'}"
                    disabled={savingTemplate || generating || !hasTemplateChanges}
                  >
                    {#if savingTemplate}
                      <span
                        class="inline-block w-3 h-3 border-2 border-[var(--color-base-300)] border-t-current rounded-full animate-spin"
                      ></span>
                      <span>{t('settings.notifications.templates.savingButton')}</span>
                    {:else}
                      <span
                        >{hasTemplateChanges
                          ? t('settings.notifications.templates.saveButtonUnsaved')
                          : t('settings.notifications.templates.saveButton')}</span
                      >
                    {/if}
                  </button>
                  <button
                    onclick={sendTestNewSpeciesNotification}
                    disabled={generating || savingTemplate}
                    class="inline-flex items-center justify-center gap-2 h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-secondary)] text-[var(--color-secondary-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-secondary)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                    title={hasTemplateChanges
                      ? t('settings.notifications.templates.testWithUnsavedChanges')
                      : t('settings.notifications.templates.testNormal')}
                  >
                    {#if generating}
                      <span
                        class="inline-block w-3 h-3 border-2 border-[var(--color-base-300)] border-t-current rounded-full animate-spin"
                      ></span>
                      <span>{t('settings.notifications.templates.sendingButton')}</span>
                    {:else}
                      <span class="flex items-center gap-1">
                        <Bell class="size-4" />
                        <span>{t('settings.notifications.templates.testButton')}</span>
                      </span>
                    {/if}
                  </button>
                </div>
              </div>
            </div>
          </div>

          <div class="rounded-lg bg-[var(--color-base-200)]">
            <div class="p-6">
              <h3 class="text-base font-semibold mb-4">
                {t('settings.notifications.templates.availableVariables')}
              </h3>
              <p class="text-sm text-[var(--color-base-content)] opacity-80 mb-3">
                {t('settings.notifications.templates.variablesDescription')}
                <code class="bg-[var(--color-base-300)] px-1 rounded-sm"
                  >&#123;&#123;.VariableName&#125;&#125;</code
                >
              </p>

              <div class="grid grid-cols-1 md:grid-cols-2 gap-x-4 gap-y-2 text-xs">
                {#each templateFields as field (field.name)}
                  <div class="flex items-baseline gap-2">
                    <code class="font-mono text-[var(--color-primary)] shrink-0"
                      >&#123;&#123;.{field.name}&#125;&#125;</code
                    >
                    <span class="text-[var(--color-base-content)] opacity-70"
                      >{field.description}</span
                    >
                  </div>
                {/each}
              </div>

              <!-- Privacy Note - Collapsible -->
              <details class="mt-4 text-xs">
                <summary
                  class="cursor-pointer text-[var(--color-base-content)] opacity-60 hover:text-[var(--color-base-content)] hover:opacity-80 flex items-center gap-1"
                >
                  <Info class="size-3.5" />
                  {t('settings.notifications.privacy.title')}
                </summary>
                <div class="mt-2 pl-5 text-[var(--color-base-content)] opacity-60 space-y-1">
                  <p>{t('settings.notifications.privacy.description')}</p>
                  <p>{t('settings.notifications.privacy.recommendation')}</p>
                </div>
              </details>
            </div>
          </div>
        {/if}
      </div>
    </SettingsSection>
  </div>
{/snippet}

<!-- Push Settings Tab Content -->
{#snippet pushTabContent()}
  <SettingsSection
    title={t('settings.notifications.push.title')}
    description={t('settings.notifications.push.description')}
    defaultOpen={true}
  >
    {#if loadingPush}
      <div class="flex justify-center py-4">
        <span
          class="inline-block w-6 h-6 border-4 border-[var(--color-base-300)] border-t-[var(--color-primary)] rounded-full animate-spin"
        ></span>
      </div>
    {:else}
      <div class="space-y-4">
        <!-- Master Enable Toggle -->
        <Checkbox
          checked={pushSettings.enabled}
          label={t('settings.notifications.push.enable')}
          disabled={savingPush}
          onchange={togglePushEnabled}
        />

        {#if pushSettings.enabled}
          <p class="text-sm text-[var(--color-base-content)] opacity-70">
            {t('settings.notifications.push.enabledDescription')}
          </p>
        {:else}
          <p class="text-sm text-[var(--color-base-content)] opacity-50">
            {t('settings.notifications.push.disabled')}
          </p>
        {/if}

        <!-- Detection Filters Section -->
        {#if pushSettings.enabled}
          <div class="rounded-lg bg-[var(--color-base-200)]">
            <div class="p-6">
              <h3 class="text-base font-semibold mb-4">
                {t('settings.notifications.push.filters.title')}
              </h3>
              <p class="text-sm text-[var(--color-base-content)] opacity-70 mb-2">
                {t('settings.notifications.push.filters.description')}
              </p>

              <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
                <!-- Minimum Confidence Threshold -->
                <div>
                  <label for="min-confidence" class="block mb-1">
                    <span class="text-sm font-semibold text-[var(--color-base-content)]">
                      {t('settings.notifications.push.filters.minConfidence.label')}
                    </span>
                  </label>
                  <div class="flex">
                    <input
                      id="min-confidence"
                      type="number"
                      min="0"
                      max="100"
                      step="5"
                      value={Math.round((pushSettings.minConfidenceThreshold ?? 0) * 100)}
                      onchange={e => {
                        const target = e.target as HTMLInputElement;
                        pushSettings.minConfidenceThreshold =
                          Math.max(0, Math.min(100, parseInt(target.value) || 0)) / 100;
                      }}
                      class="flex-1 h-10 px-3 text-sm bg-[var(--color-base-100)] border border-[var(--border-200)] rounded-l-lg focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
                      disabled={savingPush}
                    />
                    <span
                      class="inline-flex items-center justify-center px-3 text-sm bg-[var(--color-base-300)] border border-l-0 border-[var(--border-200)] rounded-r-lg"
                      >%</span
                    >
                  </div>
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 mt-1">
                    {t('settings.notifications.push.filters.minConfidence.helpText')}
                  </p>
                </div>

                <!-- Species Cooldown -->
                <div>
                  <label for="species-cooldown" class="block mb-1">
                    <span class="text-sm font-semibold text-[var(--color-base-content)]">
                      {t('settings.notifications.push.filters.speciesCooldown.label')}
                    </span>
                  </label>
                  <div class="flex">
                    <input
                      id="species-cooldown"
                      type="number"
                      min="0"
                      max="1440"
                      step="5"
                      value={pushSettings.speciesCooldownMinutes ?? 0}
                      onchange={e => {
                        const target = e.target as HTMLInputElement;
                        pushSettings.speciesCooldownMinutes = Math.max(
                          0,
                          Math.min(1440, parseInt(target.value) || 0)
                        );
                      }}
                      class="flex-1 h-10 px-3 text-sm bg-[var(--color-base-100)] border border-[var(--border-200)] rounded-l-lg focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
                      disabled={savingPush}
                    />
                    <span
                      class="inline-flex items-center justify-center px-3 text-sm bg-[var(--color-base-300)] border border-l-0 border-[var(--border-200)] rounded-r-lg"
                      >{t('settings.notifications.push.filters.speciesCooldown.unit')}</span
                    >
                  </div>
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 mt-1">
                    {t('settings.notifications.push.filters.speciesCooldown.helpText')}
                  </p>
                </div>
              </div>
            </div>
          </div>
        {/if}

        <!-- Provider Form Modal -->
        {#if showProviderForm}
          <div class="rounded-lg bg-[var(--color-base-200)] border border-[var(--color-primary)]">
            <div class="p-6">
              <h3 class="text-base font-semibold mb-4">
                {editingProviderIndex !== null
                  ? t('settings.notifications.push.form.editTitle')
                  : t('settings.notifications.push.form.addTitle')}
              </h3>

              <div class="space-y-4">
                <!-- Service Selector with Icons -->
                <SelectDropdown
                  options={availableServices}
                  bind:value={selectedService}
                  label={t('settings.notifications.push.services.selectLabel')}
                  helpText={t('settings.notifications.push.services.selectHelpText')}
                  variant="select"
                  groupBy={false}
                  onChange={value => (selectedService = value as ServiceType)}
                >
                  {#snippet renderOption(option)}
                    {@const serviceOption = option as ServiceOption}
                    <div class="flex items-center gap-2">
                      <ServiceIcon service={serviceOption.serviceId} className="size-4" />
                      <span>{serviceOption.label}</span>
                    </div>
                  {/snippet}
                  {#snippet renderSelected(options)}
                    {@const serviceOption = options[0] as ServiceOption}
                    <span class="flex items-center gap-2">
                      <ServiceIcon service={serviceOption.serviceId} className="size-4" />
                      <span>{serviceOption.label}</span>
                    </span>
                  {/snippet}
                </SelectDropdown>

                <!-- Service-Specific Inputs -->
                {#if selectedService === 'discord'}
                  <TextInput
                    id="discord-webhook"
                    value={serviceFormData.discordWebhookUrl}
                    label={t('settings.notifications.push.services.discord.webhookUrl.label')}
                    placeholder={t(
                      'settings.notifications.push.services.discord.webhookUrl.placeholder'
                    )}
                    onchange={value => (serviceFormData.discordWebhookUrl = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.discord.webhookUrl.helpText')}
                  </p>
                {:else if selectedService === 'telegram'}
                  <TextInput
                    id="telegram-token"
                    value={serviceFormData.telegramBotToken}
                    label={t('settings.notifications.push.services.telegram.botToken.label')}
                    placeholder={t(
                      'settings.notifications.push.services.telegram.botToken.placeholder'
                    )}
                    onchange={value => (serviceFormData.telegramBotToken = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.telegram.botToken.helpText')}
                  </p>
                  <TextInput
                    id="telegram-chat"
                    value={serviceFormData.telegramChatId}
                    label={t('settings.notifications.push.services.telegram.chatId.label')}
                    placeholder={t(
                      'settings.notifications.push.services.telegram.chatId.placeholder'
                    )}
                    onchange={value => (serviceFormData.telegramChatId = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.telegram.chatId.helpText')}
                  </p>
                {:else if selectedService === 'ntfy'}
                  <TextInput
                    id="ntfy-server"
                    value={serviceFormData.ntfyServer}
                    label={t('settings.notifications.push.services.ntfy.server.label')}
                    placeholder={t('settings.notifications.push.services.ntfy.server.placeholder')}
                    onchange={value => {
                      serviceFormData.ntfyServer = value;
                      serviceFormData.ntfyCheckStatus = 'idle';
                      serviceFormData.ntfyCheckHost = '';
                      // Clear credentials when switching servers to prevent leaking
                      // auth from a private server to a different host
                      serviceFormData.ntfyUsername = '';
                      serviceFormData.ntfyPassword = '';
                    }}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.ntfy.server.helpText')}
                  </p>

                  {#if serviceFormData.ntfyServer && serviceFormData.ntfyServer !== 'ntfy.sh'}
                    <!-- Protocol selector + Test Connection button -->
                    <div class="flex items-center gap-2 mt-1 flex-wrap">
                      <SelectDropdown
                        bind:value={serviceFormData.ntfyProtocol}
                        options={ntfyProtocolOptions}
                        variant="select"
                        size="sm"
                        menuSize="sm"
                        onChange={() => (serviceFormData.ntfyCheckStatus = 'idle')}
                      />
                      <button
                        type="button"
                        class="btn btn-sm btn-outline"
                        disabled={serviceFormData.ntfyCheckStatus === 'checking'}
                        onclick={checkNtfyServer}
                      >
                        {#if serviceFormData.ntfyCheckStatus === 'checking'}
                          <span class="loading loading-spinner loading-xs"></span>
                        {/if}
                        {t('settings.notifications.push.services.ntfy.testConnection')}
                      </button>
                      {#if serviceFormData.ntfyCheckStatus === 'https'}
                        <span class="text-xs text-success"
                          >{t('settings.notifications.push.services.ntfy.connectionOk.https')}</span
                        >
                      {:else if serviceFormData.ntfyCheckStatus === 'http'}
                        <span class="text-xs text-warning"
                          >{t('settings.notifications.push.services.ntfy.connectionOk.http')}</span
                        >
                      {:else if serviceFormData.ntfyCheckStatus === 'unreachable'}
                        <span class="text-xs text-error"
                          >{t('settings.notifications.push.services.ntfy.connectionFailed')}</span
                        >
                      {/if}
                    </div>

                    <!-- Optional authentication -->
                    <details class="mt-2">
                      <summary
                        class="text-sm cursor-pointer opacity-70 hover:opacity-100 select-none"
                      >
                        {t('settings.notifications.push.services.ntfy.auth.label')}
                      </summary>
                      <div class="mt-2 space-y-2">
                        <TextInput
                          id="ntfy-username"
                          value={serviceFormData.ntfyUsername}
                          label={t('settings.notifications.push.services.ntfy.auth.username.label')}
                          placeholder={t(
                            'settings.notifications.push.services.ntfy.auth.username.placeholder'
                          )}
                          onchange={value => (serviceFormData.ntfyUsername = value)}
                        />
                        <TextInput
                          id="ntfy-password"
                          type="password"
                          value={serviceFormData.ntfyPassword}
                          label={t('settings.notifications.push.services.ntfy.auth.password.label')}
                          placeholder={t(
                            'settings.notifications.push.services.ntfy.auth.password.placeholder'
                          )}
                          onchange={value => (serviceFormData.ntfyPassword = value)}
                        />
                      </div>
                    </details>
                  {/if}

                  <TextInput
                    id="ntfy-topic"
                    value={serviceFormData.ntfyTopic}
                    label={t('settings.notifications.push.services.ntfy.topic.label')}
                    placeholder={t('settings.notifications.push.services.ntfy.topic.placeholder')}
                    onchange={value => (serviceFormData.ntfyTopic = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.ntfy.topic.helpText')}
                  </p>
                {:else if selectedService === 'gotify'}
                  <TextInput
                    id="gotify-server"
                    value={serviceFormData.gotifyServer}
                    label={t('settings.notifications.push.services.gotify.server.label')}
                    placeholder={t(
                      'settings.notifications.push.services.gotify.server.placeholder'
                    )}
                    onchange={value => (serviceFormData.gotifyServer = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.gotify.server.helpText')}
                  </p>
                  <TextInput
                    id="gotify-token"
                    value={serviceFormData.gotifyToken}
                    label={t('settings.notifications.push.services.gotify.token.label')}
                    placeholder={t('settings.notifications.push.services.gotify.token.placeholder')}
                    onchange={value => (serviceFormData.gotifyToken = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.gotify.token.helpText')}
                  </p>
                {:else if selectedService === 'pushover'}
                  <TextInput
                    id="pushover-api"
                    value={serviceFormData.pushoverApiToken}
                    label={t('settings.notifications.push.services.pushover.apiToken.label')}
                    placeholder={t(
                      'settings.notifications.push.services.pushover.apiToken.placeholder'
                    )}
                    onchange={value => (serviceFormData.pushoverApiToken = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.pushover.apiToken.helpText')}
                  </p>
                  <TextInput
                    id="pushover-user"
                    value={serviceFormData.pushoverUserKey}
                    label={t('settings.notifications.push.services.pushover.userKey.label')}
                    placeholder={t(
                      'settings.notifications.push.services.pushover.userKey.placeholder'
                    )}
                    onchange={value => (serviceFormData.pushoverUserKey = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.pushover.userKey.helpText')}
                  </p>
                {:else if selectedService === 'slack'}
                  <TextInput
                    id="slack-webhook"
                    value={serviceFormData.slackWebhookUrl}
                    label={t('settings.notifications.push.services.slack.webhookUrl.label')}
                    placeholder={t(
                      'settings.notifications.push.services.slack.webhookUrl.placeholder'
                    )}
                    onchange={value => (serviceFormData.slackWebhookUrl = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.slack.webhookUrl.helpText')}
                  </p>
                {:else if selectedService === 'ifttt'}
                  <TextInput
                    id="ifttt-webhook-key"
                    value={serviceFormData.iftttWebhookKey}
                    label={t('settings.notifications.push.services.ifttt.webhookKey.label')}
                    placeholder={t(
                      'settings.notifications.push.services.ifttt.webhookKey.placeholder'
                    )}
                    onchange={value => (serviceFormData.iftttWebhookKey = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.ifttt.webhookKey.helpText')}
                  </p>
                  <TextInput
                    id="ifttt-event-name"
                    value={serviceFormData.iftttEventName}
                    label={t('settings.notifications.push.services.ifttt.eventName.label')}
                    placeholder={t(
                      'settings.notifications.push.services.ifttt.eventName.placeholder'
                    )}
                    onchange={value => (serviceFormData.iftttEventName = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.ifttt.eventName.helpText')}
                  </p>
                {:else if selectedService === 'webhook'}
                  <TextInput
                    id="webhook-url"
                    value={serviceFormData.webhookUrl}
                    label={t('settings.notifications.push.services.webhook.url.label')}
                    placeholder={t('settings.notifications.push.services.webhook.url.placeholder')}
                    onchange={value => (serviceFormData.webhookUrl = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.webhook.url.helpText')}
                  </p>

                  <!-- HTTP Method -->
                  <SelectDropdown
                    bind:value={serviceFormData.webhookMethod}
                    options={webhookMethodOptions}
                    label={t('settings.notifications.push.services.webhook.method.label')}
                    helpText={t('settings.notifications.push.services.webhook.method.helpText')}
                    variant="select"
                    size="sm"
                    menuSize="sm"
                  />

                  <!-- Authentication Type -->
                  <SelectDropdown
                    bind:value={serviceFormData.webhookAuthType}
                    options={webhookAuthOptions}
                    label={t('settings.notifications.push.services.webhook.auth.label')}
                    helpText={t('settings.notifications.push.services.webhook.auth.helpText')}
                    variant="select"
                    size="sm"
                    menuSize="sm"
                  />

                  <!-- Bearer Token (conditional) -->
                  {#if serviceFormData.webhookAuthType === 'bearer'}
                    <TextInput
                      id="webhook-bearer-token"
                      value={serviceFormData.webhookBearerToken}
                      label={t('settings.notifications.push.services.webhook.bearerToken.label')}
                      placeholder={t(
                        'settings.notifications.push.services.webhook.bearerToken.placeholder'
                      )}
                      onchange={value => (serviceFormData.webhookBearerToken = value)}
                    />
                    <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                      {t('settings.notifications.push.services.webhook.bearerToken.helpText')}
                    </p>
                  {/if}

                  <!-- Basic Auth (conditional) -->
                  {#if serviceFormData.webhookAuthType === 'basic'}
                    <TextInput
                      id="webhook-basic-user"
                      value={serviceFormData.webhookBasicUser}
                      label={t('settings.notifications.push.services.webhook.basicUser.label')}
                      placeholder={t(
                        'settings.notifications.push.services.webhook.basicUser.placeholder'
                      )}
                      onchange={value => (serviceFormData.webhookBasicUser = value)}
                    />
                    <TextInput
                      id="webhook-basic-pass"
                      type="password"
                      value={serviceFormData.webhookBasicPass}
                      label={t('settings.notifications.push.services.webhook.basicPass.label')}
                      placeholder={t(
                        'settings.notifications.push.services.webhook.basicPass.placeholder'
                      )}
                      onchange={value => (serviceFormData.webhookBasicPass = value)}
                    />
                    <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                      {t('settings.notifications.push.services.webhook.basicAuth.helpText')}
                    </p>
                  {/if}
                {:else if selectedService === 'custom'}
                  <TextInput
                    id="custom-url"
                    value={serviceFormData.customUrl}
                    label={t('settings.notifications.push.services.custom.url.label')}
                    placeholder={t('settings.notifications.push.services.custom.url.placeholder')}
                    onchange={value => (serviceFormData.customUrl = value)}
                  />
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                    {t('settings.notifications.push.services.custom.url.helpText')}
                  </p>
                  <!-- URL Formats Help for Custom -->
                  <details class="text-xs">
                    <summary
                      class="cursor-pointer text-[var(--color-base-content)] opacity-60 hover:opacity-80"
                    >
                      {t('settings.notifications.push.form.urlFormats.title')}
                    </summary>
                    <div
                      class="mt-2 pl-2 space-y-1 font-mono text-[var(--color-base-content)] opacity-70"
                    >
                      <p>
                        <strong>{t('settings.notifications.push.form.urlFormats.discord')}:</strong>
                        {t('settings.notifications.push.form.urlFormats.discordFormat')}
                      </p>
                      <p>
                        <strong>{t('settings.notifications.push.form.urlFormats.telegram')}:</strong
                        >
                        {t('settings.notifications.push.form.urlFormats.telegramFormat')}
                      </p>
                      <p>
                        <strong>{t('settings.notifications.push.form.urlFormats.slack')}:</strong>
                        {t('settings.notifications.push.form.urlFormats.slackFormat')}
                      </p>
                      <p>
                        <strong>{t('settings.notifications.push.form.urlFormats.pushover')}:</strong
                        >
                        {t('settings.notifications.push.form.urlFormats.pushoverFormat')}
                      </p>
                      <p>
                        <strong>{t('settings.notifications.push.form.urlFormats.gotify')}:</strong>
                        {t('settings.notifications.push.form.urlFormats.gotifyFormat')}
                      </p>
                      <p>
                        <strong>{t('settings.notifications.push.form.urlFormats.ntfy')}:</strong>
                        {t('settings.notifications.push.form.urlFormats.ntfyFormat')}
                      </p>
                      <a
                        href={t('settings.notifications.push.form.urlFormats.shoutrrrDocs')}
                        target="_blank"
                        rel="noopener noreferrer"
                        class="inline-flex items-center gap-1 mt-2 text-[var(--color-primary)] hover:underline"
                      >
                        {t('settings.notifications.push.form.urlFormats.moreServices')}
                        <ExternalLink class="size-3" />
                      </a>
                    </div>
                  </details>
                {/if}

                <!-- Service Validation Error -->
                {#if serviceValidationError}
                  <p class="text-xs text-[var(--color-error)]">{serviceValidationError}</p>
                {/if}

                <!-- Provider Name -->
                <TextInput
                  id="provider-name"
                  value={providerFormData.name}
                  label={t('settings.notifications.push.form.name.label')}
                  placeholder={t('settings.notifications.push.form.name.placeholder')}
                  onchange={value => (providerFormData.name = value)}
                />
                <p class="text-xs text-[var(--color-base-content)] opacity-60 -mt-2">
                  {t('settings.notifications.push.form.name.helpText')}
                </p>

                <!-- Notification Types -->
                <fieldset class="">
                  <legend class="text-sm font-semibold text-[var(--color-base-content)] mb-1">
                    {t('settings.notifications.push.form.notificationTypes.label')}
                  </legend>
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 mb-2">
                    {t('settings.notifications.push.form.notificationTypes.helpText')}
                  </p>
                  <div class="flex flex-wrap gap-4">
                    <Checkbox
                      checked={providerFormData.filterTypes.includes('detection')}
                      label={t('settings.notifications.push.form.notificationTypes.detection')}
                      onchange={() => toggleFilterType('detection')}
                    />
                    <Checkbox
                      checked={providerFormData.filterTypes.includes('error')}
                      label={t('settings.notifications.push.form.notificationTypes.error')}
                      onchange={() => toggleFilterType('error')}
                    />
                    <Checkbox
                      checked={providerFormData.filterTypes.includes('warning')}
                      label={t('settings.notifications.push.form.notificationTypes.warning')}
                      onchange={() => toggleFilterType('warning')}
                    />
                    <Checkbox
                      checked={providerFormData.filterTypes.includes('info')}
                      label={t('settings.notifications.push.form.notificationTypes.info')}
                      onchange={() => toggleFilterType('info')}
                    />
                    <Checkbox
                      checked={providerFormData.filterTypes.includes('system')}
                      label={t('settings.notifications.push.form.notificationTypes.system')}
                      onchange={() => toggleFilterType('system')}
                    />
                  </div>
                </fieldset>

                <!-- Enable Provider -->
                <Checkbox
                  checked={providerFormData.enabled}
                  label={t('settings.notifications.push.providers.enableToggle')}
                  onchange={checked => (providerFormData.enabled = checked)}
                />

                <!-- Form Actions -->
                <div class="flex gap-2 justify-end">
                  <button
                    onclick={closeProviderForm}
                    class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-black/5 dark:hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-base-content)] focus-visible:ring-offset-2 transition-colors"
                  >
                    {t('settings.notifications.push.form.cancelButton')}
                  </button>
                  <button
                    onclick={saveProvider}
                    class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                    disabled={!isServiceFormValid}
                  >
                    {t('settings.notifications.push.form.saveButton')}
                  </button>
                </div>
              </div>
            </div>
          </div>
        {/if}

        <!-- Providers List -->
        <div class="space-y-3">
          <div class="flex items-center justify-between">
            <h3 class="font-semibold text-sm">
              {t('settings.notifications.push.providers.title')}
            </h3>
            {#if !showProviderForm}
              <button
                onclick={openAddProviderForm}
                class="inline-flex items-center justify-center gap-1 h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-2 transition-colors"
              >
                <Plus class="size-4" />
                {t('settings.notifications.push.providers.addButton')}
              </button>
            {/if}
          </div>

          {#if pushSettings.providers && pushSettings.providers.length > 0}
            <div class="space-y-2">
              {#each pushSettings.providers as provider, index (`${provider.type}:${provider.name}:${index}`)}
                <div
                  class="rounded-lg bg-[var(--color-base-200)]"
                  class:opacity-50={!provider.enabled || !pushSettings.enabled}
                >
                  <div class="py-3 px-4">
                    <div class="flex items-center justify-between gap-4">
                      <div class="flex items-center gap-3 min-w-0">
                        <input
                          type="checkbox"
                          class="appearance-none w-10 h-5 rounded-full cursor-pointer transition-all relative bg-[var(--color-base-300)] before:content-[''] before:absolute before:top-0.5 before:left-0.5 before:w-4 before:h-4 before:rounded-full before:bg-[var(--color-base-100)] before:shadow-sm before:transition-transform checked:bg-[var(--color-primary)] checked:before:translate-x-5 disabled:opacity-50 disabled:cursor-not-allowed"
                          checked={provider.enabled}
                          disabled={!pushSettings.enabled}
                          onchange={() => toggleProviderEnabled(index)}
                          aria-label={t('settings.notifications.push.providers.enableToggle')}
                        />
                        <div class="min-w-0">
                          <div class="font-medium truncate">{provider.name}</div>
                          <div class="text-xs text-[var(--color-base-content)] opacity-60 truncate">
                            {#if provider.type === 'webhook'}
                              {provider.endpoints?.[0]?.url || ''}
                            {:else}
                              {t('settings.notifications.push.providers.urlsPreview', {
                                count: provider.urls?.length || 0,
                              })}
                            {/if}
                          </div>
                        </div>
                      </div>
                      <div class="flex items-center gap-1 shrink-0">
                        <span
                          class="inline-flex items-center justify-center px-1.5 py-px text-xs font-medium rounded-full bg-black/5 dark:bg-white/5 text-[var(--color-base-content)]"
                        >
                          {provider.type === 'webhook'
                            ? t('settings.notifications.push.providers.typeBadge.webhook')
                            : t('settings.notifications.push.providers.typeBadge.shoutrrr')}
                        </span>
                        <button
                          onclick={() => openEditProviderForm(index)}
                          class="inline-flex items-center justify-center w-6 h-6 rounded bg-transparent hover:bg-black/5 dark:hover:bg-white/10 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                          title={t('settings.notifications.push.providers.editButton')}
                          disabled={showProviderForm}
                        >
                          <Pencil class="size-3.5" />
                        </button>
                        <button
                          onclick={() => deleteProvider(index)}
                          class="inline-flex items-center justify-center w-6 h-6 rounded bg-transparent hover:bg-black/5 dark:hover:bg-white/10 text-[var(--color-error)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                          title={t('settings.notifications.push.providers.deleteButton')}
                          disabled={showProviderForm}
                        >
                          <Trash2 class="size-3.5" />
                        </button>
                      </div>
                    </div>
                  </div>
                </div>
              {/each}
            </div>
          {:else if !showProviderForm}
            <div
              class="text-center py-8 text-[var(--color-base-content)] opacity-60 bg-[var(--color-base-200)] rounded-lg"
            >
              <Send class="size-10 mx-auto mb-3 opacity-50" />
              <p class="text-sm font-medium">{t('settings.notifications.push.noProviders')}</p>
              <p class="text-xs mt-1">
                {t('settings.notifications.push.noProvidersDescription')}
              </p>
            </div>
          {/if}
        </div>

        <!-- Status Message -->
        {#if pushStatusMessage}
          <div
            class="flex items-center gap-2 py-2 px-3 text-sm rounded-lg {pushStatusType ===
            'success'
              ? 'bg-[color-mix(in_srgb,var(--color-success)_15%,transparent)] text-[var(--color-success)]'
              : 'bg-[color-mix(in_srgb,var(--color-error)_15%,transparent)] text-[var(--color-error)]'}"
            role="alert"
            aria-live="assertive"
          >
            <div class="shrink-0">
              {#if pushStatusType === 'success'}
                <CircleCheck class="size-4" />
              {:else if pushStatusType === 'error'}
                <XCircle class="size-4" />
              {/if}
            </div>
            <span>{pushStatusMessage}</span>
          </div>
        {/if}

        <!-- Save and Test Buttons -->
        {#if pushSettings.providers && pushSettings.providers.length > 0}
          <div class="flex gap-2 justify-end">
            <SettingsButton
              onclick={savePushSettings}
              loading={savingPush}
              loadingText={t('settings.notifications.templates.savingButton')}
              disabled={!hasPushChanges || savingPush}
              variant={hasPushChanges ? 'primary' : 'ghost'}
            >
              {hasPushChanges
                ? t('settings.notifications.templates.saveButtonUnsaved')
                : t('settings.notifications.templates.saveButton')}
            </SettingsButton>
            <SettingsButton
              onclick={testPushNotification}
              loading={testingProvider}
              loadingText={t('settings.notifications.push.form.testingButton')}
              disabled={testingProvider || !pushSettings.enabled}
              variant="secondary"
            >
              <Bell class="size-4" />
              {t('settings.notifications.push.test.button')}
            </SettingsButton>
          </div>
        {/if}
      </div>
    {/if}
  </SettingsSection>
{/snippet}

<main class="settings-page-content" aria-label="Notifications settings configuration">
  <SettingsTabs {tabs} bind:activeTab />
</main>
