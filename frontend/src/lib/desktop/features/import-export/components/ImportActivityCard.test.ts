import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/svelte';

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

// Mock api before importing the component.
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
    constructor(message: string, status: number, response?: Response) {
      super(message);
      this.name = 'ApiError';
      this.status = status;
      this.userMessage = message;
      this.isNetworkError = false;
      this.response = response ?? new Response(null, { status });
    }
  },
}));

// Mock ReconnectingEventSource with controllable event dispatching.
type EventHandler = (event: Event) => void;
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

import ImportActivityCard from './ImportActivityCard.svelte';
import { api } from '$lib/utils/api';
import type { ImportProgress, ImportStatusResponse } from '../types';

const idleStatus: ImportStatusResponse = { running: false, status: 'idle' };

const runningProgress: ImportProgress = {
  total: 100,
  processed: 40,
  inserted: 30,
  skipped: 10,
  errors: 0,
  phase: 'import',
};

const runningStatus: ImportStatusResponse = {
  running: true,
  job_id: 'job-1',
  status: 'running',
  progress: runningProgress,
};

function renderCard(onOpenWizard = vi.fn()) {
  return { onOpenWizard, ...render(ImportActivityCard, { props: { onOpenWizard } }) };
}

describe('ImportActivityCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockEsListeners = new Map();
    mockEsInstance = null;
  });

  afterEach(() => {
    cleanup();
  });

  it('shows the empty state when no import has run', async () => {
    vi.mocked(api.get).mockResolvedValue(idleStatus);
    renderCard();
    await waitFor(() => {
      expect(screen.getByText('system.importExport.activity.emptyTitle')).toBeInTheDocument();
    });
    expect(MockReconnectingEventSource).not.toHaveBeenCalled();
  });

  it('shows progress and subscribes to the SSE stream while running', async () => {
    vi.mocked(api.get).mockResolvedValue(runningStatus);
    renderCard();
    await waitFor(() => {
      expect(screen.getByText(/system.importExport.progress.runningLabel/)).toBeInTheDocument();
    });
    expect(screen.getByText('40')).toBeInTheDocument(); // processed
    expect(screen.getByText('30')).toBeInTheDocument(); // inserted
    expect(MockReconnectingEventSource).toHaveBeenCalledWith('/api/v2/import/jobs/job-1/progress');
  });

  it('open wizard button invokes the callback while running', async () => {
    vi.mocked(api.get).mockResolvedValue(runningStatus);
    const { onOpenWizard } = renderCard();
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /system.importExport.activity.openWizard/ })
      ).toBeInTheDocument();
    });
    await fireEvent.click(
      screen.getByRole('button', { name: /system.importExport.activity.openWizard/ })
    );
    expect(onOpenWizard).toHaveBeenCalledOnce();
  });

  it('updates counts from SSE progress events', async () => {
    vi.mocked(api.get).mockResolvedValue(runningStatus);
    renderCard();
    await waitFor(() => {
      expect(screen.getByText('40')).toBeInTheDocument();
    });
    dispatchMockEvent('progress', { ...runningProgress, processed: 75, inserted: 60 });
    await waitFor(() => {
      expect(screen.getByText('75')).toBeInTheDocument();
      expect(screen.getByText('60')).toBeInTheDocument();
    });
  });

  it('shows the success summary and detections link after the complete event', async () => {
    vi.mocked(api.get).mockResolvedValue(runningStatus);
    renderCard();
    await waitFor(() => {
      expect(MockReconnectingEventSource).toHaveBeenCalled();
    });
    dispatchMockEvent('complete', {
      ...runningProgress,
      processed: 100,
      inserted: 90,
      phase: 'done',
    });
    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.successTitle')).toBeInTheDocument();
    });
    expect(
      screen.getByRole('link', { name: /system.importExport.done.viewDetectionsLink/ })
    ).toBeInTheDocument();
    expect(mockEsInstance?.close).toHaveBeenCalled();
  });

  it('shows the cancelled summary after the cancelled event', async () => {
    vi.mocked(api.get).mockResolvedValue(runningStatus);
    renderCard();
    await waitFor(() => {
      expect(MockReconnectingEventSource).toHaveBeenCalled();
    });
    dispatchMockEvent('cancelled', runningProgress);
    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.cancelledTitle')).toBeInTheDocument();
    });
  });

  it('shows the error summary after a terminal error event', async () => {
    vi.mocked(api.get).mockResolvedValue(runningStatus);
    renderCard();
    await waitFor(() => {
      expect(MockReconnectingEventSource).toHaveBeenCalled();
    });
    dispatchMockEvent('error', { message: 'boom', ...runningProgress });
    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.errorTitle')).toBeInTheDocument();
    });
  });

  it('shows a finished import summary when loading after completion', async () => {
    vi.mocked(api.get).mockResolvedValue({
      running: false,
      job_id: 'job-1',
      status: 'done',
      progress: { ...runningProgress, processed: 100, inserted: 90, phase: 'done' },
    } satisfies ImportStatusResponse);
    renderCard();
    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.successTitle')).toBeInTheDocument();
    });
    expect(MockReconnectingEventSource).not.toHaveBeenCalled();
  });

  it('shows the failed import summary when status reports an error', async () => {
    vi.mocked(api.get).mockResolvedValue({
      running: false,
      job_id: 'job-1',
      status: 'done',
      progress: runningProgress,
      error: 'import failed',
    } satisfies ImportStatusResponse);
    renderCard();
    await waitFor(() => {
      expect(screen.getByText('system.importExport.done.errorTitle')).toBeInTheDocument();
    });
  });

  it('shows a load error when the status request fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('network down'));
    renderCard();
    await waitFor(() => {
      expect(screen.getByText('system.importExport.errors.loadFailed')).toBeInTheDocument();
    });
  });

  it('refetches status when refreshSignal changes', async () => {
    vi.mocked(api.get).mockResolvedValue(idleStatus);
    const onOpenWizard = vi.fn();
    const { rerender } = render(ImportActivityCard, {
      props: { onOpenWizard, refreshSignal: 0 },
    });
    await waitFor(() => {
      expect(api.get).toHaveBeenCalledTimes(1);
    });
    vi.mocked(api.get).mockResolvedValue(runningStatus);
    await rerender({ onOpenWizard, refreshSignal: 1 });
    await waitFor(() => {
      expect(api.get).toHaveBeenCalledTimes(2);
      expect(MockReconnectingEventSource).toHaveBeenCalledWith(
        '/api/v2/import/jobs/job-1/progress'
      );
    });
  });
});
