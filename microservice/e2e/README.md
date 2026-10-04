# End-to-end tests

These drive the storefront in a real Chromium browser against
the real, running microservices. Nothing is mocked: the
failure scenarios genuinely stop a service or restart it with
a fault injected, then check what a customer sees on screen.

## Running them

The services must be running first:

```bash
cd ~/lab2/example
PAYMENT_DELAY=0s INVENTORY_FLAKY=false ./scripts/start-services.sh
```

Then, from Windows (Node and the browser live there):

```powershell
cd Z:\home\fritzlee\lab2\example\e2e     # or wherever the repo is mapped
npx playwright test                       # all 31 tests
npx playwright test --headed              # watch it happen in a visible browser
npx playwright test --ui                  # step through interactively
npx playwright show-report report         # open the HTML report
```

`Z:` is a drive letter mapped to the WSL filesystem
(`net use Z: \\wsl.localhost\Ubuntu`), because npm does not
accept a UNC path as its working directory.

## What is where

| File | Scenarios |
|---|---|
| `tests/01-browsing.spec.js` | 1–4: existing record, different records, missing record, invalid input |
| `tests/02-checkout.spec.js` | Sign-in, cart, payment, order history, account isolation |
| `tests/03-failures.spec.js` | 5–8: service unavailable, timeout, retry, circuit breaker |
| `helpers/services.js` | Starts and stops services through the WSL scripts, and reads their logs |

## Reports

Every run writes:

- `report/index.html` — the browsable report, with a trace,
  screenshot and video for anything that failed
- `report/e2e-junit.xml` — JUnit XML, for CI or an IDE test
  viewer

## Notes

- The tests run one at a time (`workers: 1`). They share one
  system and several of them break it on purpose, so they
  cannot run in parallel.
- `retries: 0` on purpose: a flaky end-to-end test is a
  finding, not something to paper over.
- Each failure test restores the system afterwards, so the
  demo is left working.
