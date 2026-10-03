// Phase 6 security gate: raw HTML injection is allowed only in MarkdownView.vue (sanitized input).
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

function walk(dir: string, out: string[] = []): string[] {
  for (const n of readdirSync(dir)) {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.(vue|ts)$/.test(n)) out.push(p)
  }
  return out
}

describe('v-html and innerHTML usage', () => {
  it('appears only in components/ticket/MarkdownView.vue', () => {
    const src = resolve(import.meta.dirname, '..', 'src')
    const offenders = walk(src)
      .filter((f) => /v-html|innerHTML|insertAdjacentHTML/.test(readFileSync(f, 'utf8')))
      .map((f) => f.slice(src.length + 1).replaceAll('\\', '/'))
      .filter((f) => f !== 'components/ticket/MarkdownView.vue')
    expect(offenders).toEqual([])
  })
})
