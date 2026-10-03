import type { MoveBody } from '@/api/types'

export type Placement = Pick<MoveBody, 'before' | 'after' | 'place'>

/**
 * Compute the server placement for a drop. `ids` is the target column's ordered ids WITHOUT the moved
 * ticket; `newIndex` is where the ticket landed. Neighbours are therefore always tickets currently in
 * the target column (the server requires this). Raw positions are never sent.
 */
export function computePlacement(ids: readonly string[], newIndex: number): Placement {
  const i = Math.max(0, Math.min(newIndex, ids.length))
  if (i > 0) return { after: ids[i - 1] }
  if (ids.length > 0) return { before: ids[0] }
  return { place: 'top' }
}

/** A drop in the same column at the same index changes nothing and sends no request. */
export function isNoopDrop(fromStatus: string, toStatus: string, oldIndex: number, newIndex: number): boolean {
  return fromStatus === toStatus && oldIndex === newIndex
}
