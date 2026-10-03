import { resolve } from 'node:path'
import { checkDist } from './check-dist-lib'

const dist = resolve(process.argv[2] ?? resolve(import.meta.dirname, '..', 'dist'))
const errors = checkDist(dist)
if (errors.length > 0) {
  console.error('check:dist failed:')
  for (const e of errors) console.error(' - ' + e)
  process.exit(1)
}
console.log('check:dist ok')
