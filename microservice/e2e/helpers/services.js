// Controls the microservices from inside a test.
//
// The services run under WSL; these tests run on Windows and
// reach them over localhost. Starting and stopping them
// therefore goes through `wsl`, calling the same scripts a
// person would use by hand — which is 
// what makes the failure
// scenarios in Part D genuine rather than simulated.

const { execFileSync } = require("child_process");

const DISTRO = process.env.WSL_DISTRO || "Ubuntu";
const PROJECT = process.env.PROJECT_DIR || "/home/fritzlee/lab2/example";

// The PATH a login shell would have. Passed explicitly because
// a non-interactive shell does not source the profile that
// puts Go on the path.
const SHELL_PATH = "/usr/local/go/bin:/home/fritzlee/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin";

/**
 * Runs a command inside the project directory in WSL and
 * returns its output.
 */
function wsl(command, env = {}) {
  const assignments = Object.entries({ PATH: SHELL_PATH, ...env })
    .map(([key, value]) => `${key}=${value}`);

  // execFileSync passes each argument through without a shell,
  // so nothing here needs quoting.
  //
  // wsl.exe translates the inherited Windows PATH into Linux
  // paths and warns about every entry it cannot map. Run from
  // the mapped network drive these tests live on, npm's
  // node_modules/.bin entries produce a wall of those warnings
  // around each call, so they are stripped before handing the
  // environment over.
  const cleanPath = (process.env.PATH || "")
    .split(";")
    .filter((entry) => entry && !/^[A-Z]:\\?$/i.test(entry) && !entry.includes("node_modules"))
    .join(";");

  return execFileSync(
    "wsl",
    ["-d", DISTRO, "--cd", PROJECT, "--", "env", ...assignments, "bash", "-c", command],
    {
      encoding: "utf8",
      timeout: 120_000,
      cwd: process.env.SystemDrive ? `${process.env.SystemDrive}\\` : "C:\\",
      env: { ...process.env, PATH: cleanPath },
    }
  );
}

/**
 * Starts one or more services. With no names, starts them all.
 * Extra environment variables are passed through, which is how
 * a test injects a fault — a slow payment gateway, say.
 */
function start(names = [], env = {}) {
  const list = names.length ? ` ${names.join(" ")}` : "";
  return wsl(`./scripts/start-services.sh${list}`, env);
}

/** Stops one or more services. With no names, stops them all. */
function stop(names = []) {
  const list = names.length ? ` ${names.join(" ")}` : "";
  return wsl(`./scripts/stop-services.sh${list}`);
}

/** Reads the tail of a service's log, for asserting on it. */
function log(name, lines = 60) {
  try {
    return wsl(`tail -n ${lines} .run/logs/${name}.log`);
  } catch {
    return "";
  }
}

/** Empties a service's log so a test can assert on fresh output. */
function truncateLog(name) {
  try {
    wsl(`: > .run/logs/${name}.log`);
  } catch {
    // A log that does not exist yet is already empty.
  }
}

/**
 * Restores the whole system to its normal demo state: every
 * service running, payments fast, inventory dependable.
 * Called before and after the scenarios that break things, so
 * each test starts from a known position.
 */
function resetSystem() {
  start([], { PAYMENT_DELAY: "0s", INVENTORY_FLAKY: "false" });
}

/**
 * Waits until the storefront reports every service ready, so a
 * test does not start before the gateway has reconnected.
 */
async function waitForHealthy(request, baseURL, { timeout = 30_000 } = {}) {
  const deadline = Date.now() + timeout;
  let last = null;

  while (Date.now() < deadline) {
    try {
      const response = await request.get(`${baseURL}/api/health`);
      if (response.ok()) {
        last = await response.json();
        if (Object.values(last).every((state) => state === "READY")) {
          return last;
        }
      }
    } catch {
      // The gateway may still be starting.
    }
    await new Promise((resolve) => setTimeout(resolve, 300));
  }

  throw new Error(`services did not all become ready: ${JSON.stringify(last)}`);
}

/**
 * Waits until a named service is reported as down, so a test
 * that stopped one does not race the gateway's next poll.
 */
async function waitForDown(request, baseURL, name, { timeout = 20_000 } = {}) {
  const deadline = Date.now() + timeout;

  while (Date.now() < deadline) {
    try {
      const response = await request.get(`${baseURL}/api/health`);
      if (response.ok()) {
        const health = await response.json();
        if (health[name] !== "READY") return health;
      }
    } catch {
      // Ignore; the gateway itself may be the one restarting.
    }
    await new Promise((resolve) => setTimeout(resolve, 300));
  }

  throw new Error(`${name} was still reported as ready after being stopped`);
}

module.exports = { wsl, start, stop, log, truncateLog, resetSystem, waitForHealthy, waitForDown };
