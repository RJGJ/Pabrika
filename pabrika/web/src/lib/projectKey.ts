/** Suggest a 2 to 6 letter project key from a name ("My Website" -> "MW", "Pabrika" -> "PAB"). */
export function suggestKey(name: string): string {
  const words: string[] = name.toUpperCase().match(/[A-Z]+/g) ?? []
  const first = words[0]
  if (first === undefined) return ''
  let key = words.length === 1 ? first.slice(0, 3) : words.map((w) => w.charAt(0)).join('').slice(0, 6)
  if (key.length < 2 && first.length >= 2) key = first.slice(0, 2)
  return key
}

/** Normalize typed key input: letters only, uppercase, at most 6. */
export function normalizeKey(input: string): string {
  return input.toUpperCase().replace(/[^A-Z]/g, '').slice(0, 6)
}

export function isValidKey(key: string): boolean {
  return /^[A-Z]{2,6}$/.test(key)
}
