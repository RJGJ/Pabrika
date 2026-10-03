import MarkdownIt from 'markdown-it'
import DOMPurify from 'dompurify'

type InlineRule = (state: unknown, silent: boolean) => boolean
interface MdWithRules {
  inline: { ruler: { __rules__: { name: string; fn: InlineRule }[]; at(name: string, fn: InlineRule): void } }
}

const md = new MarkdownIt({ html: false, linkify: true, breaks: true })

// Images are dropped in v1 (no remote content): `![x](y)` renders as the literal source text.
// markdown-it's own image rule is wrapped so the consumed range is emitted as plain text.
{
  const ruler = (md as unknown as MdWithRules).inline.ruler
  const original = ruler.__rules__.find((r) => r.name === 'image')?.fn
  if (original) {
    ruler.at('image', (state, silent) => {
      const s = state as { pos: number; src: string; push(type: string, tag: string, nesting: number): { content: string } }
      const start = s.pos
      if (!original(state, true)) return false
      // The original (silent) call leaves pos at the end of the image syntax.
      const end = s.pos
      if (!silent) {
        const tok = s.push('text', '', 0)
        tok.content = s.src.slice(start, end)
      }
      return true
    })
  }
}

const ALLOWED_TAGS = [
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'p', 'ul', 'ol', 'li', 'code', 'pre', 'blockquote',
  'strong', 'em', 'del', 's', 'table', 'thead', 'tbody', 'tr', 'th', 'td', 'a', 'hr', 'br',
]

DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A' && node.hasAttribute('href')) {
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer nofollow')
  }
})

/** Render markdown to sanitized HTML. The only consumer of the result is MarkdownView (v-html). */
export function renderMarkdown(src: string): string {
  if (!src) return ''
  const raw = md.render(src)
  return DOMPurify.sanitize(raw, {
    ALLOWED_TAGS,
    ALLOWED_ATTR: ['href', 'title', 'target', 'rel'],
    ALLOWED_URI_REGEXP: /^(?:https?:|mailto:)/i,
    ALLOW_DATA_ATTR: false,
    ALLOW_ARIA_ATTR: false,
    FORBID_TAGS: ['img', 'svg', 'math', 'iframe', 'form', 'input', 'style', 'script', 'object', 'embed'],
    FORBID_ATTR: ['style'],
  })
}
