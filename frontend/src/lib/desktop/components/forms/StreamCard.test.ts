import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/svelte';
import StreamCardTestWrapper from './StreamCardTestWrapper.svelte';
import type { StreamConfig } from '$lib/stores/settings';

describe('StreamCard', () => {
  const createStream = (overrides: Partial<StreamConfig> = {}): StreamConfig => ({
    name: 'Test Stream',
    url: 'rtsp://user:password@example.local/stream',
    type: 'rtsp',
    transport: 'tcp',
    ...overrides,
  });

  it('shows disabled status and unchecked enabled toggle for disabled streams', () => {
    render(StreamCardTestWrapper, {
      props: {
        stream: createStream({ enabled: false }),
        status: 'disabled',
        onUpdate: vi.fn(() => true),
        onDelete: vi.fn(),
      },
    });

    expect(screen.getByText('Disabled')).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Enabled' })).not.toBeChecked();
  });

  it('treats streams without an explicit enabled flag as enabled', () => {
    render(StreamCardTestWrapper, {
      props: {
        stream: createStream(),
        status: 'unknown',
        onUpdate: vi.fn(() => true),
        onDelete: vi.fn(),
      },
    });

    expect(screen.getByRole('checkbox', { name: 'Enabled' })).toBeChecked();
  });

  it('emits an updated stream config when the enabled checkbox is toggled', async () => {
    const onUpdate = vi.fn(() => true);
    const stream = createStream({ enabled: false });

    render(StreamCardTestWrapper, {
      props: {
        stream,
        status: 'disabled',
        onUpdate,
        onDelete: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('checkbox', { name: 'Enabled' }));

    expect(onUpdate).toHaveBeenCalledWith({
      ...stream,
      enabled: true,
    });
  });
});
