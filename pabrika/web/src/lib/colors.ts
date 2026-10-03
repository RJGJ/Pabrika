import type { LabelColor } from '@/api/types'

// Full class names are written out so Tailwind's scanner sees them.
const LABEL: Record<LabelColor, string> = {
  gray: 'bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-200',
  red: 'bg-red-100 text-red-800 dark:bg-red-900/60 dark:text-red-200',
  orange: 'bg-orange-100 text-orange-800 dark:bg-orange-900/60 dark:text-orange-200',
  amber: 'bg-amber-100 text-amber-900 dark:bg-amber-900/60 dark:text-amber-200',
  green: 'bg-green-100 text-green-800 dark:bg-green-900/60 dark:text-green-200',
  teal: 'bg-teal-100 text-teal-800 dark:bg-teal-900/60 dark:text-teal-200',
  blue: 'bg-blue-100 text-blue-800 dark:bg-blue-900/60 dark:text-blue-200',
  indigo: 'bg-indigo-100 text-indigo-800 dark:bg-indigo-900/60 dark:text-indigo-200',
  purple: 'bg-purple-100 text-purple-800 dark:bg-purple-900/60 dark:text-purple-200',
  pink: 'bg-pink-100 text-pink-800 dark:bg-pink-900/60 dark:text-pink-200',
}

const SWATCH: Record<LabelColor, string> = {
  gray: 'bg-gray-500', red: 'bg-red-500', orange: 'bg-orange-500', amber: 'bg-amber-500',
  green: 'bg-green-500', teal: 'bg-teal-500', blue: 'bg-blue-500', indigo: 'bg-indigo-500',
  purple: 'bg-purple-500', pink: 'bg-pink-500',
}

const PRIORITY: Record<string, string> = {
  low: 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300',
  medium: 'bg-blue-100 text-blue-800 dark:bg-blue-900/60 dark:text-blue-200',
  high: 'bg-orange-100 text-orange-800 dark:bg-orange-900/60 dark:text-orange-200',
  urgent: 'bg-red-100 text-red-800 dark:bg-red-900/60 dark:text-red-200',
}

export function labelClasses(color: string): string {
  return LABEL[color as LabelColor] ?? LABEL.gray
}

export function labelSwatchClass(color: string): string {
  return SWATCH[color as LabelColor] ?? SWATCH.gray
}

export function priorityClasses(priority: string): string {
  return PRIORITY[priority] ?? PRIORITY.low
}
