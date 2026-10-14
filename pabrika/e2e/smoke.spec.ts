import { test, expect } from '@playwright/test'

test('AC0: server serves the sign-in page', async ({ page }) => {
  const res = await page.goto('/login')
  expect(res?.ok()).toBe(true)
  await expect(page.locator('body')).not.toBeEmpty()
})
