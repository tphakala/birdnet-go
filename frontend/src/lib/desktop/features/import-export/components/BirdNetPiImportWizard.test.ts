import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/svelte';

// Mock logger so tests can verify no password is leaked to log calls.
vi.mock('$lib/utils/logger', () => ({
  loggers: {
    ui: {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
  },
}));

// Mock api before importing component
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    userMessage: string;
    isNetworkError: boolean;
    response: Response;
    constructor(message: string, status: number) {
      super(message);
      this.name = 'ApiError';
      this.status = status;
      this.userMessage = message;
      this.isNetworkError = false;
      this.response = new Response(null, { status });
    }
  },
}));

// Mock ReconnectingEventSource with controllable event dispatching.
// Use vi.hoisted() so the variable is available inside the hoisted vi.mock factory.
type EventHandler = (event: Event) => void;
// Use Map to avoid object-injection-sink linting warnings and ensure proper undefined checks.
let mockEsListeners = new Map<string, EventHandler[]>();
let mockEsInstance: {
  addEventListener: ReturnType<typeof vi.fn>;
  close: ReturnType<typeof vi.fn>;
} | null = null;

const { MockReconnectingEventSource } = vi.hoisted(() => {
  const MockReconnectingEventSource = vi.fn();
  return { MockReconnectingEventSource };
});

vi.mock('$lib/utils/ReconnectingEventSource', () => {
  // Must use a regular function (not arrow) so 'new MockReconnectingEventSource()' works.
  MockReconnectingEventSource.mockImplementation(function MockReconnectingEventSourceImpl(
    this: unknown
  ) {
    mockEsListeners = new Map<string, EventHandler[]>();
    mockEsInstance = {
      addEventListener: vi.fn((type: string, handler: EventHandler) => {
        const existing = mockEsListeners.get(type);
        if (existing !== undefined) {
          existing.push(handler);
        } else {
          mockEsListeners.set(type, [handler]);
        }
      }),
      close: vi.fn(),
    };
    return mockEsInstance;
  });
  return { ReconnectingEventSource: MockReconnectingEventSource };
});

/** Helper to dispatch a mock SSE event with JSON data. */
function dispatchMockEvent(type: string, data: unknown) {
  const handlers = mockEsListeners.get(type) ?? [];
  const event = new MessageEvent(type, { data: JSON.stringify(data) });
  for (const handler of handlers) {
    handler(event);
  }
}

// Import component after mocks are set up
import BirdNetPiImportWizard from './BirdNetPiImportWizard.svelte';
import { api } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';
import { flushSync } from 'svelte';

// Default sources response: one readable candidate in a container environment.
const defaultSourcesResponse = {
  environment: 'Docker',
  containerized: true,
  run_as_user: 'birdnet',
  run_as_uid: 1000,
  candidates: [
    {
      path: '/external/birdnet-pi/birds.db',
      kind: 'local' as const,
      detection_count: 100,
      latest_date: '2026-06-20',
      audio_dir_guess: '',
      size: 1000,
      valid: true,
      reason: '',
      owner_uid: 1000,
      owner_name: 'birdnet',
    },
  ],
  guidance: null,
};

const defaultProgress = {
  total: 1000,
  processed: 500,
  inserted: 490,
  skipped: 10,
  errors: 0,
  phase: 'import' as const,
};

function makeMockEsImplementation() {
  return function MockReconnectingEventSourceImpl(this: unknown) {
    mockEsListeners = new Map<string, EventHandler[]>();
    mockEsInstance = {
      addEventListener: vi.fn((type: string, handler: EventHandler) => {
        const existing = mockEsListeners.get(type);
        if (existing !== undefined) {
          existing.push(handler);
        } else {
          mockEsListeners.set(type, [handler]);
        }
      }),
      close: vi.fn(),
    };
    return mockEsInstance;
  };
}

function setupDefaultMocks() {
  // Re-apply the ReconnectingEventSource mock implementation.
  // Must use a regular function (not arrow) so 'new' calls work without Vitest warning.
  MockReconnectingEventSource.mockImplementation(makeMockEsImplementation());

  vi.mocked(api.get).mockImplementation((url: string) => {
    if (url === '/api/v2/import/status') {
      return Promise.resolve({ running: false, status: 'idle' });
    }
    if (url === '/api/v2/import/sources') {
      return Promise.resolve(defaultSourcesResponse);
    }
    return Promise.reject(new Error(`Unmocked URL: ${url}`));
  });
  vi.mocked(api.post).mockResolvedValue({ job_id: 'test-job-123', status: 'started' });
}

/**
 * Click the "Select" button on the first candidate card to advance from the
 * source step to the mode step. Waits for the button to appear first.
 */
async function navigatePastSource() {
  const selectBtn = await screen.findByRole('button', {
    name: /system.importExport.source.selectButton/,
  });
  await fireEvent.click(selectBtn);
}

describe('BirdNetPiImportWizard', () => {
  const onClose = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    mockEsListeners = new Map<string, EventHandler[]>();
    mockEsInstance = null;
    setupDefaultMocks();
  });

  afterEach(() => {
    cleanup();
  });

  // ---- Rendering ----

  it('renders the wizard modal', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });
    await waitFor(() => {
      expect(screen.getByText('system.importExport.birdnetPi.wizardTitle')).toBeInTheDocument();
    });
  });

  it('shows loading spinner on initial load', () => {
    render(BirdNetPiImportWizard, { props: { onClose } });
    // During loading the spinner should be present
    expect(screen.getByRole('status')).toBeInTheDocument();
  });

  it('shows step indicator with 5 steps', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });
    await waitFor(() => {
      // Step indicator: aria-current="step" on first step
      expect(screen.getByRole('group')).toBeInTheDocument();
    });
  });

  // ---- Source step: candidate-driven ----

  it('shows candidate cards when sources are found', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });
    // The default mock returns one candidate; the Select button should appear.
    expect(
      await screen.findByRole('button', { name: /system.importExport.source.selectButton/ })
    ).toBeInTheDocument();
    // The candidate path should be shown
    expect(screen.getByText('/external/birdnet-pi/birds.db')).toBeInTheDocument();
  });

  it('shows zero-candidates title when no sources found', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url === '/api/v2/import/status') {
        return Promise.resolve({ running: false, status: 'idle' });
      }
      if (url === '/api/v2/import/sources') {
        return Promise.resolve({ ...defaultSourcesResponse, candidates: [], guidance: null });
      }
      return Promise.reject(new Error(`Unmocked: ${url}`));
    });
    render(BirdNetPiImportWizard, { props: { onClose } });
    expect(await screen.findByText('system.importExport.source.zeroTitle')).toBeInTheDocument();
  });

  it('shows guidance steps in zero-candidates state', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url === '/api/v2/import/status') {
        return Promise.resolve({ running: false, status: 'idle' });
      }
      if (url === '/api/v2/import/sources') {
        return Promise.resolve({
          ...defaultSourcesResponse,
          candidates: [],
          guidance: {
            environment: 'docker',
            steps: ['docker run --mount type=bind,src=/data,dst=/external ...'],
          },
        });
      }
      return Promise.reject(new Error(`Unmocked: ${url}`));
    });
    render(BirdNetPiImportWizard, { props: { onClose } });
    await waitFor(() => {
      expect(
        screen.getByText('docker run --mount type=bind,src=/data,dst=/external ...')
      ).toBeInTheDocument();
    });
  });

  it('shows manual entry form when link is clicked', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });
    // Wait for candidate cards to appear, then click the manual entry link
    await screen.findByRole('button', { name: /system.importExport.source.selectButton/ });
    const manualLink = screen.getByText('system.importExport.source.manualEntryLink');
    await fireEvent.click(manualLink);
    expect(
      screen.getByLabelText(/system.importExport.source.manualEntryLabel/)
    ).toBeInTheDocument();
  });

  // ---- Mode step ----

  it('mode step shows both db-only and db-audio radios enabled', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });
    // Navigate to mode step by selecting a candidate
    await navigatePastSource();

    await waitFor(() => {
      expect(screen.getByText('system.importExport.mode.label')).toBeInTheDocument();
    });

    // db-only radio should be enabled and selected by default
    const dbOnlyRadio = screen.getByRole('radio', {
      name: /system.importExport.mode.dbOnly.label/,
    });
    expect(dbOnlyRadio).not.toBeDisabled();
    // db-only must be the default selection so a regression in the default mode is caught.
    expect(dbOnlyRadio).toBeChecked();

    // db-audio radio should also be enabled
    const dbAudioRadio = screen.getByRole('radio', {
      name: /system.importExport.mode.dbAudio.label/,
    });
    expect(dbAudioRadio).not.toBeDisabled();
  });

  it('db-audio disabled reason is not shown', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });
    await navigatePastSource();

    await waitFor(() => {
      expect(screen.getByText('system.importExport.mode.label')).toBeInTheDocument();
    });

    // The disabled reason must no longer appear since db-audio is now enabled
    expect(
      screen.queryByText('system.importExport.mode.dbAudio.disabledReason')
    ).not.toBeInTheDocument();
  });

  // ---- Confirm step ----

  it('confirm step shows the chosen source path and mode', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });

    // Step 1: source -> mode
    await navigatePastSource();
    await waitFor(() => {
      expect(screen.getByText('system.importExport.mode.label')).toBeInTheDocument();
    });

    // Step 2: mode -> confirm
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => {
      expect(screen.getByText('system.importExport.confirm.description')).toBeInTheDocument();
    });

    // Should show the candidate path that was selected
    expect(screen.getByText('/external/birdnet-pi/birds.db')).toBeInTheDocument();
    // Should show mode
    expect(screen.getByText('system.importExport.mode.dbOnly.label')).toBeInTheDocument();
  });

  // ---- Start import ----

  it('start import posts correct body to the API', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });

    // Navigate to confirm
    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));

    // Click start import
    const startButton = screen.getByRole('button', {
      name: /system.importExport.confirm.startButton/,
    });
    await fireEvent.click(startButton);

    await waitFor(() => {
      expect(api.post).toHaveBeenCalledWith('/api/v2/import/birdnet-pi', {
        mode: 'db-only',
        source_path: '/external/birdnet-pi/birds.db',
      });
    });
  });

  it('selecting db-audio posts mode db-audio to the API', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });

    // Navigate to the mode step.
    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));

    // Select the db-audio mode.
    const dbAudioRadio = screen.getByRole('radio', {
      name: /system.importExport.mode.dbAudio.label/,
    });
    await fireEvent.click(dbAudioRadio);

    // Advance to confirm and start.
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.confirm.startButton/ })
    );

    await waitFor(() => {
      expect(api.post).toHaveBeenCalledWith('/api/v2/import/birdnet-pi', {
        mode: 'db-audio',
        source_path: '/external/birdnet-pi/birds.db',
      });
    });
  });

  it('shows already-running message on 409 response', async () => {
    const { ApiError } = await import('$lib/utils/api');
    vi.mocked(api.post).mockRejectedValue(
      new ApiError('already running', 409, new Response(null, { status: 409 }))
    );

    render(BirdNetPiImportWizard, { props: { onClose } });

    // Navigate to confirm
    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));

    const startButton = screen.getByRole('button', {
      name: /system.importExport.confirm.startButton/,
    });
    await fireEvent.click(startButton);

    await waitFor(() => {
      expect(screen.getByText('system.importExport.errors.alreadyRunning')).toBeInTheDocument();
    });
  });

  it('shows validation error message on 400 response', async () => {
    const { ApiError } = await import('$lib/utils/api');
    vi.mocked(api.post).mockRejectedValue(
      new ApiError('Invalid source path: file not found', 400, new Response(null, { status: 400 }))
    );

    render(BirdNetPiImportWizard, { props: { onClose } });

    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));

    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.confirm.startButton/ })
    );

    await waitFor(() => {
      expect(screen.getByText('Invalid source path: file not found')).toBeInTheDocument();
    });
  });

  // ---- SSE progress ----

  it('progress events update the progress bar and counters', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });

    // Navigate to confirm and start
    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.confirm.startButton/ })
    );

    await waitFor(() => {
      expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    });

    // Dispatch a progress event
    flushSync(() => {
      dispatchMockEvent('progress', defaultProgress);
    });

    await waitFor(() => {
      // The inserted count (490) should be visible
      expect(screen.getByText('490')).toBeInTheDocument();
    });
    // Progress bar aria-valuenow should reflect the percent (500/1000 = 50)
    const progressbar = screen.getByRole('progressbar');
    expect(progressbar).toHaveAttribute('aria-valuenow', '50');
  });

  it('complete event transitions to done step with success state', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });

    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.confirm.startButton/ })
    );

    await waitFor(() => {
      expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    });

    flushSync(() => {
      dispatchMockEvent('complete', {
        ...defaultProgress,
        processed: 1000,
        inserted: 990,
        skipped: 10,
        errors: 0,
        phase: 'done',
      });
    });

    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.successTitle')).toBeInTheDocument();
    });
    // Summary counts should be visible
    expect(screen.getByText('990')).toBeInTheDocument(); // inserted
    expect(screen.getByText('10')).toBeInTheDocument(); // skipped
    // View detections link must be present
    const link = screen.getByRole('link', { name: /system.importExport.done.viewDetectionsLink/ });
    expect(link).toHaveAttribute('href', expect.stringContaining('source=birdnet-pi'));
  });

  it('error event transitions to done step with error state', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });

    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.confirm.startButton/ })
    );

    await waitFor(() => {
      expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    });

    flushSync(() => {
      dispatchMockEvent('error', {
        message: 'import failed unexpectedly',
        ...defaultProgress,
      });
    });

    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.errorTitle')).toBeInTheDocument();
    });
  });

  it('native error event (no data) does not terminate the import', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });

    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.confirm.startButton/ })
    );

    await waitFor(() => {
      expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    });

    // Dispatch a native transport error (plain Event, no data) - must NOT terminate
    flushSync(() => {
      const nativeError = new Event('error');
      const handlers = mockEsListeners.get('error') ?? [];
      for (const handler of handlers) {
        handler(nativeError);
      }
    });

    // Wizard should still be on the progress step
    expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    // The EventSource should NOT have been closed
    expect(mockEsInstance?.close).not.toHaveBeenCalled();
    // The done/error UI must NOT be shown
    expect(screen.queryByText('system.importExport.done.errorTitle')).not.toBeInTheDocument();
  });

  it('server error event with JSON data transitions to done with localized error', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });

    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.confirm.startButton/ })
    );

    await waitFor(() => {
      expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    });

    // Dispatch a server error event with valid JSON data - MUST terminate
    flushSync(() => {
      dispatchMockEvent('error', {
        message: 'server-side import error',
        ...defaultProgress,
      });
    });

    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.errorTitle')).toBeInTheDocument();
    });
    // The localized error message should be shown (not the raw backend string)
    expect(screen.getByText('system.importExport.errors.importFailed')).toBeInTheDocument();
    // The SSE source must have been closed on server error
    expect(mockEsInstance?.close).toHaveBeenCalledOnce();
  });

  it('cancelled event transitions to done step with cancelled state', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });

    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.confirm.startButton/ })
    );

    await waitFor(() => {
      expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    });

    flushSync(() => {
      dispatchMockEvent('cancelled', { ...defaultProgress, processed: 300 });
    });

    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.cancelledTitle')).toBeInTheDocument();
    });
  });

  // ---- Cancel ----

  it('cancel button posts to the cancel endpoint', async () => {
    vi.mocked(api.post).mockImplementation((url: string) => {
      if (url === '/api/v2/import/birdnet-pi') {
        return Promise.resolve({ job_id: 'test-job-123', status: 'started' });
      }
      if (url === '/api/v2/import/jobs/test-job-123/cancel') {
        return Promise.resolve({ status: 'cancelling' });
      }
      return Promise.reject(new Error(`Unmocked POST: ${url}`));
    });

    render(BirdNetPiImportWizard, { props: { onClose } });

    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.confirm.startButton/ })
    );

    await waitFor(() => {
      expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    });

    const cancelButton = screen.getByRole('button', {
      name: /system.importExport.progress.cancelButton/,
    });
    await fireEvent.click(cancelButton);

    await waitFor(() => {
      expect(api.post).toHaveBeenCalledWith('/api/v2/import/jobs/test-job-123/cancel');
    });

    // Simulate the SSE cancelled event that the server sends after cancel
    flushSync(() => {
      dispatchMockEvent('cancelled', { ...defaultProgress, processed: 300 });
    });

    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.cancelledTitle')).toBeInTheDocument();
    });
  });

  it('run in background calls onClose without posting to cancel endpoint', async () => {
    render(BirdNetPiImportWizard, { props: { onClose } });

    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.confirm.startButton/ })
    );

    await waitFor(() => {
      expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    });

    const runInBgButton = screen.getByRole('button', {
      name: /system.importExport.runInBackground/,
    });
    await fireEvent.click(runInBgButton);

    expect(onClose).toHaveBeenCalledOnce();
    expect(api.post).not.toHaveBeenCalledWith(expect.stringContaining('/cancel'));
  });

  // ---- Resume on load ----

  it('resumes in-progress import from status check on mount', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url === '/api/v2/import/status') {
        return Promise.resolve({
          running: true,
          job_id: 'resume-job-456',
          status: 'running',
          progress: defaultProgress,
        });
      }
      return Promise.reject(new Error(`Unmocked: ${url}`));
    });

    render(BirdNetPiImportWizard, { props: { onClose } });

    await waitFor(() => {
      expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    });

    // Should have connected the event source for the running job
    expect(MockReconnectingEventSource).toHaveBeenCalledWith(
      '/api/v2/import/jobs/resume-job-456/progress'
    );
  });

  // ---- CR-1: rehydrate finished import on reopen ----

  it('shows done step with success state when status is done and no error on mount', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url === '/api/v2/import/status') {
        return Promise.resolve({
          running: false,
          status: 'done',
          progress: {
            ...defaultProgress,
            processed: 1000,
            inserted: 990,
            skipped: 10,
            errors: 0,
            phase: 'done' as const,
          },
          error: undefined,
        });
      }
      return Promise.reject(new Error(`Unmocked: ${url}`));
    });

    render(BirdNetPiImportWizard, { props: { onClose } });

    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.successTitle')).toBeInTheDocument();
    });
    // Progress counters should be visible
    expect(screen.getByText('990')).toBeInTheDocument();
    // Should NOT have connected an event source (no running job)
    expect(MockReconnectingEventSource).not.toHaveBeenCalled();
    // Should NOT fall through to source discovery
    expect(
      screen.queryByText('system.importExport.source.candidatesIntro')
    ).not.toBeInTheDocument();
  });

  it('shows done step with error state when status is done with error on mount', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url === '/api/v2/import/status') {
        return Promise.resolve({
          running: false,
          status: 'done',
          progress: defaultProgress,
          error: 'database error',
        });
      }
      return Promise.reject(new Error(`Unmocked: ${url}`));
    });

    render(BirdNetPiImportWizard, { props: { onClose } });

    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.errorTitle')).toBeInTheDocument();
    });
    expect(screen.getByText('system.importExport.errors.importFailed')).toBeInTheDocument();
    // Should NOT show success
    expect(screen.queryByText('system.importExport.done.successTitle')).not.toBeInTheDocument();
  });

  it('Import another resets to source step from done step', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url === '/api/v2/import/status') {
        return Promise.resolve({
          running: false,
          status: 'done',
          progress: defaultProgress,
          error: undefined,
        });
      }
      if (url === '/api/v2/import/sources') {
        return Promise.resolve(defaultSourcesResponse);
      }
      return Promise.reject(new Error(`Unmocked: ${url}`));
    });

    render(BirdNetPiImportWizard, { props: { onClose } });

    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.successTitle')).toBeInTheDocument();
    });

    const importAnotherButton = screen.getByRole('button', {
      name: /system.importExport.done.importAnother/,
    });
    await fireEvent.click(importAnotherButton);

    // After reset, should show candidate cards
    expect(
      await screen.findByRole('button', { name: /system.importExport.source.selectButton/ })
    ).toBeInTheDocument();
    // Done step should no longer be visible
    expect(screen.queryByText('system.importExport.done.successTitle')).not.toBeInTheDocument();
  });

  // ---- CR-2: rehydrate true outcome on terminal cancel response ----

  it('cancel returns {status: done} for a completed job shows SUCCESS state (not cancelled)', async () => {
    const completedProgress = {
      ...defaultProgress,
      processed: 1000,
      inserted: 950,
      skipped: 50,
      errors: 0,
      phase: 'done' as const,
    };

    vi.mocked(api.post).mockImplementation((url: string) => {
      if (url === '/api/v2/import/birdnet-pi') {
        return Promise.resolve({ job_id: 'test-job-123', status: 'started' });
      }
      if (url === '/api/v2/import/jobs/test-job-123/cancel') {
        return Promise.resolve({ status: 'done' });
      }
      return Promise.reject(new Error(`Unmocked POST: ${url}`));
    });

    // Initial mount: /status returns idle so wizard starts at source step.
    // After cancel: /status returns done with completed progress.
    let statusCallCount = 0;
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url === '/api/v2/import/status') {
        statusCallCount++;
        if (statusCallCount === 1) {
          return Promise.resolve({ running: false, status: 'idle' });
        }
        return Promise.resolve({
          running: false,
          status: 'done',
          progress: completedProgress,
          error: undefined,
        });
      }
      if (url === '/api/v2/import/sources') {
        return Promise.resolve(defaultSourcesResponse);
      }
      return Promise.reject(new Error(`Unmocked GET: ${url}`));
    });

    render(BirdNetPiImportWizard, { props: { onClose } });

    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.confirm.startButton/ })
    );

    await waitFor(() => {
      expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    });

    const cancelButton = screen.getByRole('button', {
      name: /system.importExport.progress.cancelButton/,
    });
    await fireEvent.click(cancelButton);

    // Should show SUCCESS state because /status returned a completed job (no error)
    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.successTitle')).toBeInTheDocument();
    });
    // Must NOT show cancelled
    expect(screen.queryByText('system.importExport.done.cancelledTitle')).not.toBeInTheDocument();
    // The EventSource should have been closed
    expect(mockEsInstance?.close).toHaveBeenCalled();
  });

  it('cancel returns {status: done} after SSE already set cancelled done state does not fetch /status', async () => {
    // The cancel POST is delayed so the SSE cancelled event fires first,
    // moving the wizard to currentStep='done'. When the cancel POST then
    // resolves with {status:'done'}, the guard (currentStep !== 'progress')
    // must prevent a /status fetch that would overwrite the cancelled state.
    let resolveCancelPost!: (v: { status: string }) => void;
    const cancelPostPromise = new Promise<{ status: string }>(resolve => {
      resolveCancelPost = resolve;
    });

    vi.mocked(api.post).mockImplementation((url: string) => {
      if (url === '/api/v2/import/birdnet-pi') {
        return Promise.resolve({ job_id: 'test-job-123', status: 'started' });
      }
      if (url === '/api/v2/import/jobs/test-job-123/cancel') {
        return cancelPostPromise;
      }
      return Promise.reject(new Error(`Unmocked POST: ${url}`));
    });

    const statusGetCalls: string[] = [];
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url === '/api/v2/import/status') {
        statusGetCalls.push(url);
        return Promise.resolve({ running: false, status: 'idle' });
      }
      if (url === '/api/v2/import/sources') {
        return Promise.resolve(defaultSourcesResponse);
      }
      return Promise.reject(new Error(`Unmocked GET: ${url}`));
    });

    render(BirdNetPiImportWizard, { props: { onClose } });

    await navigatePastSource();
    await waitFor(() => screen.getByText('system.importExport.mode.label'));
    await fireEvent.click(screen.getByRole('button', { name: /common.buttons.next/ }));
    await waitFor(() => screen.getByText('system.importExport.confirm.description'));
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.confirm.startButton/ })
    );

    await waitFor(() => {
      expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    });

    // Click cancel - POST is pending (delayed)
    const cancelButton = screen.getByRole('button', {
      name: /system.importExport.progress.cancelButton/,
    });
    await fireEvent.click(cancelButton);

    // SSE delivers cancelled event BEFORE the cancel POST resolves
    flushSync(() => {
      dispatchMockEvent('cancelled', { ...defaultProgress, processed: 300 });
    });

    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.cancelledTitle')).toBeInTheDocument();
    });

    // Clear the initial mount /status call count
    statusGetCalls.length = 0;

    // Now resolve the cancel POST with {status:'done'} - wizard is already at 'done'
    flushSync(() => {
      resolveCancelPost({ status: 'done' });
    });

    // Give async handlers time to run
    await waitFor(() => {
      // Wizard should remain at cancelled done state
      expect(screen.getByText('system.importExport.done.cancelledTitle')).toBeInTheDocument();
    });

    // /status must NOT have been fetched (currentStep was already 'done')
    expect(statusGetCalls).toHaveLength(0);
    // Must NOT show success
    expect(screen.queryByText('system.importExport.done.successTitle')).not.toBeInTheDocument();
  });

  // ---- Task 9: candidate-driven source step ----

  it('renders candidate cards when sources are found', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({ running: false, status: 'idle' });
    vi.mocked(api.get).mockResolvedValueOnce({
      environment: 'Bare Metal',
      containerized: false,
      run_as_user: 'birdnet',
      run_as_uid: 1000,
      candidates: [
        {
          path: '/home/pi/BirdNET-Pi/birds.db',
          kind: 'local',
          detection_count: 42,
          latest_date: '2026-06-20',
          audio_dir_guess: '',
          size: 1000,
          valid: true,
          reason: '',
          owner_uid: 1000,
          owner_name: 'pi',
        },
      ],
      guidance: null,
    });

    render(BirdNetPiImportWizard, { props: { onClose } });
    expect(
      await screen.findByRole('button', { name: /system.importExport.source.selectButton/ })
    ).toBeInTheDocument();
  });

  it('renders guidance and Check again when zero candidates', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({ running: false, status: 'idle' });
    vi.mocked(api.get).mockResolvedValueOnce({
      environment: 'Bare Metal',
      containerized: false,
      run_as_user: 'birdnet',
      run_as_uid: 1000,
      candidates: [],
      guidance: { environment: 'Bare Metal', steps: ['lsblk'] },
    });

    render(BirdNetPiImportWizard, { props: { onClose } });
    expect(
      await screen.findByRole('button', { name: /system.importExport.source.checkAgainButton/ })
    ).toBeInTheDocument();
  });

  // ---- Task 10: elevation panel ----

  const unreadableSourcesResponse = {
    environment: 'Bare Metal',
    containerized: false,
    run_as_user: 'birdnet',
    run_as_uid: 1000,
    candidates: [
      {
        path: '/home/pi/BirdNET-Pi/birds.db',
        kind: 'local' as const,
        detection_count: 0,
        latest_date: '',
        audio_dir_guess: '',
        size: 1,
        valid: false,
        reason: 'permission_denied',
        owner_uid: 1001,
        owner_name: 'pi',
      },
    ],
    guidance: null,
  };

  it('shows the HTTP password warning for an unreadable source over plain HTTP', async () => {
    // jsdom defaults to 'about:blank' which has protocol 'about:', not 'http:'.
    // Override to simulate a plain-HTTP deployment.
    Object.defineProperty(globalThis, 'location', {
      value: { protocol: 'http:' },
      writable: true,
      configurable: true,
    });

    vi.mocked(api.get).mockResolvedValueOnce({ running: false, status: 'idle' });
    vi.mocked(api.get).mockResolvedValueOnce(unreadableSourcesResponse);
    // First elevate attempt (no password) fails so the password panel is revealed.
    vi.mocked(api.post).mockRejectedValueOnce(new Error('sudo unavailable'));

    render(BirdNetPiImportWizard, { props: { onClose } });
    await fireEvent.click(
      await screen.findByRole('button', { name: /system.importExport.source.useThisButton/ })
    );
    expect(
      await screen.findByText(/system.importExport.source.elevation.httpWarning/)
    ).toBeInTheDocument();
  });

  it('never logs the typed sudo password', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({ running: false, status: 'idle' });
    vi.mocked(api.get).mockResolvedValueOnce(unreadableSourcesResponse);
    // First elevate (no password) fails -> password panel appears.
    vi.mocked(api.post).mockRejectedValueOnce(new Error('sudo unavailable'));
    // Second elevate (with password) returns a fallback response.
    vi.mocked(api.post).mockResolvedValueOnce({
      method: 'fallback',
      job_id: '',
      status: '',
      fallback_commands: ['setfacl -m u:birdnet:r /home/pi/BirdNET-Pi/birds.db'],
    });

    render(BirdNetPiImportWizard, { props: { onClose } });
    // Click "Use this" to start elevation without a password.
    await fireEvent.click(
      await screen.findByRole('button', { name: /system.importExport.source.useThisButton/ })
    );
    // Wait for the password panel.
    const passwordInput = await screen.findByLabelText(
      /system.importExport.source.elevation.passwordLabel/
    );
    // Type the secret password into the input.
    await fireEvent.input(passwordInput, { target: { value: 'hunter2' } });
    // Submit with the password.
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.source.elevation.submitButton/ })
    );
    // Wait for the fallback commands to appear.
    await waitFor(() => {
      expect(
        screen.getByText('setfacl -m u:birdnet:r /home/pi/BirdNET-Pi/birds.db')
      ).toBeInTheDocument();
    });
    // The secret password must never appear in any logger call.
    const logged =
      vi.mocked(loggers.ui.error).mock.calls.flat().join(' ') +
      vi.mocked(loggers.ui.debug).mock.calls.flat().join(' ') +
      vi.mocked(loggers.ui.info).mock.calls.flat().join(' ') +
      vi.mocked(loggers.ui.warn).mock.calls.flat().join(' ');
    expect(logged).not.toContain('hunter2');
  });

  it('elevate response with job_id advances to the progress step', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({ running: false, status: 'idle' });
    vi.mocked(api.get).mockResolvedValueOnce(unreadableSourcesResponse);
    // First elevate (no password) immediately succeeds via passwordless sudo.
    vi.mocked(api.post).mockResolvedValueOnce({
      method: 'sudo',
      job_id: 'elev-job-123',
      status: 'started',
      fallback_commands: [],
    });

    render(BirdNetPiImportWizard, { props: { onClose } });
    await fireEvent.click(
      await screen.findByRole('button', { name: /system.importExport.source.useThisButton/ })
    );
    await waitFor(() => {
      expect(screen.getByText('system.importExport.progress.runningLabel')).toBeInTheDocument();
    });
  });
});
