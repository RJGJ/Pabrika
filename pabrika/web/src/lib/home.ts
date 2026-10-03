import type { Project } from '@/api/types'

/** Which project `/` should open: the remembered one if still listed, else the first, else none. */
export function pickHomeProject(list: readonly Project[], lastKey: string | null): string | null {
  if (lastKey) {
    const hit = list.find((p) => p.key.toLowerCase() === lastKey.toLowerCase())
    if (hit) return hit.key
  }
  return list.length > 0 ? list[0].key : null
}
