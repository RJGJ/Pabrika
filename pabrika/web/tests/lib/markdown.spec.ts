import { describe, expect, it } from 'vitest'
import { renderMarkdown } from '@/lib/markdown'

function dom(html: string): HTMLElement {
  const el = document.createElement('div')
  el.innerHTML = html
  return el
}

describe('renderMarkdown', () => {
  it('renders basic markdown', () => {
    const html = renderMarkdown('# Title\n\nsome **bold** and `code`\n\n- a\n- b')
    expect(html).toContain('<h1>Title</h1>')
    expect(html).toContain('<strong>bold</strong>')
    expect(html).toContain('<code>code</code>')
    expect(html).toContain('<li>a</li>')
  })
  it('renders tables and blockquotes', () => {
    const html = renderMarkdown('| a | b |\n|---|---|\n| 1 | 2 |\n\n> quote')
    expect(html).toContain('<table>')
    expect(html).toContain('<blockquote>')
  })
  it('turns single newlines into <br>', () => {
    expect(renderMarkdown('a\nb')).toContain('<br>')
  })
  it('empty input renders an empty string', () => {
    expect(renderMarkdown('')).toBe('')
  })

  describe('sanitization', () => {
    it('strips <script>', () => {
      const html = renderMarkdown('<script>alert(1)</script>hello')
      expect(html.toLowerCase()).not.toContain('<script')
      expect(dom(html).querySelector('script')).toBeNull()
    })
    it('raw HTML is not interpreted (shown as text)', () => {
      const el = dom(renderMarkdown('<b onclick="x()">hi</b> <img src=x onerror=alert(1)>'))
      expect(el.querySelector('b')).toBeNull()
      expect(el.querySelector('img')).toBeNull()
      expect(el.innerHTML).not.toMatch(/<[a-z]+[^>]*onerror/i)
    })
    it.each([
      '[x](javascript:alert(1))',
      '[x](JaVaScRiPt:alert(1))',
      '[x](&#106;avascript:alert(1))',
      '[x](&#x6A;avascript:alert(1))',
      '[x](java&#9;script:alert(1))',
      '[x]( javascript:alert(1))',
      '[x](vbscript:msgbox(1))',
      '[x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)',
      '<javascript:alert(1)>',
      '[x][r]\n\n[r]: javascript:alert(1)',
    ])('neutralizes link %s', (md) => {
      const el = dom(renderMarkdown(md))
      for (const a of Array.from(el.querySelectorAll('a'))) {
        const href = (a.getAttribute('href') ?? '').trim().toLowerCase()
        expect(href).not.toMatch(/^(javascript|vbscript|data):/)
        expect(href === '' || /^(https?:|mailto:)/.test(href)).toBe(true)
      }
      expect(el.innerHTML.toLowerCase()).not.toMatch(/href="\s*(javascript|vbscript|data):/)
    })
    it('drops iframes, svg, forms, style', () => {
      const html = renderMarkdown('<iframe src="https://evil"></iframe>\n\n<svg onload=alert(1)><script>alert(1)</script></svg>\n\n<form action="x"><input></form>\n\n<style>a{}</style>')
      const el = dom(html)
      for (const sel of ['iframe', 'svg', 'form', 'input', 'style', 'script']) {
        expect(el.querySelector(sel)).toBeNull()
      }
    })
    it('drops images (markdown syntax renders as text)', () => {
      const html = renderMarkdown('![alt](http://x/y.png)')
      const el = dom(html)
      expect(el.querySelector('img')).toBeNull()
      expect(el.textContent).toContain('![alt](http://x/y.png)')
    })
    it('no style attribute or event attributes survive', () => {
      const el = dom(renderMarkdown('<p style="color:red" onclick="x()">hi</p>'))
      for (const node of Array.from(el.querySelectorAll('*'))) {
        expect(node.hasAttribute('style')).toBe(false)
        expect(node.hasAttribute('onclick')).toBe(false)
      }
    })
  })

  describe('links', () => {
    it('adds target and rel to normal links', () => {
      const a = dom(renderMarkdown('[site](https://example.com/a?b=1)')).querySelector('a')!
      expect(a.getAttribute('href')).toBe('https://example.com/a?b=1')
      expect(a.getAttribute('target')).toBe('_blank')
      expect(a.getAttribute('rel')).toBe('noopener noreferrer nofollow')
    })
    it('allows mailto and linkified URLs', () => {
      const el = dom(renderMarkdown('[m](mailto:a@b.co) and https://example.org/x'))
      const hrefs = Array.from(el.querySelectorAll('a')).map((a) => a.getAttribute('href'))
      expect(hrefs).toContain('mailto:a@b.co')
      expect(hrefs).toContain('https://example.org/x')
      for (const a of Array.from(el.querySelectorAll('a'))) expect(a.getAttribute('target')).toBe('_blank')
    })
  })
})
