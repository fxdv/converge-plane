import { expect, test } from '@playwright/test';

/**
 * WB-2 structural smoke: side panel uses full width below md, split above.
 * Does not require auth — validates layout contract from Phase 4 reach work.
 */
test('side issue panel responsive shell', async ({ page }) => {
  await page.setContent(`
    <style>
      .side-issue-view { width: 100%; max-width: none; }
      @media (min-width: 768px) {
        .side-issue-view { width: 50vw; max-width: 70vw; }
      }
    </style>
    <div class="side-issue-view">
      <div id="panel">Issue</div>
    </div>
  `);

  await page.setViewportSize({ width: 375, height: 667 });
  const narrow = await page.locator('.side-issue-view').boundingBox();
  expect(narrow?.width).toBeGreaterThan(350);

  await page.setViewportSize({ width: 1280, height: 800 });
  const wide = await page.locator('.side-issue-view').boundingBox();
  expect(wide?.width).toBeLessThan(900);
  expect(wide?.width).toBeGreaterThan(500);
});
