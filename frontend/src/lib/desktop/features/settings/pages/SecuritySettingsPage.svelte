<!--
  Security Settings Page Component
  
  Purpose: Configure authentication and access control for BirdNET-Go including
  HTTPS/TLS settings, basic authentication, OAuth2 social login providers, and
  subnet-based authentication bypass.
  
  Features:
  - Server configuration with automatic TLS via Let's Encrypt
  - Basic authentication with password protection
  - OAuth2 integration (Google, GitHub) with user restrictions
  - Subnet-based authentication bypass for local networks
  - Dynamic redirect URI generation based on host settings
  - Real-time validation and change detection
  
  Props: None - This is a page component that uses global settings stores
  
  Performance Optimizations:
  - Removed page-level loading spinner to prevent flickering
  - Reactive settings with $derived instead of $state + $effect
  - Cached CSRF token to avoid repeated DOM queries
  - Reactive change detection with $derived
  - Dynamic redirect URI generation based on current host
  
  @component
-->
<script lang="ts">
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import PasswordField from '$lib/desktop/components/forms/PasswordField.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import {
    settingsStore,
    settingsActions,
    securitySettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import { alertIconsSvg, systemIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts
  import { t } from '$lib/i18n';


  // PERFORMANCE OPTIMIZATION: Reactive settings with proper defaults
  let settings = $derived(
    $securitySettings || {
      host: '',
      autoTls: false,
      basicAuth: {
        enabled: false,
        username: '',
        password: '',
      },
      googleAuth: {
        enabled: false,
        clientId: '',
        clientSecret: '',
        userId: '',
      },
      githubAuth: {
        enabled: false,
        clientId: '',
        clientSecret: '',
        userId: '',
      },
      allowSubnetBypass: {
        enabled: false,
        subnet: '',
      },
    }
  );

  let store = $derived($settingsStore);

  // PERFORMANCE OPTIMIZATION: Reactive change detection with $derived
  let serverConfigHasChanges = $derived(
    hasSettingsChanged(
      {
        host: (store.originalData as any)?.security?.host,
        autoTls: (store.originalData as any)?.security?.autoTls,
      },
      {
        host: (store.formData as any)?.security?.host,
        autoTls: (store.formData as any)?.security?.autoTls,
      }
    )
  );

  let basicAuthHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.security?.basicAuth,
      (store.formData as any)?.security?.basicAuth
    )
  );

  let oauthHasChanges = $derived(
    hasSettingsChanged(
      {
        googleAuth: (store.originalData as any)?.security?.googleAuth,
        githubAuth: (store.originalData as any)?.security?.githubAuth,
      },
      {
        googleAuth: (store.formData as any)?.security?.googleAuth,
        githubAuth: (store.formData as any)?.security?.githubAuth,
      }
    )
  );

  let subnetBypassHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.security?.allowSubnetBypass,
      (store.formData as any)?.security?.allowSubnetBypass
    )
  );

  // PERFORMANCE OPTIMIZATION: Generate redirect URIs dynamically with $derived
  let currentHost = $derived(
    typeof window !== 'undefined' 
      ? window.location.origin 
      : settings?.host 
        ? `https://${settings.host}` 
        : 'https://your-domain.com'
  );
  
  let googleRedirectURI = $derived(`${currentHost}/auth/google/callback`);
  let githubRedirectURI = $derived(`${currentHost}/auth/github/callback`);

  // Server Configuration update handlers
  function updateAutoTLSEnabled(enabled: boolean) {
    settingsActions.updateSection('security', {
      ...settings,
      autoTls: enabled,
    });
  }

  function updateAutoTLSHost(host: string) {
    settingsActions.updateSection('security', {
      ...settings,
      host: host,
    });
  }

  // Basic Auth update handlers
  function updateBasicAuthEnabled(enabled: boolean) {
    settingsActions.updateSection('security', {
      ...settings,
      basicAuth: { ...settings.basicAuth, enabled },
    });
  }

  function updateBasicAuthPassword(password: string) {
    settingsActions.updateSection('security', {
      ...settings,
      basicAuth: { ...settings.basicAuth, password },
    });
  }

  // Google OAuth update handlers
  function updateGoogleAuthEnabled(enabled: boolean) {
    settingsActions.updateSection('security', {
      ...settings,
      googleAuth: { ...settings.googleAuth, enabled },
    });
  }

  function updateGoogleClientId(clientId: string) {
    settingsActions.updateSection('security', {
      ...settings,
      googleAuth: { ...settings.googleAuth, clientId },
    });
  }

  function updateGoogleClientSecret(clientSecret: string) {
    settingsActions.updateSection('security', {
      ...settings,
      googleAuth: { ...settings.googleAuth, clientSecret },
    });
  }

  function updateGoogleUserId(userId: string) {
    settingsActions.updateSection('security', {
      ...settings,
      googleAuth: { ...(settings.googleAuth as any), userId },
    });
  }

  // GitHub OAuth update handlers
  function updateGithubAuthEnabled(enabled: boolean) {
    settingsActions.updateSection('security', {
      ...settings,
      githubAuth: { ...settings.githubAuth, enabled },
    });
  }

  function updateGithubClientId(clientId: string) {
    settingsActions.updateSection('security', {
      ...settings,
      githubAuth: { ...settings.githubAuth, clientId },
    });
  }

  function updateGithubClientSecret(clientSecret: string) {
    settingsActions.updateSection('security', {
      ...settings,
      githubAuth: { ...settings.githubAuth, clientSecret },
    });
  }

  function updateGithubUserId(userId: string) {
    settingsActions.updateSection('security', {
      ...settings,
      githubAuth: { ...(settings.githubAuth as any), userId },
    });
  }

  // Subnet Bypass update handlers
  function updateSubnetBypassEnabled(enabled: boolean) {
    settingsActions.updateSection('security', {
      ...settings,
      allowSubnetBypass: { ...settings.allowSubnetBypass, enabled },
    });
  }

  function updateSubnetBypassSubnet(subnet: string) {
    settingsActions.updateSection('security', {
      ...settings,
      allowSubnetBypass: { ...settings.allowSubnetBypass, subnet },
    });
  }
</script>

<!-- Remove page-level loading spinner to prevent flickering -->
<div class="space-y-4">
    <!-- Server Configuration -->
    <SettingsSection
      title={t('settings.security.serverConfiguration.title')}
    description={t('settings.security.serverConfiguration.description')}
    defaultOpen={true}
    hasChanges={serverConfigHasChanges}
  >
    <div class="space-y-4">
      <!-- Host Address -->
      <TextInput
        id="host-address"
        bind:value={settings.host}
        label={t('settings.security.hostLabel')}
        placeholder={t('settings.security.placeholders.host')}
        disabled={store.isLoading || store.isSaving}
        onchange={() => updateAutoTLSHost(settings.host)}
      />

      <div class="border-t border-base-300 pt-4 mt-4">
        <h4 class="text-lg font-medium mb-2">{t('settings.security.httpsSettingsTitle')}</h4>
        <p class="text-sm text-base-content/70 mb-4">{t('settings.security.httpsSettingsDescription')}</p>

        <Checkbox
          bind:checked={settings.autoTls}
          label={t('settings.security.serverConfiguration.autoTlsLabel')}
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateAutoTLSEnabled(settings.autoTls)}
        />

        {#if settings.autoTls}
          <SettingsNote>
            <p><strong>{t('settings.security.serverConfiguration.autoTlsRequirements.title')}</strong></p>
            <ul class="list-disc list-inside mt-1">
              <li>{t('settings.security.serverConfiguration.autoTlsRequirements.domainRequired')}</li>
              <li>{t('settings.security.serverConfiguration.autoTlsRequirements.domainPointing')}</li>
              <li>{t('settings.security.serverConfiguration.autoTlsRequirements.portsAccessible')}</li>
            </ul>
          </SettingsNote>
        {/if}
      </div>
    </div>
  </SettingsSection>

  <!-- Basic Authentication -->
  <SettingsSection
    title={t('settings.security.basicAuthentication.title')}
    description={t('settings.security.basicAuthentication.description')}
    defaultOpen={false}
    hasChanges={basicAuthHasChanges}
  >
    <div class="space-y-4">
      <Checkbox
        bind:checked={settings.basicAuth.enabled}
        label={t('settings.security.basicAuthentication.enableLabel')}
        disabled={store.isLoading || store.isSaving}
        onchange={() => updateBasicAuthEnabled(settings.basicAuth.enabled)}
      />

      {#if settings.basicAuth?.enabled}
        <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
          <PasswordField
            label={t('settings.security.basicAuthentication.passwordLabel')}
            value={settings.basicAuth.password}
            onUpdate={updateBasicAuthPassword}
            placeholder=""
            helpText={t('settings.security.basicAuthentication.passwordHelpText')}
            disabled={store.isLoading || store.isSaving}
            allowReveal={true}
          />
        </div>
      {/if}
    </div>
  </SettingsSection>

  <!-- OAuth2 Social Authentication -->
  <SettingsSection
    title={t('settings.security.oauth.title')}
    description={t('settings.security.oauth.description')}
    defaultOpen={false}
    hasChanges={oauthHasChanges}
  >
    <div class="space-y-6">
      <!-- Google Auth -->
      <div class="border border-base-300 rounded-lg p-4">
        <h4 class="text-lg font-medium mb-4 flex items-center gap-3">
          <svg class="w-6 h-6" viewBox="0 0 24 24">
            <path
              fill="#4285F4"
              d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"
            />
            <path
              fill="#34A853"
              d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"
            />
            <path
              fill="#FBBC05"
              d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z"
            />
            <path
              fill="#EA4335"
              d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z"
            />
          </svg>
          {t('settings.security.oauth.google.title')}
        </h4>

        <Checkbox
          bind:checked={settings.googleAuth.enabled}
          label={t('settings.security.oauth.google.enableLabel')}
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateGoogleAuthEnabled(settings.googleAuth.enabled)}
        />

        {#if settings.googleAuth?.enabled}
          <div class="mt-4 space-y-4">
            <!-- Redirect URI Information -->
            <div class="bg-base-200 p-3 rounded-lg">
              <div class="text-sm">
                <p class="font-medium mb-1">{t('settings.security.oauth.google.redirectUriTitle')}</p>
                <code class="text-xs bg-base-300 px-2 py-1 rounded">{googleRedirectURI}</code>
              </div>
              <a
                href="https://console.cloud.google.com/apis/credentials"
                target="_blank"
                rel="noopener"
                class="text-sm text-primary hover:text-primary-focus inline-flex items-center mt-2"
              >
                {t('settings.security.oauth.google.getCredentialsLabel')}
                {@html systemIcons.externalLink}
              </a>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
              <PasswordField
                label={t('settings.security.oauth.google.clientIdLabel')}
                value={settings.googleAuth.clientId}
                onUpdate={updateGoogleClientId}
                placeholder=""
                helpText={t('settings.security.oauth.google.clientIdHelpText')}
                disabled={store.isLoading || store.isSaving}
                allowReveal={true}
              />

              <PasswordField
                label={t('settings.security.oauth.google.clientSecretLabel')}
                value={settings.googleAuth.clientSecret}
                onUpdate={updateGoogleClientSecret}
                placeholder=""
                helpText={t('settings.security.oauth.google.clientSecretHelpText')}
                disabled={store.isLoading || store.isSaving}
                allowReveal={true}
              />
            </div>

            <TextInput
              id="google-user-id"
              bind:value={(settings.googleAuth as any).userId}
              label={t('settings.security.oauth.google.userIdLabel')}
              placeholder={t('settings.security.placeholders.allowedUsers')}
              disabled={store.isLoading || store.isSaving}
              onchange={() => updateGoogleUserId((settings.googleAuth as any).userId || '')}
            />
          </div>
        {/if}
      </div>

      <!-- GitHub Auth -->
      <div class="border border-base-300 rounded-lg p-4">
        <h4 class="text-lg font-medium mb-4 flex items-center gap-3">
          <svg class="w-6 h-6" fill="currentColor" viewBox="0 0 24 24">
            <path
              d="M12 0C5.374 0 0 5.373 0 12 0 17.302 3.438 21.8 8.207 23.387c.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23A11.509 11.509 0 0112 5.803c1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576C20.566 21.797 24 17.3 24 12c0-6.627-5.373-12-12-12z"
            />
          </svg>
          {t('settings.security.oauth.github.title')}
        </h4>

        <Checkbox
          bind:checked={settings.githubAuth.enabled}
          label={t('settings.security.oauth.github.enableLabel')}
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateGithubAuthEnabled(settings.githubAuth.enabled)}
        />

        {#if settings.githubAuth?.enabled}
          <div class="mt-4 space-y-4">
            <!-- Redirect URI Information -->
            <div class="bg-base-200 p-3 rounded-lg">
              <div class="text-sm">
                <p class="font-medium mb-1">{t('settings.security.oauth.github.redirectUriTitle')}</p>
                <code class="text-xs bg-base-300 px-2 py-1 rounded">{githubRedirectURI}</code>
              </div>
              <a
                href="https://github.com/settings/developers"
                target="_blank"
                rel="noopener"
                class="text-sm text-primary hover:text-primary-focus inline-flex items-center mt-2"
              >
                {t('settings.security.oauth.github.getCredentialsLabel')}
                {@html systemIcons.externalLink}
              </a>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
              <PasswordField
                label={t('settings.security.oauth.github.clientIdLabel')}
                value={settings.githubAuth.clientId}
                onUpdate={updateGithubClientId}
                placeholder=""
                helpText={t('settings.security.oauth.github.clientIdHelpText')}
                disabled={store.isLoading || store.isSaving}
                allowReveal={true}
              />

              <PasswordField
                label={t('settings.security.oauth.github.clientSecretLabel')}
                value={settings.githubAuth.clientSecret}
                onUpdate={updateGithubClientSecret}
                placeholder=""
                helpText={t('settings.security.oauth.github.clientSecretHelpText')}
                disabled={store.isLoading || store.isSaving}
                allowReveal={true}
              />
            </div>

            <TextInput
              id="github-user-id"
              bind:value={(settings.githubAuth as any).userId}
              label={t('settings.security.oauth.github.userIdLabel')}
              placeholder={t('settings.security.placeholders.allowedUsers')}
              disabled={store.isLoading || store.isSaving}
              onchange={() => updateGithubUserId((settings.githubAuth as any).userId || '')}
            />
          </div>
        {/if}
      </div>
    </div>
  </SettingsSection>

  <!-- Bypass Authentication -->
  <SettingsSection
    title={t('settings.security.bypassAuthentication.title')}
    description={t('settings.security.bypassAuthentication.description')}
    defaultOpen={false}
    hasChanges={subnetBypassHasChanges}
  >
    <div class="space-y-4">
      <Checkbox
        bind:checked={settings.allowSubnetBypass.enabled}
        label={t('settings.security.allowSubnetBypassLabel')}
        disabled={store.isLoading || store.isSaving}
        onchange={() => updateSubnetBypassEnabled(settings.allowSubnetBypass.enabled)}
      />

      {#if settings.allowSubnetBypass?.enabled}
        <div class="ml-7">
          <TextInput
            id="allowed-subnet"
            bind:value={settings.allowSubnetBypass.subnet}
            label={t('settings.security.allowedSubnetsLabel')}
            placeholder={t('settings.security.placeholders.subnet')}
            disabled={store.isLoading || store.isSaving}
            onchange={() => updateSubnetBypassSubnet(settings.allowSubnetBypass.subnet || '')}
          />
          <div class="text-sm text-base-content/70 mt-1">
            {t('settings.security.allowedSubnetsHelp')}
          </div>
        </div>

        <div class="alert alert-warning">
          {@html alertIconsSvg.warning}
          <span>
            <strong>{t('settings.security.securityWarningTitle')}</strong> {t('settings.security.subnetWarningText')}
          </span>
        </div>
      {/if}
    </div>
  </SettingsSection>
  </div>
