// Cleans web/dist while preserving the .gitkeep placeholder (Vite runs with emptyOutDir:false).
// `--ensure` only recreates .gitkeep if a build removed it.
import { existsSync, mkdirSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'

const dist = resolve(import.meta.dirname, '..', 'dist')
const ensureOnly = process.argv.includes('--ensure')

mkdirSync(dist, { recursive: true })
if (!ensureOnly) {
  for (const name of readdirSync(dist)) {
    if (name === '.gitkeep') continue
    rmSync(join(dist, name), { recursive: true, force: true })
  }
}
if (!existsSync(join(dist, '.gitkeep'))) writeFileSync(join(dist, '.gitkeep'), '')
