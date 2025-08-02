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

// Mock HTMLCanvasElement.getContext for axe-core accessibility tests
HTMLCanvasElement.prototype.getContext = vi.fn().mockImplementation(contextType => {
  if (contextType === '2d') {
    return {
      fillRect: vi.fn(),
      clearRect: vi.fn(),
      getImageData: vi.fn().mockReturnValue({ data: [] }),
      putImageData: vi.fn(),
      createImageData: vi.fn().mockReturnValue({ data: [] }),
      setTransform: vi.fn(),
      drawImage: vi.fn(),
      save: vi.fn(),
      fillText: vi.fn(),
      restore: vi.fn(),
      beginPath: vi.fn(),
      moveTo: vi.fn(),
      lineTo: vi.fn(),
      closePath: vi.fn(),
      stroke: vi.fn(),
      translate: vi.fn(),
      scale: vi.fn(),
      rotate: vi.fn(),
      arc: vi.fn(),
      fill: vi.fn(),
      measureText: vi.fn().mockReturnValue({ width: 0 }),
      transform: vi.fn(),
      rect: vi.fn(),
      clip: vi.fn(),
    };
  }
  return null;
});

// Default computed styles for testing - extracted for maintainability
const DEFAULT_COMPUTED_STYLES = {
  color: 'rgb(0, 0, 0)',
  backgroundColor: 'rgb(255, 255, 255)',
  fontSize: '16px',
  fontFamily: 'Arial',
  display: 'block',
  visibility: 'visible',
  opacity: '1',
  position: 'static',
  zIndex: 'auto',
  width: '100px',
  height: '100px',
  margin: '0px',
  padding: '0px',
  border: 'none',
  textAlign: 'left',
  textDecoration: 'none',
  textTransform: 'none',
  lineHeight: 'normal',
  fontWeight: 'normal',
  fontStyle: 'normal',
  textIndent: '0px',
  letterSpacing: 'normal',
  wordSpacing: 'normal',
  overflow: 'visible',
  cursor: 'auto',
  content: 'none',
  transform: 'none',
  filter: 'none',
  clip: 'auto',
  clipPath: 'none',
  maskImage: 'none',
  outline: 'none',
  boxShadow: 'none',
  borderRadius: '0px',
  background: 'none',
  backgroundImage: 'none',
  backgroundSize: 'auto',
  backgroundPosition: '0% 0%',
  backgroundRepeat: 'repeat',
  backgroundAttachment: 'scroll',
  backgroundClip: 'border-box',
  backgroundOrigin: 'padding-box',
  top: 'auto',
  right: 'auto',
  bottom: 'auto',
  left: 'auto',
  float: 'none',
  clear: 'none',
  verticalAlign: 'baseline',
  whiteSpace: 'normal',
  wordWrap: 'normal',
  direction: 'ltr',
  unicodeBidi: 'normal',
  writingMode: 'horizontal-tb',
  textOrientation: 'mixed',
  minWidth: '0px',
  minHeight: '0px',
  maxWidth: 'none',
  maxHeight: 'none',
  listStyle: 'none',
  listStyleType: 'disc',
  listStylePosition: 'outside',
  listStyleImage: 'none',
  borderCollapse: 'separate',
  borderSpacing: '0px',
  captionSide: 'top',
  emptyCells: 'show',
  tableLayout: 'auto',
  flexDirection: 'row',
  flexWrap: 'nowrap',
  justifyContent: 'flex-start',
  alignItems: 'stretch',
  alignContent: 'stretch',
  order: '0',
  flexGrow: '0',
  flexShrink: '1',
  flexBasis: 'auto',
  alignSelf: 'auto',
  gridTemplateColumns: 'none',
  gridTemplateRows: 'none',
  gridTemplateAreas: 'none',
  gridColumnStart: 'auto',
  gridColumnEnd: 'auto',
  gridRowStart: 'auto',
  gridRowEnd: 'auto',
  gridColumn: 'auto',
  gridRow: 'auto',
  gridArea: 'auto',
  justifyItems: 'legacy',
  justifySelf: 'auto',
  placeSelf: 'auto',
  placeItems: 'auto',
  placeContent: 'normal',
  gap: 'normal',
  columnGap: 'normal',
  rowGap: 'normal',
  animation: 'none',
  animationName: 'none',
  animationDuration: '0s',
  animationTimingFunction: 'ease',
  animationDelay: '0s',
  animationIterationCount: '1',
  animationDirection: 'normal',
  animationFillMode: 'none',
  animationPlayState: 'running',
  transition: 'none',
  transitionProperty: 'all',
  transitionDuration: '0s',
  transitionTimingFunction: 'ease',
  transitionDelay: '0s',
  pointerEvents: 'auto',
  userSelect: 'auto',
  resize: 'none',
  appearance: 'none',
  webkitAppearance: 'none',
  mozAppearance: 'none',
  msAppearance: 'none',
  columns: 'auto',
  columnCount: 'auto',
  columnWidth: 'auto',
  columnRule: 'none',
  columnRuleColor: 'currentcolor',
  columnRuleStyle: 'none',
  columnRuleWidth: 'medium',
  columnSpan: 'none',
  columnFill: 'balance',
  breakBefore: 'auto',
  breakAfter: 'auto',
  breakInside: 'auto',
  pageBreakBefore: 'auto',
  pageBreakAfter: 'auto',
  pageBreakInside: 'auto',
  orphans: '2',
  widows: '2',
  quotes: 'none',
  counterReset: 'none',
  counterIncrement: 'none',
  speak: 'normal',
  speakHeader: 'once',
  speakNumeral: 'continuous',
  speakPunctuation: 'none',
  speechRate: 'medium',
  voiceFamily: 'inherit',
  pitch: 'medium',
  pitchRange: '50',
  stress: '50',
  richness: '50',
  azimuth: 'center',
  elevation: 'level',
  volume: 'medium',
  pauseBefore: 'none',
  pauseAfter: 'none',
  pause: 'none',
  cuesBefore: 'none',
  cuesAfter: 'none',
  cues: 'none',
  playDuring: 'auto',
  marker: 'none',
  markerStart: 'none',
  markerMid: 'none',
  markerEnd: 'none',
  size: 'auto',
  marks: 'none',
  page: 'auto',
};

// Mock window.getComputedStyle for axe-core accessibility tests
window.getComputedStyle = vi.fn().mockImplementation(() => {
  // Create a shallow copy of the default styles
  const style = { ...DEFAULT_COMPUTED_STYLES };

  // Add getPropertyValue method that returns the property from the style object
  // SECURITY NOTE: This mock implementation uses bracket notation with the 'property' parameter
  // which triggers object injection warnings. This is intentional and safe because:
  // 1. This code only runs in the test environment, never in production
  // 2. The 'property' parameter comes from test code or testing libraries (like axe-core)
  // 3. The style object is a mock with predefined properties, not user data
  // 4. No sensitive operations or data access occurs - it's purely for CSS property mocking
  style.getPropertyValue = vi.fn().mockImplementation(property => {
    return (
      // eslint-disable-next-line security/detect-object-injection -- Intentional: mock for test environment only
      style[property] ||
      style[property.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase())] ||
      ''
    );
  });

  return style;
});

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
        ui: {
          loading: 'Loading...',
          noData: 'No data available',
          error: 'Error',
        },
        buttons: {
          save: 'Save Changes',
          reset: 'Reset',
          delete: 'Delete',
          cancel: 'Cancel',
          apply: 'Apply',
          confirm: 'Confirm',
          edit: 'Edit',
          close: 'Close',
          back: 'Back',
          next: 'Next',
          previous: 'Previous',
          yes: 'Yes',
          no: 'No',
          ok: 'OK',
          clear: 'Clear',
        },
        aria: {
          closeModal: 'Close modal',
          closeNotification: 'Close notification',
          openMenu: 'Open menu',
          closeMenu: 'Close menu',
        },
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
        dateRange: {
          labels: {
            startDate: 'Start Date',
            endDate: 'End Date',
            quickSelect: 'Quick Select',
            selected: 'Selected date range: {startDate} to {endDate}',
          },
          presets: {
            today: 'Today',
            yesterday: 'Yesterday',
            last7Days: 'Last 7 days',
            last30Days: 'Last 30 days',
            thisMonth: 'This month',
            lastMonth: 'Last month',
            thisYear: 'This year',
          },
        },
      },
      dataDisplay: {
        table: {
          noData: 'No data available',
          sortBy: 'Sort by {column}',
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
