import { describe, it, expect, vi } from 'vitest';
import {
  screen,
  createI18nMock,
  createComponentTestFactory,
  mockDOMAPIs,
} from '../../../../../test/render-helpers';
import SettingsCard from './SettingsCard.svelte';
import SettingsCardWrapper from './test-helpers/SettingsCardWrapper.test.svelte';

// Mock i18n translations
vi.mock('$lib/i18n', () => ({
  t: createI18nMock({
    'settings.card.changedAriaLabel': 'Settings changed',
    'settings.card.changed': 'Changed',
  }),
}));

// Set up DOM APIs for tests
mockDOMAPIs();

// Create test factory for reusable test patterns
const testFactory = createComponentTestFactory(SettingsCard);
const wrapperFactory = createComponentTestFactory(SettingsCardWrapper);

describe('SettingsCard', () => {
  describe('Rendering', () => {
    it('renders with minimal props', () => {
      testFactory.render();

      const card = screen.getByTestId('settings-card');
      expect(card).toBeInTheDocument();
      expect(card).toHaveClass('card', 'bg-base-100', 'shadow-xs');
    });

    it('renders with title and description', () => {
      testFactory.render({
        title: 'Test Card',
        description: 'Test description',
      });

      expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent('Test Card');
      expect(screen.getByText('Test description')).toBeInTheDocument();
    });

    it('applies custom className', () => {
      testFactory.render({
        className: 'custom-class test-class',
      });

      const card = screen.getByTestId('settings-card');
      expect(card).toHaveClass('custom-class', 'test-class');
    });

    it('renders children content', () => {
      wrapperFactory.render({
        childContent: 'Child content test',
      });

      expect(screen.getByTestId('child-content')).toHaveTextContent('Child content test');
    });

    it('renders custom header snippet', () => {
      wrapperFactory.render({
        showCustomHeader: true,
        headerContent: 'Custom Header',
      });

      expect(screen.getByTestId('custom-header')).toHaveTextContent('Custom Header');
      // Should not render default title
      expect(screen.queryByRole('heading', { level: 3 })).not.toBeInTheDocument();
    });

    it('renders footer snippet', () => {
      wrapperFactory.render({
        showCustomFooter: true,
        footerContent: 'Footer content',
      });

      expect(screen.getByTestId('custom-footer')).toHaveTextContent('Footer content');
    });
  });

  describe('Change Indicator', () => {
    it('shows change badge when hasChanges is true', () => {
      testFactory.render({
        title: 'Test Card',
        hasChanges: true,
      });

      const badge = screen.getByRole('status', { name: 'Settings changed' });
      expect(badge).toBeInTheDocument();
      expect(badge).toHaveClass('badge', 'badge-primary', 'badge-sm');
      expect(badge).toHaveTextContent('Changed');
    });

    it('does not show change badge when hasChanges is false', () => {
      testFactory.render({
        title: 'Test Card',
        hasChanges: false,
      });

      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
    });

    it('does not show change badge by default', () => {
      testFactory.render({
        title: 'Test Card',
      });

      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
    });
  });

  describe('Padding', () => {
    it('applies padding to body by default', () => {
      wrapperFactory.render({
        childContent: 'Content',
      });

      const bodyContainer = screen.getByTestId('settings-card').querySelector('.px-6.pb-6');
      expect(bodyContainer).toBeInTheDocument();
    });

    it('removes padding when padding prop is false', () => {
      wrapperFactory.render({
        padding: false,
        childContent: 'Content',
      });

      // When padding is false, the body div has no padding classes
      const card = screen.getByTestId('settings-card');
      const bodyDivs = card.querySelectorAll('div');

      // Find the body div (contains our content)
      const bodyDiv = Array.from(bodyDivs).find(div => div.textContent.includes('Content'));

      expect(bodyDiv).toBeInTheDocument();
      // When padding is false, bodyClasses is empty string
      expect(bodyDiv).not.toHaveClass('px-6', 'pb-6');
    });
  });

  describe('Conditional Rendering', () => {
    it('does not render header section when no title, description, or header provided', () => {
      testFactory.render();

      // Check that header container doesn't exist
      const headerContainer = screen.getByTestId('settings-card').querySelector('.px-6.py-4');
      expect(headerContainer).not.toBeInTheDocument();
    });

    it('renders header section with only title', () => {
      testFactory.render({
        title: 'Title Only',
      });

      const headerContainer = screen.getByTestId('settings-card').querySelector('.px-6.py-4');
      expect(headerContainer).toBeInTheDocument();
      expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent('Title Only');
    });

    it('renders header section with only description', () => {
      testFactory.render({
        description: 'Description Only',
      });

      const headerContainer = screen.getByTestId('settings-card').querySelector('.px-6.py-4');
      expect(headerContainer).toBeInTheDocument();
      expect(screen.getByText('Description Only')).toBeInTheDocument();
    });

    it('does not render body when no children provided', () => {
      testFactory.render({
        title: 'Test Card',
      });

      // Body container should still exist but be empty
      const bodyContainer = screen.getByTestId('settings-card').querySelector('.px-6.pb-6');
      expect(bodyContainer).toBeInTheDocument();
      expect(bodyContainer).toBeEmptyDOMElement();
    });

    it('does not render footer when no footer snippet provided', () => {
      testFactory.render({
        title: 'Test Card',
      });

      // Check that footer container doesn't exist
      const footerContainers = screen.getByTestId('settings-card').querySelectorAll('.px-6.pb-6');
      // Should only have one .px-6.pb-6 (the body), not two
      expect(footerContainers).toHaveLength(1);
    });
  });

  describe('HTML Attributes', () => {
    it('passes through additional HTML attributes', () => {
      testFactory.render({
        'data-custom': 'value',
        'aria-label': 'Custom card',
        id: 'test-card-id',
      });

      const card = screen.getByTestId('settings-card');
      expect(card).toHaveAttribute('data-custom', 'value');
      expect(card).toHaveAttribute('aria-label', 'Custom card');
      expect(card).toHaveAttribute('id', 'test-card-id');
    });
  });

  describe('Optimization Tests', () => {
    it('uses memoized showHeader correctly', async () => {
      const { rerender } = testFactory.render({
        title: 'Test',
      });

      // Header should be shown
      expect(screen.getByTestId('settings-card').querySelector('.px-6.py-4')).toBeInTheDocument();

      // Update to remove title - explicitly set to undefined to properly remove header
      await rerender({ title: undefined });

      // Header should be hidden
      expect(
        screen.getByTestId('settings-card').querySelector('.px-6.py-4')
      ).not.toBeInTheDocument();

      // Add description
      await rerender({ description: 'New description' });

      // Header should be shown again
      expect(screen.getByTestId('settings-card').querySelector('.px-6.py-4')).toBeInTheDocument();
    });

    it('uses memoized CSS classes', async () => {
      const { rerender } = testFactory.render({
        className: 'initial-class',
      });

      let card = screen.getByTestId('settings-card');
      expect(card).toHaveClass('initial-class');

      // Update className
      await rerender({ className: 'updated-class' });

      // Get the card element again after rerender
      card = screen.getByTestId('settings-card');
      // The component should maintain base classes and update custom class
      expect(card).toHaveClass('card', 'bg-base-100', 'shadow-xs', 'updated-class');
    });
  });

  describe('Integration with SettingsSection', () => {
    it('receives all required props from parent components', () => {
      testFactory.render({
        title: 'Integration Test',
        description: 'Testing integration',
        hasChanges: true,
        className: 'from-parent',
        defaultOpen: true, // This prop would be passed through
      });

      const card = screen.getByTestId('settings-card');
      expect(card).toHaveClass('from-parent');
      expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent('Integration Test');
      expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
    });
  });

  describe('Complex Content Scenarios', () => {
    it('renders all sections together', () => {
      wrapperFactory.render({
        title: 'Complete Card',
        description: 'All sections present',
        hasChanges: true,
        showCustomHeader: true,
        headerContent: 'Custom Header Content',
        childContent: 'Form Content',
        showCustomFooter: true,
        footerContent: 'Save Button',
      });

      // Custom header should override default title display
      expect(screen.getByTestId('custom-header')).toHaveTextContent('Custom Header Content');
      expect(screen.getByTestId('child-content')).toHaveTextContent('Form Content');
      expect(screen.getByTestId('custom-footer')).toHaveTextContent('Save Button');
    });

    it('handles empty snippets gracefully', () => {
      wrapperFactory.render({
        title: 'Test',
        showCustomHeader: true,
        headerContent: '',
        childContent: '',
        showCustomFooter: true,
        footerContent: '',
      });

      const card = screen.getByTestId('settings-card');
      expect(card).toBeInTheDocument();
      // Should still render structure even with empty snippets
    });
  });

  describe('Accessibility', () => {
    it('has proper heading hierarchy', () => {
      testFactory.render({
        title: 'Accessible Card',
      });

      const heading = screen.getByRole('heading', { level: 3 });
      expect(heading).toBeInTheDocument();
      expect(heading).toHaveTextContent('Accessible Card');
    });

    it('change badge has proper ARIA attributes', () => {
      testFactory.render({
        title: 'Test',
        hasChanges: true,
      });

      const badge = screen.getByRole('status', { name: 'Settings changed' });
      expect(badge).toHaveAttribute('aria-label', 'Settings changed');
    });

    it('maintains semantic structure', () => {
      testFactory.render({
        title: 'Semantic Test',
        description: 'Testing semantic HTML',
      });

      const card = screen.getByTestId('settings-card');

      // Card should be a div with proper classes
      expect(card.tagName).toBe('DIV');

      // Title should be in a heading
      const heading = screen.getByRole('heading');
      expect(heading.tagName).toBe('H3');

      // Description should be in a paragraph
      const description = screen.getByText('Testing semantic HTML');
      expect(description.tagName).toBe('P');
    });
  });
});
