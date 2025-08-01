<!-- 
  LoginModal Component
  
  Purpose: Modal dialog for user authentication
  
  Features:
  - Password-based authentication
  - OAuth authentication (Google/GitHub)
  - Error handling and validation
  - Loading states during authentication
  - CSRF protection via API utilities
  
  Props:
  - isOpen: boolean - Controls modal visibility
  - onClose: () => void - Callback when modal closes
  - redirectUrl?: string - URL to redirect after successful login
  - authConfig: Authentication configuration from server
  
  Usage:
  <LoginModal 
    isOpen={showLogin} 
    onClose={() => showLogin = false} 
    authConfig={config} 
  />
-->
<script lang="ts">
  import Modal from '../ui/Modal.svelte';
  import PasswordField from '../forms/PasswordField.svelte';
  import LoadingSpinner from '../ui/LoadingSpinner.svelte';
  import { api } from '$lib/utils/api';
  import { auth as authStore } from '$lib/stores/auth';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { alertIcons } from '$lib/utils/icons';

  const logger = loggers.auth;

  interface AuthConfig {
    basicEnabled: boolean;
    googleEnabled: boolean;
    githubEnabled: boolean;
  }

  interface Props {
    isOpen: boolean;
    onClose: () => void;
    redirectUrl?: string;
    authConfig?: AuthConfig;
  }

  let {
    isOpen = false,
    onClose,
    redirectUrl = '/',
    authConfig = { basicEnabled: true, googleEnabled: false, githubEnabled: false },
  }: Props = $props();

  let password = $state('');
  let error = $state('');
  let isSubmitting = $state(false);
  let googleLoading = $state(false);
  let githubLoading = $state(false);

  async function handlePasswordLogin() {
    if (isSubmitting || !password) return;

    error = '';
    isSubmitting = true;

    try {
      await api.post<{ message: string }>('/login', {
        password,
        redirect: redirectUrl,
      });

      logger.info('Login successful');

      // Update auth store
      authStore.setLoggedIn(true);
      authStore.setSecurity(true, true);

      // Show success message briefly then close
      setTimeout(() => {
        onClose();
        // Redirect if needed
        if (redirectUrl && redirectUrl !== window.location.pathname) {
          window.location.href = redirectUrl;
        }
      }, 1500);
    } catch (err: unknown) {
      logger.error('Login failed', err);
      if (err instanceof Error) {
        error = err.message || t('auth.errors.loginFailed');
      } else {
        error = t('auth.errors.loginFailed');
      }
    } finally {
      isSubmitting = false;
    }
  }

  function handleGoogleLogin() {
    googleLoading = true;
    // OAuth redirects to external provider
    window.location.href = '/api/v1/auth/google';
  }

  function handleGithubLogin() {
    githubLoading = true;
    // OAuth redirects to external provider
    window.location.href = '/api/v1/auth/github';
  }

  function handleSubmit(event: Event) {
    event.preventDefault();
    handlePasswordLogin();
  }

  // Reset state when modal opens/closes
  $effect(() => {
    if (!isOpen) {
      password = '';
      error = '';
      isSubmitting = false;
      googleLoading = false;
      githubLoading = false;
    }
  });
</script>

<Modal {isOpen} {onClose} size="md" showCloseButton={!isSubmitting}>
  {#snippet header()}
    <div class="flex items-center gap-4">
      <div
        class="mx-auto flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-full bg-primary sm:mx-0"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="24"
          height="24"
          viewBox="0 0 24 24"
          fill="none"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          class="lucide-lock-open stroke-base-100"
        >
          <rect width="18" height="11" x="3" y="11" rx="2" ry="2"></rect>
          <path d="M7 11V7a5 5 0 0 1 9.9-1"></path>
        </svg>
      </div>
      <h3 class="text-xl font-black">{t('auth.loginTitle')}</h3>
    </div>
  {/snippet}

  {#snippet children()}
    <form onsubmit={handleSubmit} class="space-y-4">
      {#if authConfig.basicEnabled}
        <PasswordField
          label={t('auth.password')}
          bind:value={password}
          onUpdate={val => (password = val)}
          placeholder={t('auth.passwordPlaceholder')}
          required
          disabled={isSubmitting}
          autocomplete="current-password"
          allowReveal
        />

        {#if error}
          <div class="alert alert-error">
            <div class="flex items-center gap-2">
              {@html alertIcons.error}
              <span>{error}</span>
            </div>
          </div>
        {/if}

        <button type="submit" class="btn btn-primary w-full" disabled={isSubmitting || !password}>
          {#if isSubmitting}
            <LoadingSpinner size="sm" />
          {/if}
          {t('auth.login')}
        </button>
      {/if}

      {#if authConfig.basicEnabled && (authConfig.googleEnabled || authConfig.githubEnabled)}
        <div class="divider">{t('auth.or')}</div>
      {/if}

      {#if authConfig.googleEnabled || authConfig.githubEnabled}
        <div class="flex flex-col gap-2">
          {#if authConfig.googleEnabled}
            <button
              type="button"
              class="btn btn-outline w-full"
              onclick={handleGoogleLogin}
              disabled={googleLoading}
            >
              {#if googleLoading}
                <LoadingSpinner size="sm" />
              {/if}
              {t('auth.loginWithGoogle')}
            </button>
          {/if}

          {#if authConfig.githubEnabled}
            <button
              type="button"
              class="btn btn-outline w-full"
              onclick={handleGithubLogin}
              disabled={githubLoading}
            >
              {#if githubLoading}
                <LoadingSpinner size="sm" />
              {/if}
              {t('auth.loginWithGithub')}
            </button>
          {/if}
        </div>
      {/if}
    </form>
  {/snippet}
</Modal>
