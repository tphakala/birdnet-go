import { describe, it, expect } from 'vitest';
import {
  screen,
  createComponentTestFactory,
  mockDOMAPIs,
} from '../../../../../test/render-helpers';
import SettingsNoteWrapper from './test-helpers/SettingsNoteWrapper.test.svelte';

// Set up DOM APIs for tests
mockDOMAPIs();

// Create test factory for reusable test patterns
const wrapperFactory = createComponentTestFactory(SettingsNoteWrapper);

describe('SettingsNote', () => {
  describe('Rendering', () => {
    it('renders with children content', () => {
      wrapperFactory.render({
        childContent: '<p>This is a note</p>',
      });

      const content = screen.getByTestId('note-content');
      expect(content).toBeInTheDocument();
      expect(content.textContent).toContain('This is a note');
    });

    it('applies base styles', () => {
      wrapperFactory.render({
        childContent: 'Note',
      });

      const noteContainer =
        screen.getByTestId('note-content').parentElement?.parentElement?.parentElement;
      expect(noteContainer).toHaveClass('mt-4', 'p-4', 'bg-base-200', 'text-sm', 'rounded-lg');
    });

    it('applies custom className', () => {
      wrapperFactory.render({
        className: 'custom-note warning-note',
        childContent: 'Custom styled note',
      });

      const noteContainer =
        screen.getByTestId('note-content').parentElement?.parentElement?.parentElement;
      expect(noteContainer).toHaveClass('custom-note', 'warning-note');
      // Should still have base classes
      expect(noteContainer).toHaveClass('mt-4', 'p-4', 'bg-base-200', 'text-sm', 'rounded-lg');
    });
  });

  describe('Icon Support', () => {
    it('renders without icon by default', () => {
      wrapperFactory.render({
        childContent: '<span>No icon note</span>',
      });

      const content = screen.getByTestId('note-content');
      expect(content.textContent).toContain('No icon note');
      // Should not have icon
      expect(screen.queryByTestId('note-icon')).not.toBeInTheDocument();
    });

    it('renders with icon when provided', () => {
      wrapperFactory.render({
        showIcon: true,
        childContent: '<p>Note with icon</p>',
      });

      const icon = screen.getByTestId('note-icon');
      const content = screen.getByTestId('note-content');

      expect(icon).toBeInTheDocument();
      expect(content).toBeInTheDocument();
      expect(content.textContent).toContain('Note with icon');

      // Check flex layout structure
      const flexContainer = icon.parentElement?.parentElement;
      expect(flexContainer).toHaveClass('flex', 'gap-3');

      // Icon should be in shrink-0 container
      expect(icon.parentElement).toHaveClass('shrink-0');

      // Content should be in flex-1 container
      expect(content.parentElement).toHaveClass('flex-1');
    });
  });

  describe('Complex Content', () => {
    it('renders multiple paragraphs', () => {
      wrapperFactory.render({
        childContent: `
          <p data-testid="para-1">First paragraph</p>
          <p data-testid="para-2" class="mt-2">Second paragraph</p>
        `,
      });

      expect(screen.getByTestId('para-1')).toHaveTextContent('First paragraph');
      expect(screen.getByTestId('para-2')).toHaveTextContent('Second paragraph');
      expect(screen.getByTestId('para-2')).toHaveClass('mt-2');
    });

    it('renders with links', () => {
      wrapperFactory.render({
        childContent: `
          Note with <a href="/docs" class="link link-primary" data-testid="note-link">documentation link</a>.
        `,
      });

      const link = screen.getByTestId('note-link');
      expect(link).toBeInTheDocument();
      expect(link).toHaveAttribute('href', '/docs');
      expect(link).toHaveClass('link', 'link-primary');
      expect(link).toHaveTextContent('documentation link');
    });

    it('renders with code blocks', () => {
      wrapperFactory.render({
        childContent: `
          <p>Use this command:</p>
          <code data-testid="code-block" class="bg-base-300 px-2 py-1 rounded-sm">npm install</code>
        `,
      });

      const codeBlock = screen.getByTestId('code-block');
      expect(codeBlock).toBeInTheDocument();
      expect(codeBlock).toHaveTextContent('npm install');
      expect(codeBlock).toHaveClass('bg-base-300', 'px-2', 'py-1', 'rounded-sm');
    });

    it('renders with lists', () => {
      wrapperFactory.render({
        childContent: `
          <p>Important points:</p>
          <ul data-testid="note-list" class="list-disc list-inside mt-2">
            <li>First item</li>
            <li>Second item</li>
            <li>Third item</li>
          </ul>
        `,
      });

      const list = screen.getByTestId('note-list');
      expect(list).toBeInTheDocument();
      expect(list).toHaveClass('list-disc', 'list-inside', 'mt-2');
      expect(list.children).toHaveLength(3);
    });
  });

  describe('Edge Cases', () => {
    it('handles empty children gracefully', () => {
      wrapperFactory.render({
        childContent: '',
      });

      // Should still render the note container structure
      // Use getAllByRole since there are multiple divs (generic role)
      const allDivs = screen.getAllByRole('generic');
      // Find the main note container by its classes
      const noteContainer = allDivs.find(
        div =>
          div.classList.contains('mt-4') &&
          div.classList.contains('p-4') &&
          div.classList.contains('bg-base-200')
      );
      expect(noteContainer).toBeInTheDocument();

      // But note-content div won't be rendered when childContent is empty
      expect(screen.queryByTestId('note-content')).not.toBeInTheDocument();
    });

    it('handles empty icon snippet', () => {
      wrapperFactory.render({
        showIcon: true,
        childContent: '<span>Note with icon</span>',
      });

      const icon = screen.getByTestId('note-icon');
      const content = screen.getByTestId('note-content');
      expect(icon).toBeInTheDocument();
      expect(content).toBeInTheDocument();

      // Should still have flex layout
      const flexContainer = icon.parentElement?.parentElement;
      expect(flexContainer).toHaveClass('flex', 'gap-3');
    });

    it('handles className edge cases', () => {
      const { rerender } = wrapperFactory.render({
        className: '',
        childContent: 'Note',
      });

      let container =
        screen.getByTestId('note-content').parentElement?.parentElement?.parentElement;
      expect(container).toHaveClass('mt-4', 'p-4', 'bg-base-200', 'text-sm', 'rounded-lg');

      // Test with undefined className
      rerender({
        childContent: 'Note Updated',
      });

      container = screen.getByTestId('note-content').parentElement?.parentElement?.parentElement;
      expect(container).toHaveClass('mt-4', 'p-4', 'bg-base-200', 'text-sm', 'rounded-lg');
    });
  });

  describe('Semantic Structure', () => {
    it('maintains proper HTML structure without icon', () => {
      wrapperFactory.render({
        childContent: '<p>Semantic test</p>',
      });

      const container =
        screen.getByTestId('note-content').parentElement?.parentElement?.parentElement;
      expect(container?.tagName).toBe('DIV');

      // Should have one child div
      expect(container?.children).toHaveLength(1);
      expect(container?.children[0].tagName).toBe('DIV');
    });

    it('maintains proper HTML structure with icon', () => {
      wrapperFactory.render({
        showIcon: true,
        childContent: '<p>Content</p>',
      });

      const container =
        screen.getByTestId('note-content').parentElement?.parentElement?.parentElement;
      expect(container?.tagName).toBe('DIV');

      // Should have flex container
      const flexContainer = container?.querySelector('.flex');
      expect(flexContainer).toBeInTheDocument();

      // Should have two children (icon and content containers)
      expect(flexContainer?.children).toHaveLength(2);
    });
  });

  describe('Accessibility', () => {
    it('preserves semantic content structure', () => {
      wrapperFactory.render({
        childContent: `
          <h4>Note heading</h4>
          <p>Note paragraph</p>
        `,
      });

      // Content structure should be preserved
      expect(screen.getByRole('heading', { level: 4 })).toHaveTextContent('Note heading');
      expect(screen.getByText('Note paragraph').tagName).toBe('P');
    });

    it('supports ARIA attributes', () => {
      wrapperFactory.render({
        className: 'aria-live:polite',
        childContent: '<div role="alert">Alert message</div>',
      });

      const alert = screen.getByRole('alert');
      expect(alert).toHaveTextContent('Alert message');
    });

    it('maintains link accessibility', () => {
      wrapperFactory.render({
        childContent: `
          See <a href="/help" aria-label="Help documentation">help docs</a> for more info.
        `,
      });

      const link = screen.getByRole('link', { name: 'Help documentation' });
      expect(link).toHaveAttribute('href', '/help');
    });
  });

  describe('Integration Patterns', () => {
    it('works as an info note', () => {
      wrapperFactory.render({
        className: 'text-info',
        showIcon: true,
        childContent: '<p><strong>Info:</strong> This is an informational message.</p>',
      });

      const container =
        screen.getByTestId('note-content').parentElement?.parentElement?.parentElement;
      expect(container).toHaveClass('text-info');
      expect(screen.getByText('Info:')).toBeInTheDocument();
    });

    it('works as a warning note', () => {
      wrapperFactory.render({
        className: 'text-warning border border-warning',
        childContent: `
          <p class="font-semibold">Warning</p>
          <p class="mt-1">This action cannot be undone.</p>
        `,
      });

      const container =
        screen.getByTestId('note-content').parentElement?.parentElement?.parentElement;
      expect(container).toHaveClass('text-warning', 'border', 'border-warning');
      expect(screen.getByText('Warning')).toHaveClass('font-semibold');
    });

    it('works with dynamic content', async () => {
      const { rerender } = wrapperFactory.render({
        childContent: '<p data-testid="dynamic-content">Initial content</p>',
      });

      expect(screen.getByTestId('dynamic-content')).toHaveTextContent('Initial content');

      // Update content
      await rerender({
        childContent: '<p data-testid="dynamic-content">Updated content</p>',
      });

      expect(screen.getByTestId('dynamic-content')).toHaveTextContent('Updated content');
    });
  });

  describe('Real-World Usage', () => {
    it('renders privacy filter note pattern', () => {
      wrapperFactory.render({
        childContent: `
          <p data-testid="privacy-note">
            The privacy filter prevents species specified in the list below from being saved 
            in the database, sent to a proxy, or included in MQTT messages. No audio is 
            retained by the application.
          </p>
        `,
      });

      const note = screen.getByTestId('privacy-note');
      expect(note).toBeInTheDocument();
      expect(note.textContent).toContain('privacy filter');
    });

    it('renders security configuration note', () => {
      wrapperFactory.render({
        childContent: `
          <p>
            <strong>Note:</strong> To fully enable authentication, ensure the 
            <code class="bg-base-300 px-1 rounded-sm">redirectURI</code> is correctly configured 
            in your OAuth provider settings.
          </p>
        `,
      });

      expect(screen.getByText('Note:')).toBeInTheDocument();
      expect(screen.getByText('redirectURI')).toHaveClass('bg-base-300', 'px-1', 'rounded-sm');
    });

    it('renders system requirement note', () => {
      wrapperFactory.render({
        showIcon: true,
        childContent: `
          <p class="font-medium">System Requirements</p>
          <ul class="list-disc list-inside mt-2 space-y-1">
            <li>Minimum 2GB RAM</li>
            <li>64-bit processor</li>
            <li>Active internet connection</li>
          </ul>
        `,
      });

      expect(screen.getByText('System Requirements')).toHaveClass('font-medium');
      const list = screen.getByRole('list');
      expect(list.children).toHaveLength(3);
    });
  });
});
