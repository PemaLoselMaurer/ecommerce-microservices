// @ts-check
const { test, expect } = require("@playwright/test");
const services = require("../helpers/services");

// The happy path a customer actually walks: sign in, add to
// the cart, pay, and see the order in their history. This is
// the baseline the failure scenarios in the next file are
// measured against.

test.describe("Signing in and buying something", () => {
  test.beforeAll(async ({ request, baseURL }) => {
    services.resetSystem();
    await services.waitForHealthy(request, baseURL);
  });

  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    await expect(page.locator(".card").first()).toBeVisible();
  });

  /** Signs in through the dialog as one of the demo accounts. */
  async function signIn(page, email = "alice@example.com", password = "alice1234") {
    await page.getByRole("button", { name: "Sign in" }).first().click();
    await expect(page.locator("#auth-dialog")).toBeVisible();

    await page.fill("#login-email", email);
    await page.fill("#login-password", password);
    await page.locator("#login-form").getByRole("button", { name: "Sign in" }).click();
  }

  test("wrong credentials are refused without saying which part was wrong", async ({ page }) => {
    await signIn(page, "alice@example.com", "not-the-password");

    const error = page.locator("#login-error .note");
    await expect(error).toContainText("Incorrect email or password");

    // The dialog stays open and nobody is signed in.
    await expect(page.locator("#auth-dialog")).toBeVisible();
  });

  test("an unknown email gives exactly the same answer as a wrong password", async ({ page }) => {
    await signIn(page, "nobody@example.com", "alice1234");

    // Identical wording is what stops the form being used to
    // find out which addresses have accounts.
    await expect(page.locator("#login-error .note")).toContainText("Incorrect email or password");
  });

  test("signing in shows the customer's own name in the header", async ({ page }) => {
    await signIn(page);

    await expect(page.locator("#auth-dialog")).not.toBeVisible();
    await expect(page.locator("#account-slot")).toContainText("Alice");
    await expect(page.locator(".toast")).toContainText("Welcome back, Alice");
  });

  test("checkout is not offered until the shopper signs in", async ({ page }) => {
    const laptop = page.locator(".card").filter({ hasText: "Wireless Mouse" });
    await laptop.getByRole("button", { name: "Add to cart" }).click();

    await page.locator("#cart-open").click();
    await expect(page.locator("#cart-drawer")).toBeVisible();

    // Signed out, the cart offers a sign-in rather than a
    // checkout button.
    await expect(page.locator("#cart-foot")).toContainText("Sign in to complete your order");
    await expect(page.locator("#checkout-signin")).toBeVisible();
    await expect(page.locator("#checkout")).toHaveCount(0);
  });

  test("a signed-in shopper can pay and receives a payment reference", async ({ page }) => {
    await signIn(page);
    await expect(page.locator("#account-slot")).toContainText("Alice");

    // Add two different products.
    await page.locator(".card").filter({ hasText: "Mechanical Keyboard" })
      .getByRole("button", { name: "Add to cart" }).click();
    await page.locator(".card").filter({ hasText: "Wireless Mouse" })
      .getByRole("button", { name: "Add to cart" }).click();

    await expect(page.locator("#cart-count")).toHaveText("2");

    await page.locator("#cart-open").click();
    await page.locator("#checkout").click();

    // The checkout step shows what is being paid, to whom it
    // is being delivered, and how it is being settled.
    const checkout = page.locator("#checkout-dialog");
    await expect(checkout).toBeVisible();
    await expect(checkout).toContainText("Order summary");
    await expect(checkout).toContainText("Mechanical Keyboard");
    await expect(checkout).toContainText("Wireless Mouse");
    await expect(checkout).toContainText("Alice Nguyen");
    await expect(checkout).toContainText("Payment method");
    // Prices come from the serverless pricing function, one
    // order per line: 4,500 + 150 delivery, and 1,800 + 150
    // delivery, as both are under Nu. 5,000.
    await expect(page.locator("#checkout-summary")).toContainText("Nu. 6,600.00");
    await expect(page.locator("#pay-now")).toHaveText("Pay Nu. 6,600.00 and place order");

    await page.locator("#pay-now").click();

    // The receipt carries the reference the Payment Service
    // issued, which is the visible proof the payment happened.
    await expect(checkout.getByRole("heading", { name: "Payment successful" })).toBeVisible();
    await expect(checkout).toContainText("Payment reference");
    await expect(checkout).toContainText(/PAY\d{4}/);
    await expect(checkout).toContainText("CONFIRMED");

    // The receipt shows the pricing function's breakdown, and
    // the amount actually charged matches what was quoted.
    await expect(checkout).toContainText("Delivery");
    await expect(checkout).toContainText("Total charged");
    await expect(checkout).toContainText("Nu. 6,600.00");
  });

  // Lab 5, Part C: the pricing function's discount reaches the
  // customer through the normal checkout.
  test("a bulk order is discounted by the pricing function", async ({ page }) => {
    await signIn(page);

    const card = page.locator(".card").filter({ hasText: "Mechanical Keyboard" });
    await card.getByRole("button", { name: "Add to cart" }).click();
    await page.locator("#cart-open").click();
    await page.locator("[data-increase=P002]").click();
    await page.locator("[data-increase=P002]").click();
    await expect(page.locator("#cart-count")).toHaveText("3");

    await page.locator("#checkout").click();

    // 3 × 4,500 = 13,500, less 5% (675), free delivery.
    const summary = page.locator("#checkout-summary");
    await expect(summary).toContainText("Bulk discount (5%)");
    await expect(summary).toContainText("− Nu. 675.00");
    await expect(summary).toContainText("Free");
    await expect(page.locator("#pay-now")).toHaveText("Pay Nu. 12,825.00 and place order");

    await page.locator("#pay-now").click();

    const checkout = page.locator("#checkout-dialog");
    await expect(checkout.getByRole("heading", { name: "Payment successful" })).toBeVisible();
    await expect(checkout).toContainText("Bulk discount");
    await expect(checkout).toContainText("Nu. 12,825.00");
  });

  test("the order appears in the customer's own order history", async ({ page }) => {
    await signIn(page);

    await page.locator(".card").filter({ hasText: "Monitor" })
      .getByRole("button", { name: "Add to cart" }).click();

    await page.locator("#cart-open").click();
    await page.locator("#checkout").click();
    await page.locator("#pay-now").click();

    await expect(page.locator("#checkout-dialog")).toContainText("Payment successful");
    await page.locator("#receipt-orders").click();

    // Earlier tests in this file have already bought things as
    // Alice, so this looks for the order it just placed rather
    // than assuming the list is empty.
    const orders = page.locator("#orders-result .order");
    await expect(orders.first()).toBeVisible();

    const monitorOrder = orders.filter({ hasText: "Monitor" });
    await expect(monitorOrder).toHaveCount(1);
    await expect(monitorOrder).toContainText(/ORD\d{4}/);
    await expect(monitorOrder).toContainText(/PAY\d{4}/);
    await expect(monitorOrder).toContainText("CONFIRMED");
  });

  test("one customer never sees another customer's orders", async ({ page }) => {
    // Alice buys something.
    await signIn(page);
    await page.locator(".card").filter({ hasText: "Wireless Mouse" })
      .getByRole("button", { name: "Add to cart" }).click();
    await page.locator("#cart-open").click();
    await page.locator("#checkout").click();
    await page.locator("#pay-now").click();
    await expect(page.locator("#checkout-dialog")).toContainText("Payment successful");

    const aliceOrder = await page.locator("#checkout-dialog").textContent();
    const aliceOrderID = (aliceOrder || "").match(/ORD\d{4}/)?.[0];
    expect(aliceOrderID).toBeTruthy();

    // Alice signs out; Bob signs in.
    await page.locator("#receipt-close").click();
    await page.locator("#account-button").click();
    await page.locator("#signout-button").click();
    await expect(page.locator("#account-slot")).toContainText("Sign in");

    await signIn(page, "bob@example.com", "bob12345");
    await expect(page.locator("#account-slot")).toContainText("Bob");

    await page.getByRole("button", { name: "Orders", exact: true }).click();

    // Bob's history must not contain Alice's order.
    const orders = page.locator("#orders-result");
    await expect(orders).toBeVisible();
    await expect(orders).not.toContainText(String(aliceOrderID));
  });

  test("a sold-out product cannot be added to the cart", async ({ page }) => {
    const webcam = page.locator(".card").filter({ hasText: "Webcam" });

    await expect(webcam).toContainText("Out of stock");
    await expect(webcam.getByRole("button", { name: "Sold out" })).toBeDisabled();
  });

  test("registering a new account signs the customer straight in", async ({ page }) => {
    const unique = `karma${Date.now()}@example.com`;

    await page.getByRole("button", { name: "Sign in" }).first().click();
    await page.getByRole("tab", { name: "Create account" }).click();

    await page.fill("#register-name", "Karma Dorji");
    await page.fill("#register-email", unique);
    await page.fill("#register-address", "Norzin Lam, Thimphu");
    await page.fill("#register-password", "karma1234");
    await page.locator("#register-form").getByRole("button", { name: "Create account" }).click();

    await expect(page.locator("#auth-dialog")).not.toBeVisible();
    await expect(page.locator("#account-slot")).toContainText("Karma");
    await expect(page.locator(".toast")).toContainText("Welcome, Karma");
  });

  test("a password shorter than eight characters is refused", async ({ page }) => {
    await page.getByRole("button", { name: "Sign in" }).first().click();
    await page.getByRole("tab", { name: "Create account" }).click();

    await page.fill("#register-name", "Too Short");
    await page.fill("#register-email", `short${Date.now()}@example.com`);
    await page.fill("#register-password", "abc");
    await page.locator("#register-form").getByRole("button", { name: "Create account" }).click();

    await expect(page.locator("#register-error .note"))
      .toContainText("at least 8 characters");
  });
});
