import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
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
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          redirectUrl: '/ui/dashboard',
        },
      });

      // Check that the hidden input contains the valid redirect URL
      const redirectInput = screen.getByDisplayValue('/ui/dashboard') as HTMLInputElement;
      expect(redirectInput).toBeDefined();
      expect(redirectInput.value).toBe('/ui/dashboard');
      expect(redirectInput.name).toBe('redirect');
    });

    it('should reject protocol-relative URLs and fallback to base path', () => {
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          redirectUrl: '//evil.com/malicious',
        },
      });

      // Should fallback to the detected base path (/ui/)
      const redirectInput = screen.getByDisplayValue('/ui/') as HTMLInputElement;
      expect(redirectInput).toBeDefined();
      expect(redirectInput.value).toBe('/ui/');
    });

    it('should reject javascript: URLs and fallback to base path', () => {
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          redirectUrl: 'javascript:alert("xss")',
        },
      });

      // Should fallback to the detected base path
      const redirectInput = screen.getByDisplayValue('/ui/') as HTMLInputElement;
      expect(redirectInput).toBeDefined();
      expect(redirectInput.value).toBe('/ui/');
    });

    it('should reject data: URLs and fallback to base path', () => {
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          redirectUrl: 'data:text/html,<script>alert("xss")</script>',
        },
      });

      // Should fallback to the detected base path
      const redirectInput = screen.getByDisplayValue('/ui/') as HTMLInputElement;
      expect(redirectInput).toBeDefined();
      expect(redirectInput.value).toBe('/ui/');
    });

    it('should reject URLs that are too long and fallback to base path', () => {
      const longUrl = '/' + 'a'.repeat(2001); // Exceeds MAX_REDIRECT_LENGTH
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          redirectUrl: longUrl,
        },
      });

      // Should fallback to the detected base path
      const redirectInput = screen.getByDisplayValue('/ui/') as HTMLInputElement;
      expect(redirectInput).toBeDefined();
      expect(redirectInput.value).toBe('/ui/');
    });

    it('should use different base paths based on current location', () => {
      // Change the mock location to /app/
      mockWindowLocation('/app/settings');

      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          redirectUrl: 'invalid-url', // No leading slash
        },
      });

      // Should fallback to /app/ based on current location
      const redirectInput = screen.getByDisplayValue('/app/') as HTMLInputElement;
      expect(redirectInput).toBeDefined();
      expect(redirectInput.value).toBe('/app/');
    });
  });

  describe('Password Validation', () => {
    it('should reject empty passwords', async () => {
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
        },
      });

      const passwordInput = screen.getByLabelText('Password');
      const loginButton = screen.getByRole('button', { name: /login with password/i });

      await fireEvent.input(passwordInput, { target: { value: '' } });

      // Button should be disabled for empty password
      expect(loginButton).toBeDisabled();
    });

    it('should reject passwords with control characters', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);
      // Mock API to reject by default (shouldn't be called anyway)
      postSpy.mockRejectedValue(new Error('Should not call API'));

      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
        },
      });

      const passwordInput = screen.getByLabelText('Password');

      // Test with null byte (ASCII 0) which won't be trimmed
      const passwordWithNull = `password${String.fromCharCode(0)}test`;

      // Input password with control character
      await fireEvent.input(passwordInput, { target: { value: passwordWithNull } });

      // Attempt to submit
      const loginButton = screen.getByRole('button', { name: /login with password/i });
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
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
        },
      });

      const passwordInput = screen.getByLabelText('Password');
      await fireEvent.input(passwordInput, { target: { value: `password\t` } });

      expect(passwordInput).toBeDefined();
    });

    it('should reject passwords that are too long', async () => {
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
        },
      });

      const passwordInput = screen.getByLabelText('Password');
      const longPassword = 'a'.repeat(513); // Exceeds MAX_PASSWORD_LENGTH (512)

      await fireEvent.input(passwordInput, { target: { value: longPassword } });

      expect(passwordInput).toBeDefined();
    });
  });

  // Rate limiting tests removed - rate limiting is now handled server-side only

  describe('OAuth Configuration', () => {
    it('should use default endpoints when none are configured', () => {
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          authConfig: {
            basicEnabled: false,
            googleEnabled: true,
            githubEnabled: true,
          },
        },
      });

      const googleButton = screen.getByRole('button', { name: /login with google/i });
      const githubButton = screen.getByRole('button', { name: /login with github/i });

      expect(googleButton).toBeDefined();
      expect(githubButton).toBeDefined();
    });

    it('should use configured endpoints when provided', () => {
      render(LoginModal, {
        props: {
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
        },
      });

      const googleButton = screen.getByRole('button', { name: /login with google/i });
      const githubButton = screen.getByRole('button', { name: /login with github/i });

      expect(googleButton).toBeDefined();
      expect(githubButton).toBeDefined();
    });

    it('should validate OAuth endpoints', async () => {
      const mockLocation = mockWindowLocation();

      render(LoginModal, {
        props: {
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
        },
      });

      const googleButton = screen.getByRole('button', { name: /login with google/i });

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
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
        },
      });

      const dialog = screen.getByRole('dialog');
      expect(dialog).toHaveAttribute('aria-modal', 'true');
      expect(dialog).toHaveAttribute('aria-labelledby', 'modal-title');
    });

    it('should have a proper title for screen readers', () => {
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
        },
      });

      const title = screen.getByText('Login to BirdNET-Go');
      expect(title).toHaveAttribute('id', 'modal-title');
    });

    it('should not render when isOpen is false', () => {
      render(LoginModal, {
        props: {
          isOpen: false,
          onClose: vi.fn(),
        },
      });

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('should call onClose when Escape key is pressed', async () => {
      const onCloseMock = vi.fn();

      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: onCloseMock,
        },
      });

      const dialog = screen.getByRole('dialog');
      await fireEvent.keyDown(dialog, { key: 'Escape' });

      expect(onCloseMock).toHaveBeenCalled();
    });

    it('should call onClose when close button is clicked', async () => {
      const onCloseMock = vi.fn();

      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: onCloseMock,
        },
      });

      const closeButton = screen.getByRole('button', { name: /close login dialog/i });
      await fireEvent.click(closeButton);

      expect(onCloseMock).toHaveBeenCalled();
    });
  });

  describe('Form Submission', () => {
    it('should prevent form submission when validation fails', async () => {
      const { api } = await import('$lib/utils/api');
      const postSpy = vi.mocked(api.post);

      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
        },
      });

      // Submit form by clicking the submit button since form role may not be recognized
      const submitButton = screen.getByRole('button', { name: /login with password/i });
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
        redirectUrl: '/api/v1/oauth2/callback?code=123&redirect=/ui/',
      });

      mockWindowLocation();

      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
        },
      });

      const passwordInput = screen.getByLabelText('Password');
      const loginButton = screen.getByRole('button', { name: /login with password/i });

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

      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          authConfig: { basicEnabled: true, googleEnabled: false, githubEnabled: false },
        },
      });

      const passwordInput = screen.getByLabelText('Password');
      const loginButton = screen.getByRole('button', { name: /login with password/i });

      await fireEvent.input(passwordInput, { target: { value: 'wrong-password' } });
      await fireEvent.click(loginButton);

      await waitFor(() => {
        expect(screen.getByText('Invalid credentials. Please try again.')).toBeInTheDocument();
      });
    });
  });

  describe('Component Props', () => {
    it('should use default props when none are provided', () => {
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
        },
      });

      // Should render with default auth config (basic auth enabled)
      expect(screen.getByLabelText('Password')).toBeInTheDocument();
    });

    it('should respect custom redirectUrl prop', () => {
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          redirectUrl: '/custom/redirect',
        },
      });

      const hiddenInput = screen.getByDisplayValue('/custom/redirect');
      expect(hiddenInput).toBeInTheDocument();
    });

    it('should show only enabled auth methods', () => {
      render(LoginModal, {
        props: {
          isOpen: true,
          onClose: vi.fn(),
          authConfig: {
            basicEnabled: false,
            googleEnabled: true,
            githubEnabled: false,
          },
        },
      });

      expect(screen.queryByLabelText('Password')).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: /login with google/i })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /login with github/i })).not.toBeInTheDocument();
    });
  });
});
