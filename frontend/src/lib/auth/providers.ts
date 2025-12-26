/**
 * OAuth Provider Registry
 *
 * Centralized registry for OAuth authentication providers.
 * This is the single source of truth for provider configuration,
 * used by both LoginModal and SecuritySettingsPage.
 */

import type { Component } from 'svelte';
import GoogleIcon from './icons/GoogleIcon.svelte';
import GithubIcon from './icons/GithubIcon.svelte';
import MicrosoftIcon from './icons/MicrosoftIcon.svelte';

/**
 * OAuth provider definition with all metadata needed for
 * login buttons and settings configuration.
 */
export interface AuthProvider {
  /** Provider ID matching goth provider name (google, github, microsoft) */
  id: string;
  /** Display name for the provider */
  name: string;
  /** Svelte component for provider icon */
  icon: Component;
  /** i18n key for login button text (e.g., 'auth.continueWithGoogle') */
  loginButtonKey: string;
  /** OAuth endpoint path (e.g., '/auth/google') */
  authEndpoint: string;
  /** Settings page configuration */
  settings: {
    /** i18n key for settings section title */
    titleKey: string;
    /** i18n key for enable checkbox label */
    enableLabelKey: string;
    /** i18n key for redirect URI section title */
    redirectUriTitleKey: string;
    /** i18n key for "get credentials" link text */
    getCredentialsLabelKey: string;
    /** URL to provider's credential management console */
    credentialsUrl: string;
    /** OAuth callback path for redirect URI display */
    callbackPath: string;
    /** i18n key for client ID field label */
    clientIdLabelKey: string;
    /** i18n key for client ID help text */
    clientIdHelpTextKey: string;
    /** i18n key for client secret field label */
    clientSecretLabelKey: string;
    /** i18n key for client secret help text */
    clientSecretHelpTextKey: string;
    /** i18n key for user ID field label */
    userIdLabelKey: string;
  };
}

/**
 * Registry of all supported OAuth providers.
 * Add new providers here - no changes needed to LoginModal or SecuritySettingsPage.
 */
export const AUTH_PROVIDERS: Record<string, AuthProvider> = {
  google: {
    id: 'google',
    name: 'Google',
    icon: GoogleIcon,
    loginButtonKey: 'auth.continueWithGoogle',
    authEndpoint: '/auth/google',
    settings: {
      titleKey: 'settings.security.oauth.google.title',
      enableLabelKey: 'settings.security.oauth.google.enableLabel',
      redirectUriTitleKey: 'settings.security.oauth.google.redirectUriTitle',
      getCredentialsLabelKey: 'settings.security.oauth.google.getCredentialsLabel',
      credentialsUrl: 'https://console.cloud.google.com/apis/credentials',
      callbackPath: '/auth/google/callback',
      clientIdLabelKey: 'settings.security.oauth.google.clientIdLabel',
      clientIdHelpTextKey: 'settings.security.oauth.google.clientIdHelpText',
      clientSecretLabelKey: 'settings.security.oauth.google.clientSecretLabel',
      clientSecretHelpTextKey: 'settings.security.oauth.google.clientSecretHelpText',
      userIdLabelKey: 'settings.security.oauth.google.userIdLabel',
    },
  },
  github: {
    id: 'github',
    name: 'GitHub',
    icon: GithubIcon,
    loginButtonKey: 'auth.continueWithGithub',
    authEndpoint: '/auth/github',
    settings: {
      titleKey: 'settings.security.oauth.github.title',
      enableLabelKey: 'settings.security.oauth.github.enableLabel',
      redirectUriTitleKey: 'settings.security.oauth.github.redirectUriTitle',
      getCredentialsLabelKey: 'settings.security.oauth.github.getCredentialsLabel',
      credentialsUrl: 'https://github.com/settings/developers',
      callbackPath: '/auth/github/callback',
      clientIdLabelKey: 'settings.security.oauth.github.clientIdLabel',
      clientIdHelpTextKey: 'settings.security.oauth.github.clientIdHelpText',
      clientSecretLabelKey: 'settings.security.oauth.github.clientSecretLabel',
      clientSecretHelpTextKey: 'settings.security.oauth.github.clientSecretHelpText',
      userIdLabelKey: 'settings.security.oauth.github.userIdLabel',
    },
  },
  microsoft: {
    id: 'microsoft',
    name: 'Microsoft',
    icon: MicrosoftIcon,
    loginButtonKey: 'auth.continueWithMicrosoft',
    authEndpoint: '/auth/microsoftonline',
    settings: {
      titleKey: 'settings.security.oauth.microsoft.title',
      enableLabelKey: 'settings.security.oauth.microsoft.enableLabel',
      redirectUriTitleKey: 'settings.security.oauth.microsoft.redirectUriTitle',
      getCredentialsLabelKey: 'settings.security.oauth.microsoft.getCredentialsLabel',
      credentialsUrl:
        'https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps/ApplicationsListBlade',
      callbackPath: '/auth/microsoftonline/callback',
      clientIdLabelKey: 'settings.security.oauth.microsoft.clientIdLabel',
      clientIdHelpTextKey: 'settings.security.oauth.microsoft.clientIdHelpText',
      clientSecretLabelKey: 'settings.security.oauth.microsoft.clientSecretLabel',
      clientSecretHelpTextKey: 'settings.security.oauth.microsoft.clientSecretHelpText',
      userIdLabelKey: 'settings.security.oauth.microsoft.userIdLabel',
    },
  },
};

/**
 * Get providers that are currently enabled.
 * @param enabledIds - Array of enabled provider IDs from backend config
 * @returns Array of AuthProvider objects for enabled providers
 */
export function getEnabledProviders(enabledIds: string[] | undefined): AuthProvider[] {
  if (!enabledIds) {
    return [];
  }
  return enabledIds
    .map(id => {
      // eslint-disable-next-line security/detect-object-injection
      const provider = AUTH_PROVIDERS[id] as AuthProvider | undefined;
      return provider;
    })
    .filter((p): p is AuthProvider => p !== undefined);
}

/**
 * Get all registered providers.
 * @returns Array of all AuthProvider objects
 */
export function getAllProviders(): AuthProvider[] {
  return Object.values(AUTH_PROVIDERS);
}

/**
 * Get a specific provider by ID.
 * @param id - Provider ID
 * @returns AuthProvider or undefined if not found
 */
export function getProvider(id: string): AuthProvider | undefined {
  // eslint-disable-next-line security/detect-object-injection
  return AUTH_PROVIDERS[id] as AuthProvider | undefined;
}
