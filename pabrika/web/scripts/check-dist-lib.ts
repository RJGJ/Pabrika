// Post-build assertions on a dist directory. Pure so it can be tested against fixture directories.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'

// Known-safe matches from bundled libraries (substring of the offending line context).
// Keep this list minimal; each entry must say why it is safe.
export const EVAL_ALLOWLIST: string[] = []

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else out.push(p)
  }
  return out
}

export function checkDist(dist: string): string[] {
  const errors: string[] = []
  const indexPath = join(dist, 'index.html')
  if (!existsSync(indexPath)) {
    errors.push('index.html is missing')
    return errors
  }
  const html = readFileSync(indexPath, 'utf8')

  for (const m of html.matchAll(/<script\b([^>]*)>/gi)) {
    if (!/\bsrc\s*=/.test(m[1])) errors.push('index.html has an inline <script>')
  }
  if (/\son[a-z]+\s*=/i.test(html.replace(/<script[\s\S]*?<\/script>/gi, ''))) {
    errors.push('index.html has an inline event handler attribute')
  }
  const extRef = /(?:src|href)\s*=\s*["'](?:https?:)?\/\/[^"']+["']/gi
  for (const m of html.matchAll(extRef)) errors.push(`index.html references external URL: ${m[0]}`)

  const assets = join(dist, 'assets')
  const assetFiles = existsSync(assets) ? readdirSync(assets).filter((f) => /[.-][A-Za-z0-9_-]{6,}\.\w+$/.test(f)) : []
  if (assetFiles.length === 0) errors.push('assets/ directory with hashed files is missing')

  for (const file of walk(dist)) {
    const rel = file.slice(dist.length + 1)
    if (rel === '.gitkeep') continue
    if (/\.(js|mjs)$/.test(file)) {
      const src = readFileSync(file, 'utf8')
      for (const re of [/\beval\s*\(/g, /new Function\s*\(/g]) {
        for (const m of src.matchAll(re)) {
          const ctx = src.slice(Math.max(0, m.index - 40), m.index + 60)
          if (!EVAL_ALLOWLIST.some((a) => ctx.includes(a))) {
            errors.push(`${rel} contains ${m[0].trim()}`)
            break
          }
        }
      }
    }
    if (/\.(js|mjs|css|html)$/.test(file)) {
      const src = readFileSync(file, 'utf8')
      // External resources only: ignore plain string literals such as namespaces, docs links.
      const css = /url\(\s*["']?https?:\/\//gi
      const imp = /@import\s+(?:url\()?["']https?:\/\//gi
      const dyn = /\bimport\(\s*["']https?:\/\//g
      const tags = /<(?:script|link|img|iframe)\b[^>]*(?:src|href)=["']https?:\/\//gi
      for (const re of [css, imp, dyn, tags]) {
        if (re.test(src)) errors.push(`${rel} references an external origin`)
      }
    }
  }
  return errors
}
