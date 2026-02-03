/**
 * Copyright 2024 Defense Unicorns
 * SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial
 */

import { test, expect } from "@playwright/test";

// Helper to generate unique keys to prevent test collisions
function randomKeyName() {
  return `auto-test-key-${Math.floor(Math.random() * 10000)}`;
}

test("verify database connection and set key-value", async ({ page }) => {
  // 1. Navigate to the application root
  await page.goto("/");

  // 2. DB Connection Check
  // The app starts with "Connecting...". We wait for the status bar to confirm "Online".
  await expect(page.locator("#dbStatus")).toContainText("Database Online", {
    timeout: 15000,
  });
  await expect(page.locator("#dbStatus")).toHaveClass(/status-online/);

  // 3. Perform Action (Set Key/Value)
  const keyName = randomKeyName();
  const valueData = "integration-test-value";

  // Using locators by ID as defined in your index.html
  await page.locator("#key").fill(keyName);
  await page.locator("#value").fill(valueData);
  await page.locator("#setBtn").click();

  // 4. Verify Result
  // The app automatically refreshes the table. We check if a row with our text exists.
  const newRow = page.locator("tr", { hasText: keyName });

  await expect(newRow).toBeVisible();
  await expect(newRow).toContainText(valueData);
});