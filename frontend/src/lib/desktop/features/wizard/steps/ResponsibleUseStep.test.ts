import { describe, it, expect, vi } from 'vitest';
import { screen, fireEvent } from '@testing-library/svelte';
import { renderTyped } from '../../../../../test/render-helpers';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';

import ResponsibleUseStep from './ResponsibleUseStep.svelte';

const ACKNOWLEDGE_NAME = /wizard\.steps\.responsibleUse\.acknowledge/;
const PRIMARY_BORDER_CLASS = 'border-[var(--color-primary)]';
const PRIMARY_TINT_CLASS = 'bg-[var(--color-primary)]/5';
const IDLE_BORDER_CLASS = 'border-[var(--border-200)]';
const HOVER_BORDER_CLASS = 'hover:border-[var(--border-300)]';

const acknowledgeBox = () => screen.getByRole('checkbox', { name: ACKNOWLEDGE_NAME });

/** The card the acknowledgement checkbox sits in. */
function card(): HTMLLabelElement {
  const label = acknowledgeBox().closest('label');
  if (!label) throw new Error('acknowledgement checkbox is not inside a label');
  return label;
}

describe('ResponsibleUseStep', () => {
  it('reports invalid at mount and valid once acknowledged', async () => {
    const onValidChange = vi.fn();
    renderTyped(ResponsibleUseStep, { props: { onValidChange } });

    expect(onValidChange).toHaveBeenLastCalledWith(false);

    await fireEvent.click(acknowledgeBox());
    expect(onValidChange).toHaveBeenLastCalledWith(true);

    await fireEvent.click(acknowledgeBox());
    expect(onValidChange).toHaveBeenLastCalledWith(false);
  });

  it('a click anywhere on the card toggles the acknowledgement', async () => {
    renderTyped(ResponsibleUseStep, { props: {} });
    expect(acknowledgeBox()).not.toBeChecked();

    await fireEvent.click(card());
    expect(acknowledgeBox()).toBeChecked();

    await fireEvent.click(card());
    expect(acknowledgeBox()).not.toBeChecked();
  });

  it('marks the card with the primary border and tint while acknowledged, and the idle and hover border otherwise', async () => {
    renderTyped(ResponsibleUseStep, { props: {} });
    expect(card()).toHaveClass(IDLE_BORDER_CLASS, HOVER_BORDER_CLASS);
    expect(card()).not.toHaveClass(PRIMARY_BORDER_CLASS);
    expect(card()).not.toHaveClass(PRIMARY_TINT_CLASS);

    await fireEvent.click(acknowledgeBox());
    expect(card()).toHaveClass(PRIMARY_BORDER_CLASS, PRIMARY_TINT_CLASS);
    expect(card()).not.toHaveClass(IDLE_BORDER_CLASS);
    expect(card()).not.toHaveClass(HOVER_BORDER_CLASS);

    await fireEvent.click(acknowledgeBox());
    expect(card()).toHaveClass(IDLE_BORDER_CLASS, HOVER_BORDER_CLASS);
    expect(card()).not.toHaveClass(PRIMARY_BORDER_CLASS);
    expect(card()).not.toHaveClass(PRIMARY_TINT_CLASS);
  });
});

describe('ResponsibleUseStep motion', () => {
  it('transitions only colours on the card and drops the transition under reduced motion', () => {
    renderTyped(ResponsibleUseStep, { props: {} });

    expect(card()).toHaveClass(
      'transition-[color,background-color,border-color]',
      'motion-reduce:transition-none'
    );
    expect(card()).not.toHaveClass('transition-colors');
  });
});

describe('ResponsibleUseStep Accessibility', () => {
  it('has no violations while unchecked', async () => {
    const { container } = renderTyped(ResponsibleUseStep, { props: {} });
    expect(acknowledgeBox()).not.toBeChecked();

    await expectNoA11yViolations(container);
  });

  it('has no violations once acknowledged', async () => {
    const { container } = renderTyped(ResponsibleUseStep, { props: {} });
    await fireEvent.click(acknowledgeBox());
    expect(acknowledgeBox()).toBeChecked();

    await expectNoA11yViolations(container);
  });
});
