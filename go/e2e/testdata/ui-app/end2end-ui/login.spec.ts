import { test, expect } from '@playwright/test';

// PREVIEW_URL comes from the task, so the spec runs against whatever preview the
// pipeline built. A literal host here would test that host forever.
test('the login page renders', async ({ page }) => {
  await page.goto(process.env.PREVIEW_URL!);
  await expect(page.getByRole('button', { name: /sign in/i })).toBeVisible();
});
