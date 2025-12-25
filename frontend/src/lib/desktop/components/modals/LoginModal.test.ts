import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  createComponentTestFactory,
  fireEvent,
  waitFor,
  screen,
} from '../../../../test/render-helpers';
import LoginModal from './LoginModal.svelte';

// Mock the api module
vi.mock('$lib/utils/api', () => ({
  api: {
    post: vi.fn(),
  },
}));

// Mock the logger
vi.mock('$lib/utils/logger', () => ({
  loggers: {
    auth: {
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
      debug: vi.fn(),
    },
  },
}));

// Mock the translation function
vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
}));

describe('LoginModal', () => {
  const loginModalTest = createComponentTestFactory(LoginModal);

  // Helper function to mock window.location
  function mockWindowLocation(pathname = '/ui/') {
    const mockLocation = {
      href: '',
      pathname,
      reload: vi.fn(),
    };
    Object.defineProperty(window, 'location', {
      value: mockLocation,
      writable: true,
    });
    return mockLocation;
  }

  beforeEach(() => {
    // Clear all mocks before each test
    vi.clearAllMocks();

    // Mock getComputedStyle to prevent DOM accessibility API errors
    Object.defineProperty(window, 'getComputedStyle', {
      value: vi.fn(() => ({
        getPropertyValue: vi.fn(() => ''),
        visibility: 'visible',
        display: 'block',
      })),
      writable: true,
    });

    // Mock focus trap functions for modal
    Object.defineProperty(HTMLElement.prototype, 'focus', {
      value: vi.fn(),
      writable: true,
    });

    // Mock localStorage
    const localStorageMock = {
      getItem: vi.fn(),
      setItem: vi.fn(),
      removeItem: vi.fn(),
      clear: vi.fn(),
    };
    Object.defineProperty(window, 'localStorage', {
      value: localStorageMock,
    });

    // Set up default window.location mock
    mockWindowLocation();
  });

  afterEach(() => {
    vi.resetAllMocks();
  });

  describe('Redirect URL Validation', () => {
    it('should accept valid relative URLs', () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '/ui/dashboard',
      });

      // Check that the hidden input contains the valid redirect URL
      const redirectInput = screen.getByDisplayValue('/ui/dashboard') as HTMLInputElement;
      expect(redirectInput).toBeDefined();
      expect(redirectInput.value).toBe('/ui/dashboard');
      expect(redirectInput.name).toBe('redirect');
    });

    it('should reject protocol-relative URLs and fallback to base path', () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '//evil.com/malicious',
      });

      // Should fallback to the detected base path (/ui/)
      const redirectInput = screen.getByDisplayValue('/ui/') as HTMLInputElement;
      expect(redirectInput).toBeDefined();
      expect(redirectInput.value).toBe('/ui/');
    });

    it('should reject javascript: URLs and fallback to base path', () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: 'javascript:alert("xss")',
      });

      // Should fallback to the detected base path
      const redirectInput = screen.getByDisplayValue('/ui/') as HTMLInputElement;
      expect(redirectInput).toBeDefined();
      expect(redirectInput.value).toBe('/ui/');
    });

    it('should reject data: URLs and fallback to base path', () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: 'data:text/html,<script>alert("xss")</script>',
      });

      // Should fallback to the detected base path
      const redirectInput = screen.getByDisplayValue('/ui/') as HTMLInputElement;
      expect(redirectInput).toBeDefined();
      expect(redirectInput.value).toBe('/ui/');
    });

    it('should reject URLs that are too long and fallback to base path', () => {
      const longUrl = '/' + 'a'.repeat(2001); // Exceeds MAX_REDIRECT_LENGTH
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: longUrl,
      });

      // Should fallback to the detected base path
      const redirectInput = screen.getByDisplayValue('/ui/') as HTMLInputElement;
      expect(redirectInput).toBeDefined();
      expect(redirectInput.value).toBe('/ui/');
    });

    it('should use different base paths based on current location', () => {
      // Change the mock location to /app/
      mockWindowLocation('/app/settings');

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: 'invalid-url', // No leading slash
      });

      // Should fallback to /app/ based on current location
      const redirectInput = screen.getByDisplayValue('/app/') as HTMLInputElement;
      expect(redirectInput).toBeDefined();
      expect(redirectInput.value).toBe('/app/');
    });
  });

  describe('Password Validation', () => {
    it('should reject empty passwords', async () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      await fireEvent.input(passwordInput, { target: { value: '' } });

      // Button should be disabled for empty password
      expect(loginButton).toBeDisabled();
    });

    it('should reject passwords with control characters', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      // Mock API to reject by default (shouldn't be called anyway)
      postSpy.mockRejectedValue(new Error('Should not call API'));

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');

      // Test with null byte (ASCII 0) which won't be trimmed
      const passwordWithNull = `password${String.fromCharCode(0)}test`;

      // Input password with control character
      await fireEvent.input(passwordInput, { target: { value: passwordWithNull } });

      // Attempt to submit
      const loginButton = screen.getByRole('button', { name: /continue with password/i });
      await fireEvent.click(loginButton);

      // Add a small delay to let any async operations complete
      await new Promise(resolve => setTimeout(resolve, 100));

      // Check if error is displayed
      const errorElement = screen.queryByRole('alert');
      expect(errorElement).toBeInTheDocument();
      expect(errorElement?.textContent).toBe('Password contains invalid characters');

      // Verify that the API was NOT called (validation should prevent it)
      expect(postSpy).not.toHaveBeenCalled();
    });

    it('should accept tab characters in passwords', async () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      await fireEvent.input(passwordInput, { target: { value: `password\t` } });

      expect(passwordInput).toBeDefined();
    });

    it('should reject passwords that are too long', async () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const longPassword = 'a'.repeat(513); // Exceeds MAX_PASSWORD_LENGTH (512)

      await fireEvent.input(passwordInput, { target: { value: longPassword } });

      expect(passwordInput).toBeDefined();
    });
  });

  // Rate limiting tests removed - rate limiting is now handled server-side only

  describe('OAuth Configuration', () => {
    it('should use default endpoints when none are configured', () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        authConfig: {
          basicEnabled: false,
          googleEnabled: true,
          githubEnabled: true,
        },
      });

      const googleButton = screen.getByRole('button', { name: /continueWithGoogle/i });
      const githubButton = screen.getByRole('button', { name: /continueWithGithub/i });

      expect(googleButton).toBeDefined();
      expect(githubButton).toBeDefined();
    });

    it('should use configured endpoints when provided', () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        authConfig: {
          basicEnabled: false,
          googleEnabled: true,
          githubEnabled: true,
          endpoints: {
            google: '/api/v1/auth/custom-google',
            github: '/api/v1/auth/custom-github',
          },
        },
      });

      const googleButton = screen.getByRole('button', { name: /continueWithGoogle/i });
      const githubButton = screen.getByRole('button', { name: /continueWithGithub/i });

      expect(googleButton).toBeDefined();
      expect(githubButton).toBeDefined();
    });

    it('should validate OAuth endpoints', async () => {
      const mockLocation = mockWindowLocation();

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        authConfig: {
          basicEnabled: false,
          googleEnabled: true,
          githubEnabled: false,
          endpoints: {
            google: '/malicious/endpoint',
          },
        },
      });

      const googleButton = screen.getByRole('button', { name: /continueWithGoogle/i });

      // Click the button
      await fireEvent.click(googleButton);

      // Give time for any async operations
      await new Promise(resolve => setTimeout(resolve, 100));

      // Should not redirect to malicious endpoint (validation should block it)
      expect(mockLocation.href).toBe('');
    });
  });

  describe('Focus Trap and Accessibility', () => {
    it('should render with proper ARIA attributes', () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
      });

      const dialog = screen.getByRole('dialog');
      expect(dialog).toHaveAttribute('aria-modal', 'true');
      expect(dialog).toHaveAttribute('aria-labelledby', 'modal-title');
    });

    it('should have a proper title for screen readers', () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
      });

      const title = screen.getByText('auth.loginTitle');
      expect(title).toHaveAttribute('id', 'modal-title');
    });

    it('should not render when isOpen is false', () => {
      loginModalTest.render({
        isOpen: false,
        onClose: vi.fn(),
      });

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('should call onClose when Escape key is pressed', async () => {
      const onCloseMock = vi.fn();

      loginModalTest.render({
        isOpen: true,
        onClose: onCloseMock,
      });

      const dialog = screen.getByRole('dialog');
      await fireEvent.keyDown(dialog, { key: 'Escape' });

      expect(onCloseMock).toHaveBeenCalled();
    });

    it('should call onClose when close button is clicked', async () => {
      const onCloseMock = vi.fn();

      loginModalTest.render({
        isOpen: true,
        onClose: onCloseMock,
      });

      const closeButton = screen.getByRole('button', { name: /close login dialog/i });
      await fireEvent.click(closeButton);

      expect(onCloseMock).toHaveBeenCalled();
    });
  });

  describe('Redirect URL Duplication Prevention', () => {
    it('should extract relative path when redirectUrl contains base path', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      postSpy.mockResolvedValue({ success: true, message: 'Login successful' });

      mockWindowLocation('/ui/dashboard');

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '/ui/dashboard', // Full path with base
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      await fireEvent.input(passwordInput, { target: { value: 'valid-password' } });
      await fireEvent.click(loginButton);

      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/v2/auth/login',
          expect.objectContaining({
            username: 'birdnet-client',
            password: 'valid-password',
            redirectUrl: '/dashboard', // Should be relative path
            basePath: '/ui/', // Should be base path
          })
        );
      });
    });

    it('should handle analytics subpage URLs correctly', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      postSpy.mockResolvedValue({ success: true, message: 'Login successful' });

      mockWindowLocation('/ui/analytics/species');

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '/ui/analytics/species',
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      await fireEvent.input(passwordInput, { target: { value: 'valid-password' } });
      await fireEvent.click(loginButton);

      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/v2/auth/login',
          expect.objectContaining({
            redirectUrl: '/analytics/species',
            basePath: '/ui/',
          })
        );
      });
    });

    it('should handle settings subpage URLs correctly', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      postSpy.mockResolvedValue({ success: true, message: 'Login successful' });

      mockWindowLocation('/ui/settings/main');

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '/ui/settings/main',
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      await fireEvent.input(passwordInput, { target: { value: 'valid-password' } });
      await fireEvent.click(loginButton);

      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/v2/auth/login',
          expect.objectContaining({
            redirectUrl: '/settings/main',
            basePath: '/ui/',
          })
        );
      });
    });

    it('should not modify relative URLs that do not contain base path', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      postSpy.mockResolvedValue({ success: true, message: 'Login successful' });

      mockWindowLocation('/ui/dashboard');

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '/custom/path', // Different from base path
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      await fireEvent.input(passwordInput, { target: { value: 'valid-password' } });
      await fireEvent.click(loginButton);

      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/v2/auth/login',
          expect.objectContaining({
            redirectUrl: '/custom/path', // Should remain unchanged
            basePath: '/ui/',
          })
        );
      });
    });

    it('should handle base path only redirectUrl correctly', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      postSpy.mockResolvedValue({ success: true, message: 'Login successful' });

      mockWindowLocation('/ui/');

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '/ui/', // Exactly matches base path
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      await fireEvent.input(passwordInput, { target: { value: 'valid-password' } });
      await fireEvent.click(loginButton);

      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/v2/auth/login',
          expect.objectContaining({
            redirectUrl: '/ui/', // Should remain unchanged as it equals base path
            basePath: '/ui/',
          })
        );
      });
    });

    it('should handle different base paths correctly', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      postSpy.mockResolvedValue({ success: true, message: 'Login successful' });

      mockWindowLocation('/app/dashboard');

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '/app/settings',
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      await fireEvent.input(passwordInput, { target: { value: 'valid-password' } });
      await fireEvent.click(loginButton);

      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/v2/auth/login',
          expect.objectContaining({
            redirectUrl: '/settings',
            basePath: '/app/',
          })
        );
      });
    });

    it('should ensure relative path starts with slash', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      postSpy.mockResolvedValue({ success: true, message: 'Login successful' });

      mockWindowLocation('/ui/');

      // Create a scenario where the relative path would not start with /
      // by setting up a redirectUrl that when base path is removed, doesn't start with /
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '/ui/dashboard',
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      await fireEvent.input(passwordInput, { target: { value: 'valid-password' } });
      await fireEvent.click(loginButton);

      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/v2/auth/login',
          expect.objectContaining({
            redirectUrl: '/dashboard', // Should start with /
            basePath: '/ui/',
          })
        );
      });

      // Verify that the redirectUrl always starts with '/'
      const [, payload] = postSpy.mock.calls[0] as [
        string,
        { redirectUrl: string; basePath: string },
      ];
      expect(payload.redirectUrl).toMatch(/^\/.*$/);
    });
  });

  describe('Form Submission', () => {
    it('should prevent form submission when validation fails', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      // Submit form by clicking the submit button since form role may not be recognized
      const submitButton = screen.getByRole('button', { name: /continue with password/i });
      await fireEvent.click(submitButton);

      // Should not call API with empty password
      expect(postSpy).not.toHaveBeenCalled();
    });

    it('should call API when form is valid', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      postSpy.mockResolvedValue({
        success: true,
        message: 'Login successful',
        redirectUrl: '/api/v2/auth/callback?code=123&redirect=/ui/',
      });

      mockWindowLocation();

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      await fireEvent.input(passwordInput, { target: { value: 'valid-password' } });
      await fireEvent.click(loginButton);

      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/v2/auth/login',
          expect.objectContaining({
            username: 'birdnet-client',
            password: 'valid-password',
          })
        );
      });
    });

    it('should handle API errors gracefully', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      postSpy.mockRejectedValue(new Error('Invalid credentials'));

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      await fireEvent.input(passwordInput, { target: { value: 'wrong-password' } });
      await fireEvent.click(loginButton);

      await waitFor(() => {
        expect(screen.getByText('Invalid credentials. Please try again.')).toBeInTheDocument();
      });
    });
  });

  describe('Integration Tests - URL Preservation', () => {
    it('should preserve the complete URL path after successful login without OAuth callback', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      const onCloseMock = vi.fn();
      const mockLocation = mockWindowLocation('/ui/analytics/species');

      // Mock successful login without OAuth callback
      postSpy.mockResolvedValue({
        success: true,
        message: 'Login successful',
        // No redirectUrl in response means standard login flow
      });

      loginModalTest.render({
        isOpen: true,
        onClose: onCloseMock,
        redirectUrl: '/ui/analytics/species', // User was on analytics/species page
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      // Perform login
      await fireEvent.input(passwordInput, { target: { value: 'valid-password' } });
      await fireEvent.click(loginButton);

      // Verify API call with correct URL structure
      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/v2/auth/login',
          expect.objectContaining({
            username: 'birdnet-client',
            password: 'valid-password',
            redirectUrl: '/analytics/species', // Relative path prevents duplication
            basePath: '/ui/', // Base path sent separately
          })
        );
      });

      // Verify modal closes after successful login
      await waitFor(() => {
        expect(onCloseMock).toHaveBeenCalled();
      });

      // Verify page refresh is called (simulating successful login)
      await new Promise(resolve => setTimeout(resolve, 600)); // Wait for timeout
      expect(mockLocation.reload).toHaveBeenCalled();
    });

    it('should handle OAuth callback redirect preserving original URL', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      const mockLocation = mockWindowLocation('/ui/settings/main');

      // Mock successful login with OAuth callback
      postSpy.mockResolvedValue({
        success: true,
        message: 'Login successful',
        redirectUrl: '/api/v2/auth/callback?code=123&state=settings%2Fmain', // OAuth callback
      });

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '/ui/settings/main', // User was on settings page
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      // Perform login
      await fireEvent.input(passwordInput, { target: { value: 'valid-password' } });
      await fireEvent.click(loginButton);

      // Verify API call
      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/v2/auth/login',
          expect.objectContaining({
            redirectUrl: '/settings/main', // Relative path
            basePath: '/ui/',
          })
        );
      });

      // Verify OAuth redirect happens immediately
      expect(mockLocation.href).toBe('/api/v2/auth/callback?code=123&state=settings%2Fmain');
    });

    it('should handle edge case of user on root path', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      postSpy.mockResolvedValue({ success: true, message: 'Login successful' });

      mockWindowLocation('/ui/');

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '/ui/', // User on root UI path
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      await fireEvent.input(passwordInput, { target: { value: 'valid-password' } });
      await fireEvent.click(loginButton);

      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/v2/auth/login',
          expect.objectContaining({
            redirectUrl: '/ui/', // Should remain as-is when it equals base path
            basePath: '/ui/',
          })
        );
      });
    });

    it('should handle complex nested URLs correctly', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      postSpy.mockResolvedValue({ success: true, message: 'Login successful' });

      mockWindowLocation('/ui/detections/12345');

      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '/ui/detections/12345', // Detection detail page
        authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
      });

      const passwordInput = screen.getByLabelText('auth.password');
      const loginButton = screen.getByRole('button', { name: /continue with password/i });

      await fireEvent.input(passwordInput, { target: { value: 'valid-password' } });
      await fireEvent.click(loginButton);

      await waitFor(() => {
        expect(postSpy).toHaveBeenCalledWith(
          '/api/v2/auth/login',
          expect.objectContaining({
            redirectUrl: '/detections/12345', // Relative path
            basePath: '/ui/',
          })
        );
      });
    });
  });

  describe('Component Props', () => {
    it('should use default props when none are provided', () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
      });

      // Should render with default auth config (basic auth enabled)
      expect(screen.getByLabelText('auth.password')).toBeInTheDocument();
    });

    it('should respect custom redirectUrl prop', () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        redirectUrl: '/custom/redirect',
      });

      const hiddenInput = screen.getByDisplayValue('/custom/redirect');
      expect(hiddenInput).toBeInTheDocument();
    });

    it('should show only enabled auth methods', () => {
      loginModalTest.render({
        isOpen: true,
        onClose: vi.fn(),
        authConfig: {
          basicEnabled: false,
          googleEnabled: true,
          githubEnabled: false,
        },
      });

      expect(screen.queryByLabelText('auth.password')).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: /continueWithGoogle/i })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /continueWithGithub/i })).not.toBeInTheDocument();
    });
  });
});
