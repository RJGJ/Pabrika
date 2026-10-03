import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { checkDist } from '../scripts/check-dist-lib'

const dirs: string[] = []
function fixture(files: Record<string, string>): string {
  const dir = mkdtempSync(join(tmpdir(), 'dist-'))
  dirs.push(dir)
  for (const [name, content] of Object.entries(files)) {
    const p = join(dir, name)
    mkdirSync(join(p, '..'), { recursive: true })
    writeFileSync(p, content)
  }
  return dir
}
afterEach(() => {
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true })
})

const goodHtml = '<html><head><script type="module" src="/assets/index-AbCdEf12.js"></script></head><body></body></html>'
const good = { 'index.html': goodHtml, 'assets/index-AbCdEf12.js': 'console.log(1)', '.gitkeep': '' }

describe('checkDist', () => {
  it('passes a clean dist', () => {
    expect(checkDist(fixture(good))).toEqual([])
  })
  it('fails when index.html is missing', () => {
    expect(checkDist(fixture({ 'assets/a-AbCdEf12.js': '' }))).toContain('index.html is missing')
  })
  it('fails on an inline script', () => {
    const errs = checkDist(fixture({ ...good, 'index.html': '<script>alert(1)</script>' }))
    expect(errs.join()).toContain('inline <script>')
  })
  it('fails on an external script URL', () => {
    const errs = checkDist(fixture({ ...good, 'index.html': '<script src="https://cdn.example.com/x.js"></script>' }))
    expect(errs.join()).toContain('external')
  })
  it('fails on external url() in css', () => {
    const errs = checkDist(fixture({ ...good, 'assets/a-AbCdEf12.css': 'a{background:url(https://x.test/a.png)}' }))
    expect(errs.join()).toContain('external origin')
  })
  it('fails on eval in js', () => {
    const errs = checkDist(fixture({ ...good, 'assets/index-AbCdEf12.js': 'eval("1")' }))
    expect(errs.join()).toContain('eval(')
  })
  it('fails without hashed assets', () => {
    expect(checkDist(fixture({ 'index.html': goodHtml })).join()).toContain('assets/')
  })
})
