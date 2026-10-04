// @ts-check
const { test, expect } = require("@playwright/test");
const services = require("../helpers/services");

// Part D, scenarios 5 to 8.
//
// Each of these genuinely breaks the running system — a
// service is stopped, or restarted with a fault injected — and
// then checks what a customer sees in the browser. Nothing is
// mocked; the storefront is talking to a system that really is
// in trouble.
//
// Every test puts the system back afterwards, so the demo is
// left in working order.

test.describe("When the system is in trouble", () => {
  test.beforeAll(async ({ request, baseURL }) => {
    services.resetSystem();
    await services.waitForHealthy(request, baseURL);
  });

  // Each test here restarts services, so the next one waits
  // for the system to settle rather than racing a gateway that
  // is still reconnecting.
  test.beforeEach(async ({ request, baseURL }) => {
    await services.waitForHealthy(request, baseURL);
  });

  test.afterEach(async ({ request, baseURL }) => {
    services.resetSystem();
    await services.waitForHealthy(request, baseURL);
  });

  /**
   * Signs in as Alice through the dialog.
   *
   * Sign-in is setup for most of these tests rather than the
   * thing under test, so when it fails this reports the reason
   * the page gave instead of leaving a bare timeout on the
   * header.
   */
  async function signIn(page) {
    await page.getByRole("button", { name: "Sign in" }).first().click();
    await expect(page.locator("#auth-dialog")).toBeVisible();

    await page.fill("#login-email", "alice@example.com");
    await page.fill("#login-password", "alice1234");

    const [response] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("/api/auth/login")),
      page.locator("#login-form").getByRole("button", { name: "Sign in" }).click(),
    ]);

    if (!response.ok()) {
      throw new Error(`sign-in failed with HTTP ${response.status()}: ${await response.text()}`);
    }

    await expect(page.locator("#account-slot")).toContainText("Alice");
  }

  // ========================================================
  // Scenario 5 — a dependent service is unavailable
  // ========================================================

  test("Scenario 5: with the Product Service stopped, the catalogue explains itself", async ({ page, request, baseURL }) => {
    services.stop(["product"]);
    await services.waitForDown(request, baseURL, "product");

    await page.goto("/");

    const grid = page.locator("#product-grid");
    await expect(grid).toContainText("The Product Service is unavailable right now");
    await expect(grid).toContainText("Unavailable");

    // The shopper is offered a way forward, not a dead end.
    await expect(page.locator("#retry-catalogue")).toBeVisible();

    // And the status strip shows which service is at fault.
    await expect(page.locator(".service").filter({ hasText: "Catalogue" }))
      .toContainText("TRANSIENT_FAILURE");
  });

  test("Scenario 5: retrying after the service comes back succeeds", async ({ page, request, baseURL }) => {
    services.stop(["product"]);
    await services.waitForDown(request, baseURL, "product");

    await page.goto("/");
    await expect(page.locator("#retry-catalogue")).toBeVisible();

    // Bring the service back while the page is still open.
    services.start(["product"]);
    await services.waitForHealthy(request, baseURL);

    await page.locator("#retry-catalogue").click();

    await expect(page.locator(".card")).toHaveCount(8);
  });

  test("Scenario 5: a product lookup reports the service, not a transport error", async ({ page, request, baseURL }) => {
    await page.goto("/");
    await expect(page.locator(".card").first()).toBeVisible();

    services.stop(["product"]);
    await services.waitForDown(request, baseURL, "product");

    await page.fill("#lookup-input", "P001");
    await page.press("#lookup-input", "Enter");

    const result = page.locator("#lookup-result .note");
    await expect(result).toContainText("The Product Service is unavailable right now");

    // Nothing about sockets, ports or dial errors.
    await expect(result).not.toContainText("connection refused");
    await expect(result).not.toContainText("transport");
  });

  test("Scenario 5: with the Order Service stopped, checkout fails cleanly", async ({ page, request, baseURL }) => {
    await page.goto("/");
    await signIn(page);

    await page.locator(".card").filter({ hasText: "Wireless Mouse" })
      .getByRole("button", { name: "Add to cart" }).click();

    services.stop(["order"]);
    await services.waitForDown(request, baseURL, "order");

    await page.locator("#cart-open").click();
    await page.locator("#checkout").click();

    // Checkout asks the Order Service for a price before it
    // offers to take payment, so a stopped Order Service is
    // caught there: the customer is told, and Pay is never
    // offered for an order that cannot be priced.
    //
    // A stopped dependency surfaces one of two ways depending
    // on whether gRPC's channel has already noticed the drop:
    // straight away as Unavailable, or as a timeout if the
    // call is still waiting to reconnect when the gateway's
    // budget runs out. Both are correct, and which one turns
    // up is a matter of timing, so the test accepts either and
    // checks what actually matters — that the shopper is told
    // clearly and nothing was charged.
    const result = page.locator("#checkout-summary .note");
    await expect(result).toContainText(/unavailable|took too long/);
    await expect(page.locator("#pay-now")).toBeDisabled();

    // The cart is not silently emptied for an order that was
    // never placed.
    await expect(page.locator("#cart-count")).toHaveText("1");
  });

  test("Scenario 5: with only the Inventory Service stopped, the shop still works", async ({ page, request, baseURL }) => {
    services.stop(["inventory"]);
    await services.waitForDown(request, baseURL, "inventory");

    await page.goto("/");

    // Losing stock levels degrades the storefront rather than
    // taking it down: products still list, marked unknown.
    await expect(page.locator(".card")).toHaveCount(8);
    await expect(page.locator(".card").first()).toContainText("Stock unknown");
  });

  // ========================================================
  // Scenario 6 — a dependency slower than the timeout
  // ========================================================

  test("Scenario 6: a slow Payment Service surfaces as a timeout message", async ({ page, request, baseURL }) => {
    // Restart Payment with its simulated 3-second gateway
    // delay. The Order Service allows it 1 second.
    services.start(["payment"], { PAYMENT_DELAY: "3s" });
    await services.waitForHealthy(request, baseURL);

    await page.goto("/");
    await signIn(page);

    await page.locator(".card").filter({ hasText: "Wireless Mouse" })
      .getByRole("button", { name: "Add to cart" }).click();
    await page.locator("#cart-open").click();
    await page.locator("#checkout").click();

    const started = Date.now();
    await page.locator("#pay-now").click();

    const result = page.locator("#checkout-result .note");
    await expect(result).toContainText("took too long to respond");
    await expect(result).toContainText("DeadlineExceeded");

    // The page must not sit there for the full three seconds
    // per attempt; the timeout is what makes it fail promptly.
    const elapsed = Date.now() - started;
    expect(elapsed, "the shopper waited too long for a failure").toBeLessThan(20_000);

    // No payment was taken, so the item is still in the cart.
    await expect(page.locator("#cart-count")).toHaveText("1");
  });

  test("Scenario 6: the timeout is recorded by the Order Service", async ({ page, request, baseURL }) => {
    services.truncateLog("order");
    services.start(["payment"], { PAYMENT_DELAY: "3s" });
    await services.waitForHealthy(request, baseURL);

    await page.goto("/");
    await signIn(page);

    await page.locator(".card").filter({ hasText: "Wireless Mouse" })
      .getByRole("button", { name: "Add to cart" }).click();
    await page.locator("#cart-open").click();
    await page.locator("#checkout").click();
    await page.locator("#pay-now").click();
    await expect(page.locator("#checkout-result .note")).toContainText("took too long");

    // The interceptor names the call it cut off and the budget
    // it exceeded.
    const log = services.log("order");
    expect(log).toContain("[timeout]");
    expect(log).toContain("ProcessPayment");
    expect(log).toContain("exceeded its 1s budget");
  });

  // ========================================================
  // Scenario 7 — retry behaviour
  // ========================================================

  test("Scenario 7: retries hide the flaky Inventory Service from the shopper", async ({ page, request, baseURL }) => {
    services.truncateLog("order");

    // Restart Inventory with its simulated transient failures:
    // two calls in every three fail with Unavailable.
    services.start(["inventory"], { INVENTORY_FLAKY: "true" });
    await services.waitForHealthy(request, baseURL);

    await page.goto("/");
    await signIn(page);

    await page.locator(".card").filter({ hasText: "Wireless Mouse" })
      .getByRole("button", { name: "Add to cart" }).click();
    await page.locator("#cart-open").click();
    await page.locator("#checkout").click();
    await page.locator("#pay-now").click();

    // The order goes through despite the failures underneath.
    await expect(page.locator("#checkout-dialog"))
      .toContainText("Payment successful");

    // The log shows the attempts the customer never saw.
    const log = services.log("order");
    expect(log).toContain("[retry]");
    expect(log).toContain("ReserveStock");
    expect(log).toMatch(/attempt \d\/3 failed \(Unavailable\), retrying/);
    expect(log).toMatch(/succeeded on attempt \d\/3/);
  });

  // ========================================================
  // Scenario 8 — circuit breaker behaviour
  // ========================================================

  test("Scenario 8: repeated failures open the circuit and stop the calls", async ({ page, request, baseURL }) => {
    services.truncateLog("order");

    await page.goto("/");
    await signIn(page);

    await page.locator(".card").filter({ hasText: "Wireless Mouse" })
      .getByRole("button", { name: "Add to cart" }).click();

    // Kill the Product Service. The Order Service's connection
    // to it is wrapped in a circuit breaker that trips after
    // three consecutive failures.
    services.stop(["product"]);
    await services.waitForDown(request, baseURL, "product");

    // Four attempts in a row, without closing the checkout in
    // between. They have to land inside the breaker's
    // five-second reset window, or it will have gone half-open
    // and let the fourth call through — which is correct
    // behaviour, but not what this test is measuring.
    //
    // Opening checkout is the first attempt: pricing needs the
    // product, so the Order Service calls Product through the
    // breaker. "Try again" makes the next three.
    await page.locator("#cart-open").click();
    await page.locator("#checkout").click();

    const result = page.locator("#checkout-summary .note");
    await expect(result).toContainText("unavailable");
    for (let attempt = 2; attempt <= 4; attempt++) {
      await page.locator("#quote-retry").click();
      await expect(result).toContainText("unavailable");
    }
    await expect(page.locator("#pay-now")).toBeDisabled();

    const log = services.log("order", 120);

    // The breaker tripped...
    expect(log).toContain("CLOSED -> OPEN");
    expect(log).toContain("3 consecutive failures");

    // ...and then started refusing calls without making them,
    // which is the whole point: a dependency known to be down
    // stops costing every request a failed round trip.
    expect(log).toContain("rejecting /product.ProductService/GetProduct without calling it");
  });

  test("Scenario 8: the circuit closes again once the service recovers", async ({ page, request, baseURL }) => {
    services.truncateLog("order");

    await page.goto("/");
    await signIn(page);
    await page.locator(".card").filter({ hasText: "Wireless Mouse" })
      .getByRole("button", { name: "Add to cart" }).click();

    services.stop(["product"]);
    await services.waitForDown(request, baseURL, "product");

    // Trip the breaker: each checkout tries to price the order,
    // which needs the product.
    for (let attempt = 1; attempt <= 3; attempt++) {
      await page.locator("#cart-open").click();
      await page.locator("#checkout").click();
      await expect(page.locator("#checkout-summary .note")).toContainText("unavailable");
      await page.locator("#checkout-close").click();
    }

    // Bring the Product Service back and wait out the
    // breaker's 5-second reset window.
    services.start(["product"]);
    await services.waitForHealthy(request, baseURL);
    await page.waitForTimeout(6_000);

    await page.locator("#cart-open").click();
    await page.locator("#checkout").click();
    await page.locator("#pay-now").click();

    // The trial call succeeds, so the breaker closes and the
    // order goes through.
    await expect(page.locator("#checkout-dialog")).toContainText("Payment successful");

    const log = services.log("order", 120);
    expect(log).toContain("OPEN -> HALF-OPEN");
    expect(log).toMatch(/-> CLOSED, call succeeded/);
  });
});
