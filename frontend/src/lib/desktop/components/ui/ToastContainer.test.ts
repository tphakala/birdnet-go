import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { tick } from 'svelte';
import ToastContainer from './ToastContainer.svelte';
import { toastActions } from '$lib/stores/toast';

// setup.ts mocks the toast store without the `toasts` store this component
// reads; use the real store here.
vi.unmock('$lib/stores/toast');

// The shared i18n mock in src/test/setup.ts returns the key, so each region's
// accessible name is its translation key.
const REGION_KEYS = [
  'common.aria.toastRegion.topLeft',
  'common.aria.toastRegion.topCenter',
  'common.aria.toastRegion.topRight',
  'common.aria.toastRegion.bottomLeft',
  'common.aria.toastRegion.bottomCenter',
  'common.aria.toastRegion.bottomRight',
];

describe('ToastContainer', () => {
  it('renders one translated notification region per position', () => {
    render(ToastContainer);

    for (const key of REGION_KEYS) {
      expect(screen.getByRole('region', { name: key })).toBeInTheDocument();
    }
    expect(screen.getAllByRole('region')).toHaveLength(REGION_KEYS.length);
  });
});

describe('ToastContainer auto-dismiss', () => {
  const DEFAULT_DURATION_MS = 5000;
  const MARGIN_MS = 1000;

  afterEach(() => {
    vi.useRealTimers();
    toastActions.clear();
  });

  it('keeps a toast with a null duration on screen after the default duration', async () => {
    vi.useFakeTimers();
    render(ToastContainer);

    toastActions.error('Persistent failure', { duration: null });
    toastActions.info('Brief note');
    await tick();
    expect(screen.getByText('Persistent failure')).toBeInTheDocument();
    expect(screen.getByText('Brief note')).toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(DEFAULT_DURATION_MS + MARGIN_MS);

    expect(screen.getByText('Persistent failure')).toBeInTheDocument();
    expect(screen.queryByText('Brief note')).not.toBeInTheDocument();
  });
});

function blurActiveElement() {
  if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
}

describe('ToastContainer keyboard focus', () => {
  // Label from the shared i18n mock in src/test/setup.ts
  const CLOSE_LABEL = 'Close notification';
  const DEFAULT_DURATION_MS = 5000;
  const MARGIN_MS = 1000;
  // Mirrors MAX_TOASTS in $lib/stores/toast
  const MAX_TOASTS = 3;

  const closeButtons = () => screen.getAllByRole('button', { name: CLOSE_LABEL });
  const closeOf = (text: string): HTMLElement => {
    const alert = screen.getByText(text).closest<HTMLElement>('[role="alert"]');
    if (!alert) throw new Error(`The toast "${text}" is not shown`);
    return within(alert).getByRole('button', { name: CLOSE_LABEL });
  };

  let pageButton: HTMLButtonElement;

  beforeEach(() => {
    pageButton = document.createElement('button');
    pageButton.textContent = 'Page button';
    document.body.prepend(pageButton);
  });

  afterEach(() => {
    vi.useRealTimers();
    toastActions.clear();
    pageButton.remove();
  });

  it("closing a focused toast moves focus to the next toast's close button", async () => {
    const user = userEvent.setup();
    render(ToastContainer);
    toastActions.info('First', { duration: null });
    toastActions.info('Second', { duration: null });
    toastActions.info('Third', { duration: null });
    await tick();
    const [first, second] = closeButtons();
    first.focus();

    await user.keyboard('{Enter}');
    await tick();

    expect(screen.queryByText('First')).not.toBeInTheDocument();
    expect(second).toHaveFocus();
  });

  it('closing the last focused toast moves focus to the previous toast', async () => {
    const user = userEvent.setup();
    render(ToastContainer);
    toastActions.info('First', { duration: null });
    toastActions.info('Second', { duration: null });
    await tick();
    const [first, second] = closeButtons();
    second.focus();

    await user.keyboard('{Enter}');
    await tick();

    expect(screen.queryByText('Second')).not.toBeInTheDocument();
    expect(first).toHaveFocus();
  });

  it('closing the only focused toast returns focus to the element focused before', async () => {
    const user = userEvent.setup();
    render(ToastContainer);
    toastActions.info('Only', { duration: null });
    await tick();
    pageButton.focus();
    await user.tab();
    const [close] = closeButtons();
    expect(close).toHaveFocus();

    await user.keyboard('{Enter}');
    await tick();

    expect(screen.queryByText('Only')).not.toBeInTheDocument();
    expect(pageButton).toHaveFocus();
  });

  it('auto-dismiss of a focused toast keeps focus', async () => {
    vi.useFakeTimers();
    render(ToastContainer);
    toastActions.info('Brief note');
    toastActions.info('Persistent note', { duration: null });
    await tick();
    const [brief, persistent] = closeButtons();
    brief.focus();

    await vi.advanceTimersByTimeAsync(DEFAULT_DURATION_MS + MARGIN_MS);
    await tick();

    expect(screen.queryByText('Brief note')).not.toBeInTheDocument();
    expect(persistent).toHaveFocus();
  });

  it('a toast evicted past the limit while focused keeps focus', async () => {
    render(ToastContainer);
    for (let i = 1; i <= MAX_TOASTS; i++) toastActions.info(`Toast ${i}`, { duration: null });
    await tick();
    const [oldest, next] = closeButtons();
    oldest.focus();

    toastActions.info('One too many', { duration: null });
    await tick();

    expect(screen.queryByText('Toast 1')).not.toBeInTheDocument();
    expect(next).toHaveFocus();
  });

  it('removing a toast does not move focus when the user is elsewhere', async () => {
    render(ToastContainer);
    const id = toastActions.info('Unfocused', { duration: null });
    toastActions.info('Other', { duration: null });
    await tick();
    pageButton.focus();

    toastActions.remove(id);
    await tick();

    expect(pageButton).toHaveFocus();
  });

  it('removing a toast does not pull focus back after the user left it for the page', async () => {
    render(ToastContainer);
    const id = toastActions.info('Left behind', { duration: null });
    toastActions.info('Other', { duration: null });
    await tick();
    closeButtons()[0].focus();
    pageButton.focus();

    toastActions.remove(id);
    await tick();

    expect(pageButton).toHaveFocus();
  });

  it('removing a toast leaves focus on body alone after the user clicked the page', async () => {
    const user = userEvent.setup();
    render(ToastContainer);
    const id = toastActions.info('Clicked away', { duration: null });
    toastActions.info('Other', { duration: null });
    await tick();
    closeButtons()[0].focus();
    // A click on page content that takes no focus leaves focus on body
    await user.pointer({ target: document.body, keys: '[MouseLeft]' });
    blurActiveElement();

    toastActions.remove(id);
    await tick();

    expect(document.body).toHaveFocus();
  });
  it('closing a second toast in a row returns focus to the element focused before the toasts', async () => {
    const user = userEvent.setup();
    render(ToastContainer);
    toastActions.info('First', { duration: null });
    toastActions.info('Second', { duration: null });
    await tick();
    pageButton.focus();
    await user.tab();
    expect(closeButtons()[0]).toHaveFocus();

    // The first close hands focus to the second toast, the second has nowhere to go
    await user.keyboard('{Enter}');
    await tick();
    expect(closeButtons()[0]).toHaveFocus();
    await user.keyboard('{Enter}');
    await tick();

    expect(screen.queryByText('Second')).not.toBeInTheDocument();
    expect(pageButton).toHaveFocus();
  });

  it('closing the only toast returns focus to focusable SVG content that had it before', async () => {
    const user = userEvent.setup();
    render(ToastContainer);
    toastActions.info('Only', { duration: null });
    await tick();
    const chart = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    chart.setAttribute('tabindex', '0');
    document.body.append(chart);
    try {
      chart.focus();
      closeButtons()[0].focus();

      await user.keyboard('{Enter}');
      await tick();

      expect(screen.queryByText('Only')).not.toBeInTheDocument();
      expect(chart).toHaveFocus();
    } finally {
      chart.remove();
    }
  });

  it('a pointer press on SVG content outside the toasts ends focus tracking', async () => {
    render(ToastContainer);
    const id = toastActions.info('Left behind', { duration: null });
    toastActions.info('Other', { duration: null });
    await tick();
    closeButtons()[0].focus();
    const chart = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    document.body.append(chart);
    try {
      // A press on an SVG element takes no focus, so focus falls to <body>
      await fireEvent.pointerDown(chart);
      closeButtons()[0].blur();

      toastActions.remove(id);
      await tick();

      expect(document.body).toHaveFocus();
    } finally {
      chart.remove();
    }
  });
  it('removing a toast does not pull focus back after the user left it for a page element and focus later fell to body', async () => {
    render(ToastContainer);
    const id = toastActions.info('Left behind', { duration: null });
    toastActions.info('Other', { duration: null });
    await tick();
    closeButtons()[0].focus();
    pageButton.focus();
    pageButton.blur();
    expect(document.body).toHaveFocus();

    toastActions.remove(id);
    await tick();

    expect(document.body).toHaveFocus();
  });

  it('closing the middle toast moves focus to the next one, not the previous one', async () => {
    const user = userEvent.setup();
    render(ToastContainer);
    toastActions.info('First', { duration: null });
    toastActions.info('Second', { duration: null });
    toastActions.info('Third', { duration: null });
    await tick();
    closeOf('Second').focus();

    await user.keyboard('{Enter}');
    await tick();

    expect(screen.queryByText('Second')).not.toBeInTheDocument();
    expect(closeOf('Third')).toHaveFocus();
  });

  it('closing a focused toast in another position moves focus within that position', async () => {
    const user = userEvent.setup();
    render(ToastContainer);
    toastActions.info('Top right', { duration: null });
    toastActions.info('Bottom first', { duration: null, position: 'bottom-left' });
    toastActions.info('Bottom second', { duration: null, position: 'bottom-left' });
    await tick();
    closeOf('Bottom first').focus();

    await user.keyboard('{Enter}');
    await tick();

    expect(closeOf('Bottom second')).toHaveFocus();
  });

  it('closing the only toast of an earlier position returns focus to the page, not to a toast in a later position', async () => {
    const user = userEvent.setup();
    render(ToastContainer);
    toastActions.info('Early only', { duration: null, position: 'top-left' });
    toastActions.info('Late only', { duration: null, position: 'bottom-right' });
    await tick();
    pageButton.focus();
    closeOf('Early only').focus();

    await user.keyboard('{Enter}');
    await tick();

    expect(pageButton).toHaveFocus();
  });

  it('closing the only toast of a position returns focus to the page, not to a toast in another position', async () => {
    const user = userEvent.setup();
    render(ToastContainer);
    toastActions.info('Top right', { duration: null });
    toastActions.info('Bottom only', { duration: null, position: 'bottom-left' });
    await tick();
    pageButton.focus();
    closeOf('Bottom only').focus();

    await user.keyboard('{Enter}');
    await tick();

    expect(pageButton).toHaveFocus();
  });
});
