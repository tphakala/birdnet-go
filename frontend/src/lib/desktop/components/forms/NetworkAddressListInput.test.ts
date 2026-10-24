import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, within, waitFor } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { t } from '$lib/i18n';
import NetworkAddressListInput from './NetworkAddressListInput.svelte';

// The shared t() mock returns the bare key, which hides parameters. Show them here so the
// tests can check that a message names the offending values.
const originalT = vi.mocked(t).getMockImplementation();

function renderList(props: Partial<Parameters<typeof renderInput>[0]> = {}) {
  return renderInput({
    label: 'Addresses',
    values: [],
    onUpdate: vi.fn(),
    accept: 'ip-or-cidr',
    ...props,
  });
}

function renderInput(props: {
  label: string;
  values: string[];
  onUpdate: (values: string[]) => void;
  accept: 'cidr' | 'ip' | 'ip-or-cidr';
  disabled?: boolean;
  maxItems?: number;
  emptyStateMessage?: string;
  draft?: string;
  reportResetKey?: number;
  className?: string;
  rowAnnotation?: ReturnType<typeof annotationSnippet>;
}) {
  return render(NetworkAddressListInput, { props });
}

function annotationSnippet() {
  return createRawSnippet<[{ value: string; index: number; valid: boolean }]>(row => ({
    render: () =>
      `<span data-testid="annotation">${row().index}:${row().value}:${String(row().valid)}</span>`,
  }));
}

type ControlledProps = Partial<Parameters<typeof renderInput>[0]>;

/**
 * Renders the list with a parent that applies every update, as the real parents do
 * (they write the new list back through the settings store before the next render).
 */
function renderControlled(props: ControlledProps = {}) {
  let apply: (next: { values: string[] }) => Promise<void> = async () => {};
  const onUpdate = vi.fn((values: string[]) => void apply({ values }));
  const view = renderList({ ...props, onUpdate });
  apply = view.rerender;
  return { ...view, onUpdate };
}

function input(): HTMLInputElement {
  return screen.getByRole<HTMLInputElement>('textbox', { name: 'Addresses' });
}

function addButton(): HTMLElement {
  return screen.getByRole('button', { name: /forms\.networkAddressList\.add/ });
}

function alertText(): string {
  return screen.getByRole('alert').textContent;
}

async function typeAndSubmit(value: string, via: 'enter' | 'button' = 'enter') {
  await fireEvent.input(input(), { target: { value } });
  if (via === 'enter') {
    await fireEvent.keyDown(input(), { key: 'Enter' });
  } else {
    await fireEvent.click(addButton());
  }
}

describe('NetworkAddressListInput', () => {
  beforeEach(() => {
    vi.mocked(t).mockImplementation((key: string, params?: Record<string, unknown>) =>
      params ? `${key} ${JSON.stringify(params)}` : key
    );
  });

  afterEach(() => {
    vi.mocked(t).mockReset();
    if (originalT) vi.mocked(t).mockImplementation(originalT);
  });

  it('adds a value with the Add button and clears the input', async () => {
    const onUpdate = vi.fn();
    renderList({ values: ['10.0.0.1'], onUpdate });

    await typeAndSubmit('  172.18.0.1 ', 'button');

    expect(onUpdate).toHaveBeenCalledWith(['10.0.0.1', '172.18.0.1']);
    expect(input().value).toBe('');
  });

  it('adds a value with Enter', async () => {
    const onUpdate = vi.fn();
    renderList({ onUpdate });

    await typeAndSubmit('192.168.1.0/24');

    expect(onUpdate).toHaveBeenCalledWith(['192.168.1.0/24']);
  });

  it('keeps Add enabled for any non-empty text, even an invalid one', async () => {
    renderList();
    expect(addButton()).toBeDisabled();

    await fireEvent.input(input(), { target: { value: 'not-an-address' } });

    expect(addButton()).not.toBeDisabled();
  });

  it('turns a multi-line paste into comma-separated text in the input', async () => {
    renderList();
    const field = input();
    field.focus();

    await fireEvent.paste(field, {
      clipboardData: { getData: () => '10.0.0.1, 10.0.0.2\nbad 10.0.0.1' },
    });

    expect(field.value).toBe('10.0.0.1, 10.0.0.2, bad 10.0.0.1');
  });

  it('adds the valid tokens of a batch and reports invalid ones before duplicates', async () => {
    const { onUpdate } = renderControlled();
    await fireEvent.paste(input(), {
      clipboardData: { getData: () => '10.0.0.1, 10.0.0.2\nbad 10.0.0.1' },
    });

    await fireEvent.keyDown(input(), { key: 'Enter' });

    expect(onUpdate).toHaveBeenCalledTimes(1);
    expect(onUpdate).toHaveBeenCalledWith(['10.0.0.1', '10.0.0.2']);
    expect(input().value).toBe('bad');
    const message = alertText();
    expect(message).toContain('forms.networkAddressList.errors.invalid.ipOrCidr');
    expect(message).toContain('bad');
    expect(message).toContain('forms.networkAddressList.errors.duplicate');
    expect(message).toContain('10.0.0.1');
    expect(message.indexOf('errors.invalid')).toBeLessThan(message.indexOf('errors.duplicate'));
    expect(input()).toHaveAttribute('aria-invalid', 'true');
  });

  it('clears the message when the input changes', async () => {
    renderList();
    await typeAndSubmit('bad');
    expect(alertText()).toContain('errors.invalid');

    await fireEvent.input(input(), { target: { value: 'bad2' } });

    expect(alertText()).toBe('');
    expect(input()).not.toHaveAttribute('aria-invalid', 'true');
  });

  it.each([
    { accept: 'ip' as const, value: '2001:DB8::1' },
    { accept: 'cidr' as const, value: '2001:db8::/32' },
    { accept: 'ip-or-cidr' as const, value: 'fe80::1' },
    { accept: 'ip-or-cidr' as const, value: '2001:db8::/48' },
  ])('accepts the IPv6 value $value in $accept mode', async ({ accept, value }) => {
    const onUpdate = vi.fn();
    renderList({ accept, onUpdate });

    await typeAndSubmit(value);

    expect(onUpdate).toHaveBeenCalledWith([value]);
  });

  it('rejects a bare IP in cidr mode with the cidr message', async () => {
    const onUpdate = vi.fn();
    renderList({ accept: 'cidr', onUpdate });

    await typeAndSubmit('192.168.1.10');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(input().value).toBe('192.168.1.10');
    expect(alertText()).toContain('forms.networkAddressList.errors.invalid.cidr');
  });

  it('rejects a range in ip mode with the ip message', async () => {
    const onUpdate = vi.fn();
    renderList({ accept: 'ip', onUpdate });

    await typeAndSubmit('10.0.0.0/8');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(alertText()).toContain('forms.networkAddressList.errors.invalid.ip');
  });

  it('detects duplicates through the normalized key and drops them from the input', async () => {
    const onUpdate = vi.fn();
    renderList({ values: ['192.168.1.0/24', '10.0.0.1'], onUpdate });

    await typeAndSubmit('192.168.1.5/24, 10.0.0.1/32');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(input().value).toBe('');
    expect(alertText()).toContain('forms.networkAddressList.errors.duplicate');
    expect(alertText()).toContain('192.168.1.5/24');
  });

  it('adds the valid form of an invalid row instead of reporting a duplicate', async () => {
    const onUpdate = vi.fn();
    renderList({ accept: 'cidr', values: ['10.0.0.1'], onUpdate });

    await typeAndSubmit('10.0.0.1/32');

    expect(onUpdate).toHaveBeenCalledWith(['10.0.0.1', '10.0.0.1/32']);
    expect(alertText()).not.toContain('errors.duplicate');
  });

  it('removes a row and names the address in the remove button', async () => {
    const onUpdate = vi.fn();
    renderList({ values: ['10.0.0.1', '10.0.0.2'], onUpdate });

    const remove = screen.getByRole('button', { name: /10\.0\.0\.2/ });
    await fireEvent.click(remove);

    expect(onUpdate).toHaveBeenCalledWith(['10.0.0.1']);
  });

  it('moves focus to the new last row when the last row is removed', async () => {
    let rerenderList: (props: { values: string[] }) => Promise<void> = async () => {};
    const { rerender } = renderList({
      values: ['10.0.0.1', '10.0.0.2'],
      onUpdate: values => void rerenderList({ values }),
    });
    rerenderList = rerender;

    const last = screen.getByRole('button', { name: /10\.0\.0\.2/ });
    last.focus();
    await fireEvent.click(last);

    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole('button', { name: /10\.0\.0\.1/ }))
    );
  });

  it('moves focus to the input when the only row is removed', async () => {
    let rerenderList: (props: { values: string[] }) => Promise<void> = async () => {};
    const { rerender } = renderList({
      values: ['10.0.0.1'],
      onUpdate: values => void rerenderList({ values }),
    });
    rerenderList = rerender;

    await fireEvent.click(screen.getByRole('button', { name: /10\.0\.0\.1/ }));

    await waitFor(() => expect(document.activeElement).toBe(input()));
  });

  it('returns focus to the input after adding with the button', async () => {
    renderList();

    await typeAndSubmit('10.0.0.1', 'button');

    expect(document.activeElement).toBe(input());
  });

  it('shows the empty state message when there are no values', () => {
    renderList({ emptyStateMessage: 'Nothing here yet' });

    expect(screen.getByText('Nothing here yet')).toBeInTheDocument();
    expect(screen.queryByRole('list')).not.toBeInTheDocument();
  });

  it('blocks adding at maxItems and keeps over-limit tokens in the input', async () => {
    const { rerender, onUpdate } = renderControlled({ values: ['10.0.0.1'], maxItems: 2 });

    await typeAndSubmit('10.0.0.2, 10.0.0.3');

    expect(onUpdate).toHaveBeenCalledWith(['10.0.0.1', '10.0.0.2']);
    expect(input().value).toBe('10.0.0.3');
    expect(alertText()).toContain('forms.networkAddressList.errors.limit');

    await rerender({ values: ['10.0.0.1', '10.0.0.2'] });

    expect(addButton()).toBeDisabled();
    expect(screen.getByText(/forms\.networkAddressList\.limitReached/)).toBeInTheDocument();
  });

  it('disables the input, Add and every remove button when disabled', async () => {
    renderList({ values: ['10.0.0.1'], disabled: true });
    await fireEvent.input(input(), { target: { value: '10.0.0.2' } });

    expect(input()).toBeDisabled();
    expect(addButton()).toBeDisabled();
    expect(screen.getByRole('button', { name: /10\.0\.0\.1/ })).toBeDisabled();
  });

  it('flags invalid and duplicated values it is given and never removes them', () => {
    const onUpdate = vi.fn();
    renderList({ values: ['10.0.0.1', 'nonsense', '10.0.0.1', '10.0.0.1/32'], onUpdate });

    const rows = within(screen.getByRole('list')).getAllByRole('listitem');
    expect(rows).toHaveLength(4);
    expect(within(rows[0]).queryByText(/rowDuplicate|rowInvalid/)).toBeNull();
    expect(
      within(rows[1]).getByText(/forms\.networkAddressList\.rowInvalid\.ipOrCidr/)
    ).toBeInTheDocument();
    expect(
      within(rows[2]).getByText(/forms\.networkAddressList\.rowDuplicate/)
    ).toBeInTheDocument();
    expect(
      within(rows[3]).getByText(/forms\.networkAddressList\.rowDuplicate/)
    ).toBeInTheDocument();
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it('renders the rowAnnotation snippet for every row with its validity', () => {
    renderList({ values: ['10.0.0.0/8', 'nonsense'], rowAnnotation: annotationSnippet() });

    const annotations = screen.getAllByTestId('annotation').map(el => el.textContent);
    expect(annotations).toEqual(['0:10.0.0.0/8:true', '1:nonsense:false']);
  });

  it('shows the pending hint while text is typed but not added', async () => {
    renderList();

    await fireEvent.input(input(), { target: { value: '10.0.0.1' } });
    expect(screen.getByText('forms.networkAddressList.pendingHint')).toBeInTheDocument();

    await fireEvent.keyDown(input(), { key: 'Enter' });
    expect(screen.queryByText('forms.networkAddressList.pendingHint')).not.toBeInTheDocument();
  });

  it('starts from a draft passed in, so a remounted field keeps the typed text', () => {
    const first = renderList({ draft: '10.0.0.7' });
    expect(input().value).toBe('10.0.0.7');
    first.unmount();

    renderList({ draft: '10.0.0.7' });

    expect(input().value).toBe('10.0.0.7');
    expect(screen.getByText('forms.networkAddressList.pendingHint')).toBeInTheDocument();
  });

  it('shows the ip-mode message and row flag for ranges in ip mode', async () => {
    renderList({ accept: 'ip', values: ['10.0.0.0/8'] });

    await typeAndSubmit('192.168.0.0/16');

    expect(alertText()).toContain('forms.networkAddressList.errors.invalid.ip');
    expect(alertText()).toContain('192.168.0.0/16');
    expect(screen.getByText('forms.networkAddressList.rowInvalid.ip')).toBeInTheDocument();
  });

  it('keeps every token in the input and Add disabled once the list is full', async () => {
    const onUpdate = vi.fn();
    renderList({ values: ['10.0.0.1', '10.0.0.2'], maxItems: 2, onUpdate });

    await fireEvent.input(input(), { target: { value: '10.0.0.3' } });

    expect(addButton()).toBeDisabled();
    await fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onUpdate).not.toHaveBeenCalled();
    expect(input().value).toBe('10.0.0.3');
    expect(input()).toHaveAttribute(
      'aria-describedby',
      expect.stringContaining(screen.getByText(/forms\.networkAddressList\.limitReached/).id)
    );
  });

  it('merges className onto the root element', () => {
    const { container } = renderList({ className: 'custom-root' });
    expect(container.querySelector('.form-control.custom-root')).not.toBeNull();
  });

  it('keeps pending text but blocks adding when disabled while text is pending', async () => {
    const onUpdate = vi.fn();
    const { rerender } = renderList({ onUpdate });
    await fireEvent.input(input(), { target: { value: '10.0.0.1' } });

    await rerender({ disabled: true });

    expect(input().value).toBe('10.0.0.1');
    expect(addButton()).toBeDisabled();
    await fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onUpdate).not.toHaveBeenCalled();
  });

  describe('add report visibility', () => {
    it('hides the invalid-token report when reportResetKey changes', async () => {
      const { rerender } = renderControlled({ reportResetKey: 0 });
      await typeAndSubmit('abc');
      expect(alertText()).toContain('errors.invalid');
      expect(input()).toHaveAttribute('aria-invalid', 'true');
      expect(input()).toHaveClass('input-error');

      await rerender({ draft: '', reportResetKey: 1 });

      expect(alertText()).toBe('');
      expect(input()).not.toHaveAttribute('aria-invalid', 'true');
      expect(input()).not.toHaveClass('input-error');
    });

    it('hides a duplicate-only report when reportResetKey changes with the draft and list unchanged', async () => {
      const { rerender } = renderControlled({ values: ['10.0.0.1'], reportResetKey: 0 });
      await typeAndSubmit('10.0.0.1');
      expect(input().value).toBe('');
      expect(alertText()).toContain('errors.duplicate');

      await rerender({ values: ['10.0.0.1'], draft: '', reportResetKey: 1 });

      expect(alertText()).toBe('');
    });

    it('keeps the duplicate-only report after its own add empties the draft', async () => {
      renderControlled({ values: ['10.0.0.1'] });

      await typeAndSubmit('10.0.0.1');

      expect(input().value).toBe('');
      expect(alertText()).toContain('errors.duplicate');
    });

    it('keeps the report while the parent applies the add', async () => {
      renderControlled();

      await typeAndSubmit('10.0.0.2, abc');

      expect(alertText()).toContain('errors.invalid');
      expect(input()).toHaveAttribute('aria-invalid', 'true');
    });

    it('keeps the report when reportResetKey is set but does not change', async () => {
      const { rerender } = renderControlled({ values: ['10.0.0.1'], reportResetKey: 3 });
      await typeAndSubmit('10.0.0.1');

      await rerender({ values: ['10.0.0.1'], draft: '', reportResetKey: 3 });

      expect(alertText()).toContain('errors.duplicate');
    });

    it('hides the report after removing a row', async () => {
      renderControlled({ values: ['10.0.0.1'] });
      await typeAndSubmit('10.0.0.2, abc');
      expect(alertText()).toContain('errors.invalid');

      await fireEvent.click(screen.getByRole('button', { name: /10\.0\.0\.2/ }));

      expect(alertText()).toBe('');
      expect(input().value).toBe('abc');
      expect(input()).not.toHaveAttribute('aria-invalid', 'true');
    });

    it('does not bring a report back when the list returns after a row removal', async () => {
      const { rerender } = renderControlled({ values: ['10.0.0.1', '10.0.0.2'] });
      await typeAndSubmit('10.0.0.1');
      expect(alertText()).toContain('errors.duplicate');

      await fireEvent.click(screen.getByRole('button', { name: /10\.0\.0\.2/ }));
      await rerender({ values: ['10.0.0.1', '10.0.0.2'], draft: '' });

      expect(alertText()).toBe('');
    });

    it('replaces the first report with the second add', async () => {
      renderControlled();

      await typeAndSubmit('abc');
      await fireEvent.input(input(), { target: { value: '10.0.0.5, def' } });
      await fireEvent.keyDown(input(), { key: 'Enter' });

      expect(alertText()).toContain('def');
      expect(alertText()).not.toContain('abc');
    });
  });
});
