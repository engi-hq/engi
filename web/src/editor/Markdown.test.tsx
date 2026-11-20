import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Markdown, safeHref } from './Markdown';

const html = (src: string) => render(<Markdown source={src} />).container;

describe('Markdown', () => {
  it('renders the common blocks and inline marks', () => {
    const c = html('# Title\n\nSome **bold**, *italic*, ~~gone~~ and `code`.\n\n- one\n- two\n\n1. first\n2. second\n\n> quoted\n\n```\nlet x = 1\n```\n\n---');
    expect(c.querySelector('h1')?.textContent).toBe('Title');
    expect(c.querySelector('strong')?.textContent).toBe('bold');
    expect(c.querySelector('em')?.textContent).toBe('italic');
    expect(c.querySelector('del')?.textContent).toBe('gone');
    expect(c.querySelector('p code')?.textContent).toBe('code');
    expect([...c.querySelectorAll('ul li')].map((l) => l.textContent)).toEqual(['one', 'two']);
    expect([...c.querySelectorAll('ol li')].map((l) => l.textContent)).toEqual(['first', 'second']);
    expect(c.querySelector('blockquote')?.textContent).toBe('quoted');
    expect(c.querySelector('pre code')?.textContent).toBe('let x = 1');
    expect(c.querySelector('hr')).not.toBeNull();
  });

  it('keeps snake_case words and a lone star as text', () => {
    const c = html('use snake_case_name and 2 * 3');
    expect(c.querySelector('em')).toBeNull();
    expect(c.textContent).toBe('use snake_case_name and 2 * 3');
  });

  it('shows raw HTML as text and creates no elements from it', () => {
    const c = html('<script>window.hacked = 1</script>\n\n<img src=x onerror="window.hacked=1"> and <b>bold</b>');
    expect(c.querySelector('script')).toBeNull();
    expect(c.querySelector('img')).toBeNull();
    expect(c.querySelector('b')).toBeNull();
    expect(c.textContent).toContain('<script>window.hacked = 1</script>');
    expect((window as unknown as { hacked?: number }).hacked).toBeUndefined();
  });

  it('opens http links in a new tab without opener access', () => {
    html('[Docs](https://example.com/a?b=1) and [mail](mailto:a@b.co)');
    const a = screen.getByRole('link', { name: 'Docs' });
    expect(a).toHaveAttribute('href', 'https://example.com/a?b=1');
    expect(a).toHaveAttribute('target', '_blank');
    expect(a.getAttribute('rel')).toContain('noopener');
    expect(screen.getByRole('link', { name: 'mail' })).toHaveAttribute('href', 'mailto:a@b.co');
  });

  it.each([
    ['javascript:alert'],
    ['JaVaScRiPt:alert'],
    ['data:text/html,<b>x</b>'],
    ['vbscript:x'],
    ['/relative/path'],
    ['//evil.example'],
  ])('renders %s as plain text, not a link', (url) => {
    const c = html(`[click](${url})`);
    expect(c.querySelector('a')).toBeNull();
    expect(c.textContent).toBe('click');
  });

  it('does not render images', () => {
    const c = html('![alt](https://example.com/x.png)');
    expect(c.querySelector('img')).toBeNull();
  });
});

describe('safeHref', () => {
  it('allows only http, https and mailto', () => {
    expect(safeHref(' https://a.b ')).toBe('https://a.b');
    expect(safeHref('javascript:1')).toBeNull();
    expect(safeHref('')).toBeNull();
  });
});
