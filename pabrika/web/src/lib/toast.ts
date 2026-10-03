import { toast } from 'vue-sonner'
import type { ToastKind } from '@/api/client'

export function notify(kind: ToastKind, message: string): void {
  if (kind === 'error') toast.error(message)
  else if (kind === 'success') toast.success(message)
  else toast(message)
}
