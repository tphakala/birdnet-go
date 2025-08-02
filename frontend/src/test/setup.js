import '@testing-library/jest-dom';
import { vi } from 'vitest';

// Mock window.matchMedia
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation(query => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(), // deprecated
    removeListener: vi.fn(), // deprecated
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
});

// Mock IntersectionObserver
global.IntersectionObserver = vi.fn().mockImplementation(() => ({
  observe: vi.fn(),
  unobserve: vi.fn(),
  disconnect: vi.fn(),
}));

// Mock ResizeObserver
global.ResizeObserver = vi.fn().mockImplementation(() => ({
  observe: vi.fn(),
  unobserve: vi.fn(),
  disconnect: vi.fn(),
}));

// Mock fetch for i18n translation loading
global.fetch = vi.fn().mockImplementation(url => {
  // Mock translation files for i18n system
  if (url.includes('/ui/assets/messages/') && url.endsWith('.json')) {
    const mockTranslations = {
      common: {
        loading: 'Loading...',
        error: 'Error',
        save: 'Save',
        cancel: 'Cancel',
        yes: 'Yes',
        no: 'No',
      },
      forms: {
        labels: {
          showPassword: 'Show password',
          hidePassword: 'Hide password',
          copyToClipboard: 'Copy to clipboard',
          clearSelection: 'Clear selection',
          selectOption: 'Select an option',
        },
        password: {
          strength: {
            label: 'Password Strength:',
            levels: {
              weak: 'Weak',
              fair: 'Fair',
              good: 'Good',
              strong: 'Strong',
            },
            suggestions: {
              title: 'Suggestions:',
              minLength: 'At least 8 characters',
              mixedCase: 'Mix of uppercase and lowercase',
              number: 'At least one number',
              special: 'At least one special character',
            },
          },
        },
        validation: {
          required: 'This field is required',
          invalid: 'Invalid value',
          minLength: 'Must be at least {min} characters',
          maxLength: 'Must be no more than {max} characters',
          minValue: 'Must be at least {min}',
          maxValue: 'Must be no more than {max}',
          email: 'Invalid email address',
          url: 'Invalid URL',
        },
        placeholders: {
          text: 'Enter {field}',
          select: 'Select {field}',
          search: 'Search...',
          date: 'Select date',
        },
      },
    };

    return Promise.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve(mockTranslations),
      text: () => Promise.resolve(JSON.stringify(mockTranslations)),
      headers: new Headers(),
      statusText: 'OK',
      type: 'basic',
      url,
      redirected: false,
      body: null,
      bodyUsed: false,
      arrayBuffer: () => Promise.resolve(new ArrayBuffer(0)),
      blob: () => Promise.resolve(new Blob()),
      formData: () => Promise.resolve(new FormData()),
      clone: function () {
        return this;
      },
    });
  }

  // Default mock for other fetch requests
  return Promise.reject(new Error(`Unmocked fetch call to: ${url}`));
});

// Add any other global test setup here
