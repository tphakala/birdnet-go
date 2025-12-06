import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import RTSPUrlManager from './RTSPUrlManager.svelte';

const defaultUrls = [
  { id: '1', url: 'rtsp://cam1.example.com/stream', name: 'Camera 1', active: true },
  { id: '2', url: 'rtsp://cam2.example.com/stream', name: 'Camera 2', active: false },
];

describe('RTSPUrlManager', () => {
  it('renders with basic props', () => {
    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: [],
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByText('RTSP Streams')).toBeInTheDocument();
    expect(screen.getByText('Add New RTSP Stream')).toBeInTheDocument();
  });

  it('shows empty state when no URLs configured', () => {
    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: [],
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByText('No RTSP streams configured')).toBeInTheDocument();
  });

  it('displays existing RTSP streams', () => {
    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: defaultUrls,
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByDisplayValue('Camera 1')).toBeInTheDocument();
    expect(screen.getByDisplayValue('Camera 2')).toBeInTheDocument();
    expect(screen.getByDisplayValue('rtsp://cam1.example.com/stream')).toBeInTheDocument();
    expect(screen.getByDisplayValue('rtsp://cam2.example.com/stream')).toBeInTheDocument();
  });

  it('shows stream count and status badges', () => {
    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: defaultUrls,
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByText('Configured RTSP Streams (2/5):')).toBeInTheDocument();
    expect(screen.getByText('Active')).toBeInTheDocument();
    expect(screen.getByText('Inactive')).toBeInTheDocument();
  });

  it('adds new RTSP stream when form is filled and Add button is clicked', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: [],
        onUpdate,
      },
    });

    const nameInput = screen.getByLabelText(/Stream Name.*\*/);
    const urlInput = screen.getByLabelText(/RTSP URL.*\*/);
    const addButton = screen.getByRole('button', { name: 'Add RTSP URL' });

    await fireEvent.input(nameInput, { target: { value: 'New Camera' } });
    await fireEvent.input(urlInput, { target: { value: 'rtsp://new.example.com/stream' } });
    await fireEvent.click(addButton);

    expect(onUpdate).toHaveBeenCalledWith([
      expect.objectContaining({
        url: 'rtsp://new.example.com/stream',
        name: 'New Camera',
        active: true,
      }),
    ]);
  });

  it('validates RTSP URL format', async () => {
    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: [],
        onUpdate: vi.fn(),
      },
    });

    const nameInput = screen.getByLabelText(/Stream Name.*\*/);
    const urlInput = screen.getByLabelText(/RTSP URL.*\*/);

    await fireEvent.input(nameInput, { target: { value: 'Invalid Camera' } });
    await fireEvent.input(urlInput, { target: { value: 'http://invalid.example.com' } });

    expect(screen.getByText('URL must start with rtsp://')).toBeInTheDocument();
  });

  it('validates stream name', async () => {
    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: [],
        onUpdate: vi.fn(),
      },
    });

    const nameInput = screen.getByLabelText(/Stream Name.*\*/);

    await fireEvent.input(nameInput, { target: { value: '' } });
    await fireEvent.blur(nameInput);

    // Name validation appears on input change
    await fireEvent.input(nameInput, { target: { value: 'a'.repeat(51) } });

    expect(screen.getByText('Name must be less than 50 characters')).toBeInTheDocument();
  });

  it('prevents adding duplicate URLs', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: defaultUrls,
        onUpdate,
      },
    });

    const nameInput = screen.getByLabelText(/Stream Name.*\*/);
    const urlInput = screen.getByLabelText(/RTSP URL.*\*/);
    const addButton = screen.getByRole('button', { name: 'Add RTSP URL' });

    await fireEvent.input(nameInput, { target: { value: 'Duplicate Camera' } });
    await fireEvent.input(urlInput, { target: { value: 'rtsp://cam1.example.com/stream' } });
    await fireEvent.click(addButton);

    // Should not call onUpdate for duplicate URL
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it('removes RTSP stream when remove button is clicked', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: defaultUrls,
        onUpdate,
      },
    });

    const removeButtons = screen.getAllByLabelText('Remove RTSP stream');
    await fireEvent.click(removeButtons[0]);

    expect(onUpdate).toHaveBeenCalledWith([
      { id: '2', url: 'rtsp://cam2.example.com/stream', name: 'Camera 2', active: false },
    ]);
  });

  it('updates stream name when modified', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: defaultUrls,
        onUpdate,
      },
    });

    const nameInputs = screen.getAllByLabelText(/Stream Name/);
    await fireEvent.input(nameInputs[1], { target: { value: 'Updated Camera 1' } });

    expect(onUpdate).toHaveBeenCalledWith([
      { id: '1', url: 'rtsp://cam1.example.com/stream', name: 'Updated Camera 1', active: true },
      { id: '2', url: 'rtsp://cam2.example.com/stream', name: 'Camera 2', active: false },
    ]);
  });

  it('updates stream URL when modified', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: defaultUrls,
        onUpdate,
      },
    });

    const urlInputs = screen.getAllByLabelText(/RTSP URL/);
    await fireEvent.input(urlInputs[1], { target: { value: 'rtsp://updated.example.com/stream' } });

    expect(onUpdate).toHaveBeenCalledWith([
      { id: '1', url: 'rtsp://updated.example.com/stream', name: 'Camera 1', active: true },
      { id: '2', url: 'rtsp://cam2.example.com/stream', name: 'Camera 2', active: false },
    ]);
  });

  it('toggles stream active state', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: defaultUrls,
        onUpdate,
      },
    });

    const toggles = screen.getAllByRole('checkbox');
    const activeToggle = toggles.find(toggle => (toggle as HTMLInputElement).checked);

    // Fail fast if toggle not found - also helps TypeScript narrow the type
    if (!activeToggle) throw new Error('Active toggle not found');
    await fireEvent.click(activeToggle);

    expect(onUpdate).toHaveBeenCalledWith([
      { id: '1', url: 'rtsp://cam1.example.com/stream', name: 'Camera 1', active: false },
      { id: '2', url: 'rtsp://cam2.example.com/stream', name: 'Camera 2', active: false },
    ]);
  });

  it('disables Add button when inputs are invalid', async () => {
    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: [],
        onUpdate: vi.fn(),
      },
    });

    const addButton = screen.getByRole('button', { name: 'Add RTSP URL' });
    expect(addButton).toBeDisabled();

    // Only name filled
    const nameInput = screen.getByLabelText(/Stream Name.*\*/);
    await fireEvent.input(nameInput, { target: { value: 'Camera' } });
    expect(addButton).toBeDisabled();

    // Invalid URL
    const urlInput = screen.getByLabelText(/RTSP URL.*\*/);
    await fireEvent.input(urlInput, { target: { value: 'invalid-url' } });
    expect(addButton).toBeDisabled();
  });

  it('enables Add button when both inputs are valid', async () => {
    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: [],
        onUpdate: vi.fn(),
      },
    });

    const nameInput = screen.getByLabelText(/Stream Name.*\*/);
    const urlInput = screen.getByLabelText(/RTSP URL.*\*/);
    const addButton = screen.getByRole('button', { name: 'Add RTSP URL' });

    await fireEvent.input(nameInput, { target: { value: 'Valid Camera' } });
    await fireEvent.input(urlInput, { target: { value: 'rtsp://valid.example.com/stream' } });

    expect(addButton).not.toBeDisabled();
  });

  it('shows maximum items warning when limit reached', () => {
    const maxUrls = Array.from({ length: 5 }, (_, i) => ({
      id: `${i + 1}`,
      url: `rtsp://cam${i + 1}.example.com/stream`,
      name: `Camera ${i + 1}`,
      active: true,
    }));

    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: maxUrls,
        onUpdate: vi.fn(),
        maxItems: 5,
      },
    });

    expect(screen.getByText('Maximum number of RTSP streams (5) reached.')).toBeInTheDocument();
  });

  it('disables all inputs when disabled prop is true', () => {
    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
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

  it('shows help text when provided', () => {
    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: [],
        onUpdate: vi.fn(),
        helpText: 'Configure your RTSP camera streams',
      },
    });

    expect(screen.getByText('Configure your RTSP camera streams')).toBeInTheDocument();
  });

  it('clears form after successful addition', async () => {
    const onUpdate = vi.fn();

    render(RTSPUrlManager, {
      props: {
        label: 'RTSP Streams',
        urls: [],
        onUpdate,
      },
    });

    const nameInput = screen.getByLabelText(/Stream Name.*\*/) as HTMLInputElement;
    const urlInput = screen.getByLabelText(/RTSP URL.*\*/) as HTMLInputElement;
    const addButton = screen.getByRole('button', { name: 'Add RTSP URL' });

    await fireEvent.input(nameInput, { target: { value: 'Test Camera' } });
    await fireEvent.input(urlInput, { target: { value: 'rtsp://test.example.com/stream' } });
    await fireEvent.click(addButton);

    expect(nameInput.value).toBe('');
    expect(urlInput.value).toBe('');
  });
});
