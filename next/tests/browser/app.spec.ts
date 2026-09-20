import { expect, test } from "@playwright/test";

test("loads the scaffold at the pinned browser profile", async ({ page }) => {
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "TeddyCloud Next" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Check typed client" })).toBeVisible();
  expect(page.viewportSize()).toEqual({ height: 900, width: 1440 });
  expect(await page.evaluate(() => window.devicePixelRatio)).toBe(1);
});
