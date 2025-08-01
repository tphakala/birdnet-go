<!-- 
  SECURITY-HARDENED LoginModal Component
  
  Security improvements implemented:
  - Input validation and sanitization
  - Redirect URL validation
  - Secure error handling
  - Rate limiting protection
  - Secure state management
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

  // SECURITY: Define maximum password length to prevent DoS
  const MAX_PASSWORD_LENGTH = 1000;
  const MAX_REDIRECT_LENGTH = 2000;

  // SECURITY: Rate limiting for login attempts
  const MAX_ATTEMPTS = 5;
  const RATE_LIMIT_MINUTES = 15;

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

  // SECURITY: Rate limiting state
  let attemptCount = $state(0);
  let lastAttemptTime = $state(0);
  let isRateLimited = $state(false);

  // SECURITY: Validate redirect URL to prevent open redirects
  function validateRedirectUrl(url: string): boolean {
    try {
      // Only allow relative URLs starting with /
      if (!url.startsWith('/')) {
        return false;
      }

      // Prevent protocol-relative URLs
      if (url.startsWith('//')) {
        return false;
      }

      // Prevent javascript: or data: URLs
      if (url.toLowerCase().includes('javascript:') || url.toLowerCase().includes('data:')) {
        return false;
      }

      // Check length
      if (url.length > MAX_REDIRECT_LENGTH) {
        return false;
      }

      return true;
    } catch {
      return false;
    }
  }

  // SECURITY: Sanitize and validate password input
  function validatePassword(pwd: string): { isValid: boolean; error?: string } {
    if (!pwd) {
      return { isValid: false, error: t('auth.errors.passwordRequired') };
    }

    if (pwd.length > MAX_PASSWORD_LENGTH) {
      return { isValid: false, error: t('auth.errors.passwordTooLong') };
    }

    // Check for null bytes or other dangerous characters
    if (pwd.includes('\0') || pwd.includes('\r') || pwd.includes('\n')) {
      return { isValid: false, error: t('auth.errors.invalidCharacters') };
    }

    return { isValid: true };
  }

  // SECURITY: Check rate limiting
  function checkRateLimit(): boolean {
    const now = Date.now();
    const timeSinceLastAttempt = now - lastAttemptTime;

    // Reset attempts after rate limit period
    if (timeSinceLastAttempt > RATE_LIMIT_MINUTES * 60 * 1000) {
      attemptCount = 0;
      isRateLimited = false;
    }

    if (attemptCount >= MAX_ATTEMPTS) {
      isRateLimited = true;
      return false;
    }

    return true;
  }

  // SECURITY: Secure error message mapping
  function getSecureErrorMessage(err: unknown): string {
    // Never expose server error details to prevent information leakage
    if (err instanceof Error) {
      // Only show generic messages for security
      if (err.message.includes('401') || err.message.includes('Unauthorized')) {
        return t('auth.errors.invalidCredentials');
      }
      if (err.message.includes('429') || err.message.includes('rate limit')) {
        return t('auth.errors.tooManyAttempts');
      }
      if (err.message.includes('500') || err.message.includes('server')) {
        return t('auth.errors.serverError');
      }
    }

    // Generic fallback - never expose specific error details
    return t('auth.errors.loginFailed');
  }

  async function handlePasswordLogin() {
    if (isSubmitting) return;

    // SECURITY: Check rate limiting first
    if (!checkRateLimit()) {
      error = t('auth.errors.rateLimited', { minutes: RATE_LIMIT_MINUTES });
      return;
    }

    // SECURITY: Validate password
    const passwordValidation = validatePassword(password);
    if (!passwordValidation.isValid) {
      error = passwordValidation.error || t('auth.errors.invalidInput');
      return;
    }

    // SECURITY: Validate redirect URL
    const safeRedirectUrl = redirectUrl && validateRedirectUrl(redirectUrl) ? redirectUrl : '/';

    error = '';
    isSubmitting = true;
    lastAttemptTime = Date.now();
    attemptCount += 1;

    try {
      // SECURITY: Don't update auth state until server confirms success
      await api.post<{ message: string }>('/login', {
        password: password.trim(), // Remove whitespace
        redirect: safeRedirectUrl, // Use validated redirect
      });

      logger.info('Login successful');

      // SECURITY: Only update auth state after confirmed success
      authStore.setLoggedIn(true);
      authStore.setSecurity(true, true);

      // SECURITY: Reset rate limiting on successful login
      attemptCount = 0;
      isRateLimited = false;

      // Show success message briefly then close
      setTimeout(() => {
        onClose();
        // SECURITY: Use validated redirect URL
        if (safeRedirectUrl !== window.location.pathname) {
          window.location.href = safeRedirectUrl;
        }
      }, 1500);
    } catch (err: unknown) {
      logger.error('Login failed');
      // SECURITY: Use secure error messaging
      error = getSecureErrorMessage(err);
    } finally {
      isSubmitting = false;
    }
  }

  // SECURITY: Validate OAuth endpoints before redirect
  function handleOAuthLogin(provider: 'google' | 'github') {
    // SECURITY: Validate OAuth endpoints are properly configured
    const oauthEndpoints = {
      google: '/api/v1/auth/google',
      github: '/api/v1/auth/github',
    };

    const endpoint = oauthEndpoints[provider];

    // SECURITY: Basic endpoint validation
    if (!endpoint || !endpoint.startsWith('/api/v1/auth/')) {
      error = t('auth.errors.configurationError');
      return;
    }

    if (provider === 'google') {
      googleLoading = true;
    } else {
      githubLoading = true;
    }

    // Redirect to OAuth provider
    window.location.href = endpoint;
  }

  function handleSubmit(event: Event) {
    event.preventDefault();
    handlePasswordLogin();
  }

  // SECURITY: Secure state cleanup
  $effect(() => {
    if (!isOpen) {
      // Clear all sensitive state when modal closes
      password = '';
      error = '';
      isSubmitting = false;
      googleLoading = false;
      githubLoading = false;
      // Don't reset rate limiting state when modal closes
    }
  });
</script>

<Modal {isOpen} {onClose} size="md" showCloseButton={!isSubmitting && !isRateLimited}>
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
    <!-- SECURITY: Rate limiting notice -->
    {#if isRateLimited}
      <div class="alert alert-warning mb-4">
        <div class="flex items-center gap-2">
          {@html alertIcons.warning}
          <span>{t('auth.errors.rateLimited', { minutes: RATE_LIMIT_MINUTES })}</span>
        </div>
      </div>
    {:else}
      <form onsubmit={handleSubmit} class="space-y-4">
        {#if authConfig.basicEnabled}
          <PasswordField
            label={t('auth.password')}
            bind:value={password}
            onUpdate={val => (password = val)}
            placeholder={t('auth.passwordPlaceholder')}
            required
            disabled={isSubmitting || isRateLimited}
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

          <button
            type="submit"
            class="btn btn-primary w-full"
            disabled={isSubmitting || isRateLimited || !password}
          >
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
                onclick={() => handleOAuthLogin('google')}
                disabled={googleLoading || isRateLimited}
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
                onclick={() => handleOAuthLogin('github')}
                disabled={githubLoading || isRateLimited}
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
    {/if}
  {/snippet}
</Modal>
