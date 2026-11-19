/**
 * SettingsGroupLabel: the uppercase label of a subgroup of fields inside a
 * settings card, rendered on the element the caller picks.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { cleanup, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { renderTyped } from '../../../../../test/render-helpers';
import SettingsGroupLabel, {
  SETTINGS_GROUP_LABEL_CLASSES,
  type SettingsGroupLabelElement,
} from './SettingsGroupLabel.svelte';

const BASE_CLASSES = SETTINGS_GROUP_LABEL_CLASSES.split(' ');

function renderLabel(props: {
  as?: SettingsGroupLabelElement;
  divided?: boolean;
  class?: string;
  id?: string;
}) {
  renderTyped(SettingsGroupLabel, {
    props: {
      'data-testid': 'label',
      children: createRawSnippet(() => ({ render: () => '<span>Capture</span>' })),
      ...props,
    },
  });
  return screen.getByTestId('label');
}

describe('SettingsGroupLabel', () => {
  afterEach(() => {
    cleanup();
  });

  it.each([
    { as: undefined, tag: 'H4' },
    { as: 'h2' as const, tag: 'H2' },
    { as: 'span' as const, tag: 'SPAN' },
  ])('renders the chosen element and defaults to h4 (as=$as)', ({ as, tag }) => {
    const label = renderLabel({ as });

    expect(label.tagName).toBe(tag);
    expect(label).toHaveClass(...BASE_CLASSES);
    expect(label).toHaveTextContent('Capture');
  });

  it('draws the divider only when divided', () => {
    expect(renderLabel({})).not.toHaveClass('border-t');
    cleanup();

    expect(renderLabel({ divided: true })).toHaveClass('border-t', 'mt-2', 'pt-4');
  });

  it('appends the caller class and passes attributes to the element', () => {
    const label = renderLabel({ class: 'mb-3', id: 'capture-heading' });

    expect(label).toHaveClass('mb-3', ...BASE_CLASSES);
    expect(label).toHaveAttribute('id', 'capture-heading');
  });
});
