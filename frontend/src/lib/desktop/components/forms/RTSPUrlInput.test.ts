import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import RTSPUrlInput from './RTSPUrlInput.svelte';

const defaultUrls = ['rtsp://example.com/stream1', 'rtsp://example.com/stream2'];

describe('RTSPUrlInput', () => {
  it('renders with empty urls array', () => {
    render(RTSPUrlInput, {
      props: {
        urls: [],
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByPlaceholderText(/Enter RTSP URL/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add' })).toBeInTheDocument();
  });

  it('displays existing URLs', () => {
    render(RTSPUrlInput, {
      props: {
        urls: defaultUrls,
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByDisplayValue('rtsp://example.com/stream1')).toBeInTheDocument();
    expect(screen.getByDisplayValue('rtsp://example.com/stream2')).toBeInTheDocument();
  });

  it('shows Remove buttons for existing URLs', () => {
    render(RTSPUrlInput, {
      props: {
        urls: defaultUrls,
        onUpdate: vi.fn(),
      },
    });

    const removeButtons = screen.getAllByText('Remove');
    expect(removeButtons).toHaveLength(2);
  });

  it('adds new URL when Add button is clicked', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlInput, {
      props: {
        urls: [],
        onUpdate,
      },
    });

    const input = screen.getByPlaceholderText(/Enter RTSP URL/);
    const addButton = screen.getByRole('button', { name: 'Add' });

    await fireEvent.input(input, { target: { value: 'rtsp://new.example.com/stream' } });
    await fireEvent.click(addButton);

    expect(onUpdate).toHaveBeenCalledWith(['rtsp://new.example.com/stream']);
  });

  it('adds new URL when Enter key is pressed', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlInput, {
      props: {
        urls: [],
        onUpdate,
      },
    });

    const input = screen.getByPlaceholderText(/Enter RTSP URL/);

    await fireEvent.input(input, { target: { value: 'rtsp://new.example.com/stream' } });
    await fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });

    expect(onUpdate).toHaveBeenCalledWith(['rtsp://new.example.com/stream']);
  });

  it('clears input after adding URL', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlInput, {
      props: {
        urls: [],
        onUpdate,
      },
    });

    const input = screen.getByPlaceholderText(/Enter RTSP URL/) as HTMLInputElement;
    const addButton = screen.getByRole('button', { name: 'Add' });

    await fireEvent.input(input, { target: { value: 'rtsp://new.example.com/stream' } });
    await fireEvent.click(addButton);

    expect(input.value).toBe('');
  });

  it('removes URL when Remove button is clicked', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlInput, {
      props: {
        urls: defaultUrls,
        onUpdate,
      },
    });

    const removeButtons = screen.getAllByText('Remove');
    await fireEvent.click(removeButtons[0]);

    expect(onUpdate).toHaveBeenCalledWith(['rtsp://example.com/stream2']);
  });

  it('updates existing URL when modified', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlInput, {
      props: {
        urls: defaultUrls,
        onUpdate,
      },
    });

    const urlInput = screen.getByDisplayValue('rtsp://example.com/stream1');
    await fireEvent.input(urlInput, { target: { value: 'rtsp://modified.example.com/stream1' } });

    expect(onUpdate).toHaveBeenCalledWith([
      'rtsp://modified.example.com/stream1',
      'rtsp://example.com/stream2',
    ]);
  });

  it('disables Add button when input is empty', () => {
    render(RTSPUrlInput, {
      props: {
        urls: [],
        onUpdate: vi.fn(),
      },
    });

    const addButton = screen.getByRole('button', { name: 'Add' });
    expect(addButton).toBeDisabled();
  });

  it('enables Add button when input has value', async () => {
    render(RTSPUrlInput, {
      props: {
        urls: [],
        onUpdate: vi.fn(),
      },
    });

    const input = screen.getByPlaceholderText(/Enter RTSP URL/);
    const addButton = screen.getByRole('button', { name: 'Add' });

    await fireEvent.input(input, { target: { value: 'rtsp://example.com/stream' } });

    expect(addButton).not.toBeDisabled();
  });

  it('disables all inputs when disabled prop is true', () => {
    render(RTSPUrlInput, {
      props: {
        urls: defaultUrls,
        onUpdate: vi.fn(),
        disabled: true,
      },
    });

    const inputs = screen.getAllByRole('textbox');
    inputs.forEach(input => {
      expect(input).toBeDisabled();
    });

    const buttons = screen.getAllByRole('button');
    buttons.forEach(button => {
      expect(button).toBeDisabled();
    });
  });

  it('trims whitespace from new URL input', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlInput, {
      props: {
        urls: [],
        onUpdate,
      },
    });

    const input = screen.getByPlaceholderText(/Enter RTSP URL/);
    const addButton = screen.getByRole('button', { name: 'Add' });

    await fireEvent.input(input, { target: { value: '  rtsp://example.com/stream  ' } });
    await fireEvent.click(addButton);

    expect(onUpdate).toHaveBeenCalledWith(['rtsp://example.com/stream']);
  });

  it('does not add empty URLs', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlInput, {
      props: {
        urls: [],
        onUpdate,
      },
    });

    const input = screen.getByPlaceholderText(/Enter RTSP URL/);
    const addButton = screen.getByRole('button', { name: 'Add' });

    await fireEvent.input(input, { target: { value: '   ' } });
    await fireEvent.click(addButton);

    expect(onUpdate).not.toHaveBeenCalled();
  });

  it('handles multiple URLs correctly', () => {
    const manyUrls = [
      'rtsp://cam1.example.com/stream',
      'rtsp://cam2.example.com/stream',
      'rtsp://cam3.example.com/stream',
    ];

    render(RTSPUrlInput, {
      props: {
        urls: manyUrls,
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByDisplayValue('rtsp://cam1.example.com/stream')).toBeInTheDocument();
    expect(screen.getByDisplayValue('rtsp://cam2.example.com/stream')).toBeInTheDocument();
    expect(screen.getByDisplayValue('rtsp://cam3.example.com/stream')).toBeInTheDocument();

    const removeButtons = screen.getAllByText('Remove');
    expect(removeButtons).toHaveLength(3);
  });
});
