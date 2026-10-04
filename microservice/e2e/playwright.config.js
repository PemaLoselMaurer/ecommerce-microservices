// @ts-check
const { defineConfig, devices } = require("@playwright/test");

/**
 * End-to-end configuration for the Druk Electronics storefront.
 *
 * These tests drive the real application in a real browser
 * against the real services. Several of them deliberately stop
 * a service or restart it with a fault injected, so they must
 * not run in parallel with each other — one worker, no
 * sharding.
 */
module.exports = defineConfig({
  testDir: "./tests",

  // Failure scenarios take a few seconds each: a stopped
  // service, a circuit breaker's reset window, a payment
  // gateway that answers slowly on purpose.
  timeout: 90_000,
  expect: { timeout: 15_000 },

  // Shared, mutable system under test — one test at a time.
  fullyParallel: false,
  workers: 1,

  // A flaky end-to-end test is a finding, not something to
  // paper over with a retry.
  retries: 0,

  reporter: [
    ["list"],
    ["html", { outputFolder: "report", open: "never" }],
    ["junit", { outputFile: "report/e2e-junit.xml" }],
  ],

  use: {
    baseURL: process.env.STOREFRONT_URL || "http://localhost:8081",

    // Evidence for the lab report: a trace and a screenshot
    // for anything that fails, and a video of every run.
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",

    actionTimeout: 15_000,
    navigationTimeout: 20_000,
  },

  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
