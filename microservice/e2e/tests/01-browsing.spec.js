// @ts-check
const { test, expect } = require("@playwright/test");
const services = require("../helpers/services");

// Part D, scenarios 1 to 4.
//
// Everything here happens through the storefront in a real
// browser: typing into the search box, pressing buttons,
// reading what appears on screen. Nothing calls the services
// directly.

test.describe("Browsing the storefront", () => {
  test.beforeAll(async ({ request, baseURL }) => {
    services.resetSystem();
    await services.waitForHealthy(request, baseURL);
  });

  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    // The catalogue arrives over the network, so wait for it
    // rather than for a fixed delay.
    await expect(page.locator(".card").first()).toBeVisible();
  });

  // ========================================================
  // Scenario 1 — an existing record
  // ========================================================

  test("Scenario 1: looking up a valid product ID shows its details", async ({ page }) => {
    await page.fill("#lookup-input", "P001");
    await page.press("#lookup-input", "Enter");

    const dialog = page.locator("#product-dialog");
    await expect(dialog).toBeVisible();

    // The details shown must be the ones the Product Service
    // holds, not a placeholder.
    await expect(dialog.getByRole("heading", { name: "Laptop" })).toBeVisible();
    await expect(dialog).toContainText("Nu. 75,000.00");
    await expect(dialog).toContainText("P001");
    await expect(dialog).toContainText("laptops");

    // The stock line comes from a second service.
    await expect(dialog).toContainText("10 available");
  });

  test("Scenario 1: the catalogue shows every product with live prices and stock", async ({ page }) => {
    const cards = page.locator(".card");
    await expect(cards).toHaveCount(8);

    const laptop = cards.filter({ hasText: "Laptop" }).first();
    await expect(laptop).toContainText("P001");
    await expect(laptop).toContainText("Nu. 75,000.00");
    await expect(laptop).toContainText("Nu. 88,000.00"); // the struck-through was-price
    await expect(laptop).toContainText("In stock");
    await expect(laptop).toContainText("BESTSELLER");
  });

  test("Scenario 1: stock states are shown correctly", async ({ page }) => {
    const cards = page.locator(".card");

    // P007 is stocked at 3, below the low-stock threshold.
    await expect(cards.filter({ hasText: "Tablet" })).toContainText("Only 3 left");

    // P008 is stocked at zero, so it cannot be bought.
    const webcam = cards.filter({ hasText: "Webcam" });
    await expect(webcam).toContainText("Out of stock");
    await expect(webcam.getByRole("button", { name: "Sold out" })).toBeDisabled();
  });

  // ========================================================
  // Scenario 2 — different existing records
  // ========================================================

  test("Scenario 2: several different IDs each return their own record", async ({ page }) => {
    const cases = [
      { id: "P002", name: "Mechanical Keyboard", price: "Nu. 4,500.00", stock: "25 available" },
      { id: "P004", name: "Monitor", price: "Nu. 25,000.00", stock: "8 available" },
      { id: "P005", name: "Noise-Cancelling Headphones", price: "Nu. 12,000.00", stock: "14 available" },
      { id: "P006", name: "Smartphone", price: "Nu. 48,000.00", stock: "6 available" },
    ];

    for (const expected of cases) {
      await page.fill("#lookup-input", expected.id);
      await page.press("#lookup-input", "Enter");

      const dialog = page.locator("#product-dialog");
      await expect(dialog).toBeVisible();
      await expect(dialog.getByRole("heading", { name: expected.name })).toBeVisible();
      await expect(dialog).toContainText(expected.price);
      await expect(dialog).toContainText(expected.stock);

      // The dialog offers two ways to close; target the
      // footer button rather than matching both.
      await dialog.locator("#product-dismiss").click();
      await expect(dialog).not.toBeVisible();
    }
  });

  test("Scenario 2: filtering by category returns only that category", async ({ page }) => {
    await page.getByRole("button", { name: "Mobile", exact: true }).click();

    const cards = page.locator(".card");
    await expect(cards).toHaveCount(2);
    await expect(cards.filter({ hasText: "Smartphone" })).toBeVisible();
    await expect(cards.filter({ hasText: "Tablet" })).toBeVisible();

    await page.getByRole("button", { name: "All", exact: true }).click();
    await expect(cards).toHaveCount(8);
  });

  // ========================================================
  // Scenario 3 — a record that does not exist
  // ========================================================

  test("Scenario 3: an unknown product ID shows a clear not-found message", async ({ page }) => {
    await page.fill("#lookup-input", "P999");
    await page.press("#lookup-input", "Enter");

    const result = page.locator("#lookup-result .note");
    await expect(result).toBeVisible();
    await expect(result).toContainText("Product with ID P999 not found");
    await expect(result).toContainText("NotFound");

    // A missing product is not an error dialog full of
    // product details.
    await expect(page.locator("#product-dialog")).not.toBeVisible();

    // The shopper is also told in the corner of the screen.
    await expect(page.locator(".toast").filter({ hasText: "Not found" })).toBeVisible();
  });

  test("Scenario 3: several unknown identifiers are all reported", async ({ page }) => {
    for (const id of ["P999", "ZZZZ", "12345", "p001"]) {
      await page.fill("#lookup-input", id);
      await page.press("#lookup-input", "Enter");

      const result = page.locator("#lookup-result .note");
      await expect(result).toContainText("not found");
      await expect(result).toContainText(id);
    }
  });

  // ========================================================
  // Scenario 4 — empty or invalid input
  // ========================================================

  test("Scenario 4: submitting an empty search asks for an ID instead of calling the service", async ({ page }) => {
    // If the empty search reached the gateway it would appear
    // here; the UI should stop it first.
    let calls = 0;
    page.on("request", (request) => {
      if (request.url().includes("/api/products/")) calls++;
    });

    await page.fill("#lookup-input", "");
    await page.press("#lookup-input", "Enter");

    const result = page.locator("#lookup-result .note");
    await expect(result).toBeVisible();
    await expect(result).toContainText("Please enter a product ID");
    await expect(result).toContainText("EMPTY_INPUT");

    expect(calls, "an empty search should not reach the service").toBe(0);
  });

  test("Scenario 4: a whitespace-only search is treated as empty", async ({ page }) => {
    await page.fill("#lookup-input", "   ");
    await page.press("#lookup-input", "Enter");

    await expect(page.locator("#lookup-result .note")).toContainText("Please enter a product ID");
  });

  test("Scenario 4: a name filter that matches nothing says so rather than failing", async ({ page }) => {
    await page.fill("#name-filter", "refrigerator");

    await expect(page.locator("#product-grid")).toContainText("No matching products");
    await expect(page.locator(".card")).toHaveCount(0);

    // Clearing the filter brings the catalogue back.
    await page.fill("#name-filter", "");
    await expect(page.locator(".card")).toHaveCount(8);
  });

  test("Scenario 4: signing in with empty fields is rejected before the request", async ({ page }) => {
    await page.getByRole("button", { name: "Sign in" }).first().click();
    await expect(page.locator("#auth-dialog")).toBeVisible();

    await page.locator("#login-form").getByRole("button", { name: "Sign in" }).click();

    await expect(page.locator("#login-error .note"))
      .toContainText("Please enter both your email and your password");
  });
});
