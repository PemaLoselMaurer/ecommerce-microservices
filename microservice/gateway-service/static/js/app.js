/* ==========================================================
   Druk Electronics — storefront behaviour

   Sections, in order:
     1. Helpers
     2. Product artwork
     3. Gateway client
     4. Messages and toasts
     5. Authentication
     6. Navigation
     7. Catalogue
     8. Product lookup and detail
     9. Cart and checkout
    10. Orders
    11. Account
    12. Service status
    13. Boot
   ========================================================== */
"use strict";

/* ==========================================================
   1. Helpers
   ========================================================== */
const $ = (id) => document.getElementById(id);
const query = (selector, root = document) => root.querySelector(selector);

const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (character) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[character]));

const money = (amount) => "Nu. " + Number(amount).toLocaleString("en-IN", {
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
});

const firstName = (name) => String(name || "").split(/\s+/)[0] || "there";

// discountOf works out the saving from the two prices the
// Product Service supplies. A product is only on offer when
// its list price is genuinely above what it now sells for.
function discountOf(product) {
  const list = Number(product.list_price || 0);
  const now = Number(product.price || 0);
  if (list <= now) return null;

  return {
    list,
    saved: list - now,
    percent: Math.round(((list - now) / list) * 100),
  };
}

// starsFor renders a five-star row, filling half a star when
// the rating falls between two whole values.
function starsFor(rating) {
  const value = Number(rating || 0);
  let stars = "";

  for (let position = 1; position <= 5; position++) {
    if (value >= position) stars += "★";
    else if (value >= position - 0.5) stars += "⯨";
    else stars += "☆";
  }
  return stars;
}

function ratingHTML(product) {
  if (!product.rating) return "";

  return `<div class="rating">
    <span class="stars" aria-hidden="true">${starsFor(product.rating)}</span>
    <span>${Number(product.rating).toFixed(1)}</span>
    <span class="count">(${Number(product.review_count || 0).toLocaleString()})</span>
  </div>`;
}

function pricingHTML(product) {
  const discount = discountOf(product);

  return `<div class="pricing">
    <span class="now">${money(product.price)}</span>
    ${discount ? `<span class="was">${money(discount.list)}</span>
                  <span class="save">Save ${discount.percent}%</span>` : ""}
  </div>`;
}

const initials = (name) => String(name || "?")
  .split(/\s+/).filter(Boolean).slice(0, 2)
  .map((word) => word[0].toUpperCase()).join("");

/* ==========================================================
   2. Product artwork

   Drawn inline as SVG rather than loaded as image files, so
   the storefront ships inside the gateway binary with nothing
   to fetch from a CDN. All eight share one muted palette, so
   the grid reads as a set rather than eight stock photos.
   ========================================================== */
const SHELL = "#c3c9d2";
const SHELL_DARK = "#98a0ad";
const SCREEN = "#dfe6f2";
const SCREEN_EDGE = "#b9c4d6";
const DETAIL = "#7c8695";
const ACCENT = "#1f5fd0";

const ART = {
  laptop: `<svg viewBox="0 0 240 180" fill="none" xmlns="http://www.w3.org/2000/svg">
    <rect x="48" y="34" width="144" height="92" rx="6" fill="${SHELL}"/>
    <rect x="56" y="42" width="128" height="76" rx="3" fill="${SCREEN}" stroke="${SCREEN_EDGE}"/>
    <path d="M68 106l24-26 17 18 14-14 21 22z" fill="${SCREEN_EDGE}"/>
    <circle cx="152" cy="60" r="7" fill="${SCREEN_EDGE}"/>
    <path d="M28 126h184l9 14a4 4 0 0 1-3.4 6H22.4a4 4 0 0 1-3.4-6z" fill="${SHELL_DARK}"/>
    <rect x="102" y="131" width="36" height="4" rx="2" fill="${DETAIL}"/>
  </svg>`,

  keyboard: `<svg viewBox="0 0 240 180" fill="none" xmlns="http://www.w3.org/2000/svg">
    <rect x="22" y="56" width="196" height="72" rx="8" fill="${SHELL}"/>
    <rect x="22" y="116" width="196" height="12" rx="6" fill="${SHELL_DARK}"/>
    <g fill="#eef1f6">
      <rect x="34" y="66" width="16" height="14" rx="3"/><rect x="54" y="66" width="16" height="14" rx="3"/>
      <rect x="74" y="66" width="16" height="14" rx="3"/><rect x="94" y="66" width="16" height="14" rx="3"/>
      <rect x="114" y="66" width="16" height="14" rx="3"/><rect x="134" y="66" width="16" height="14" rx="3"/>
      <rect x="154" y="66" width="16" height="14" rx="3"/><rect x="174" y="66" width="32" height="14" rx="3"/>
      <rect x="34" y="84" width="22" height="14" rx="3"/><rect x="60" y="84" width="16" height="14" rx="3"/>
      <rect x="80" y="84" width="16" height="14" rx="3"/><rect x="100" y="84" width="16" height="14" rx="3"/>
      <rect x="120" y="84" width="16" height="14" rx="3"/><rect x="140" y="84" width="16" height="14" rx="3"/>
      <rect x="160" y="84" width="16" height="14" rx="3"/><rect x="180" y="84" width="26" height="14" rx="3"/>
      <rect x="34" y="102" width="26" height="10" rx="3"/><rect x="180" y="102" width="26" height="10" rx="3"/>
    </g>
    <rect x="64" y="102" width="112" height="10" rx="3" fill="${SCREEN}"/>
    <rect x="86" y="121" width="68" height="3" rx="1.5" fill="${ACCENT}" opacity=".55"/>
  </svg>`,

  mouse: `<svg viewBox="0 0 240 180" fill="none" xmlns="http://www.w3.org/2000/svg">
    <path d="M120 26c28 0 47 21 47 49v32c0 25-21 43-47 43s-47-18-47-43V75c0-28 19-49 47-49z" fill="${SHELL}"/>
    <path d="M120 26c28 0 47 21 47 49v6H73v-6c0-28 19-49 47-49z" fill="${SCREEN}"/>
    <path d="M120 26v55" stroke="${SHELL_DARK}" stroke-width="2"/>
    <rect x="116" y="44" width="8" height="22" rx="4" fill="${ACCENT}" opacity=".55"/>
    <ellipse cx="120" cy="138" rx="24" ry="5" fill="${SHELL_DARK}" opacity=".28"/>
  </svg>`,

  monitor: `<svg viewBox="0 0 240 180" fill="none" xmlns="http://www.w3.org/2000/svg">
    <rect x="20" y="24" width="200" height="114" rx="7" fill="${SHELL}"/>
    <rect x="28" y="32" width="184" height="92" rx="3" fill="${SCREEN}" stroke="${SCREEN_EDGE}"/>
    <path d="M44 112l32-36 22 24 20-22 32 34z" fill="${SCREEN_EDGE}"/>
    <circle cx="170" cy="56" r="10" fill="${SCREEN_EDGE}"/>
    <rect x="102" y="138" width="36" height="16" fill="${SHELL_DARK}"/>
    <rect x="74" y="152" width="92" height="9" rx="4.5" fill="${SHELL_DARK}"/>
  </svg>`,

  headphones: `<svg viewBox="0 0 240 180" fill="none" xmlns="http://www.w3.org/2000/svg">
    <path d="M48 116V96a72 72 0 0 1 144 0v20" stroke="${SHELL}" stroke-width="16" stroke-linecap="round"/>
    <rect x="28" y="98" width="40" height="58" rx="15" fill="${SHELL_DARK}"/>
    <rect x="172" y="98" width="40" height="58" rx="15" fill="${SHELL_DARK}"/>
    <rect x="36" y="106" width="24" height="42" rx="11" fill="${SCREEN}"/>
    <rect x="180" y="106" width="24" height="42" rx="11" fill="${SCREEN}"/>
    <circle cx="48" cy="127" r="5" fill="${SCREEN_EDGE}"/>
    <circle cx="192" cy="127" r="5" fill="${SCREEN_EDGE}"/>
  </svg>`,

  phone: `<svg viewBox="0 0 240 180" fill="none" xmlns="http://www.w3.org/2000/svg">
    <rect x="82" y="12" width="76" height="156" rx="13" fill="${SHELL}"/>
    <rect x="89" y="20" width="62" height="140" rx="8" fill="${SCREEN}" stroke="${SCREEN_EDGE}"/>
    <rect x="108" y="24" width="24" height="5" rx="2.5" fill="${SHELL_DARK}"/>
    <path d="M97 128l20-24 15 16 13-14 15 22z" fill="${SCREEN_EDGE}"/>
    <rect x="106" y="150" width="28" height="3" rx="1.5" fill="${SHELL_DARK}"/>
    <circle cx="163" cy="40" r="6" fill="${DETAIL}"/><circle cx="163" cy="56" r="6" fill="${DETAIL}"/>
  </svg>`,

  tablet: `<svg viewBox="0 0 240 180" fill="none" xmlns="http://www.w3.org/2000/svg">
    <rect x="44" y="16" width="152" height="148" rx="11" fill="${SHELL}"/>
    <rect x="52" y="24" width="136" height="132" rx="6" fill="${SCREEN}" stroke="${SCREEN_EDGE}"/>
    <path d="M68 130l28-32 21 22 19-20 28 30z" fill="${SCREEN_EDGE}"/>
    <circle cx="150" cy="52" r="11" fill="${SCREEN_EDGE}"/>
    <rect x="204" y="44" width="8" height="46" rx="4" fill="${SHELL_DARK}"/>
  </svg>`,

  webcam: `<svg viewBox="0 0 240 180" fill="none" xmlns="http://www.w3.org/2000/svg">
    <rect x="62" y="128" width="116" height="14" rx="7" fill="${SHELL_DARK}"/>
    <rect x="108" y="112" width="24" height="18" fill="${SHELL}"/>
    <circle cx="120" cy="74" r="48" fill="${SHELL}"/>
    <circle cx="120" cy="74" r="34" fill="${SCREEN}" stroke="${SCREEN_EDGE}"/>
    <circle cx="120" cy="74" r="15" fill="${DETAIL}"/>
    <circle cx="111" cy="65" r="5" fill="#ffffff" opacity=".8"/>
    <circle cx="158" cy="48" r="4" fill="${ACCENT}"/>
  </svg>`,

  generic: `<svg viewBox="0 0 240 180" fill="none" xmlns="http://www.w3.org/2000/svg">
    <rect x="56" y="44" width="128" height="94" rx="9" fill="${SHELL}"/>
    <rect x="56" y="44" width="128" height="26" rx="9" fill="${SHELL_DARK}"/>
    <circle cx="120" cy="104" r="19" fill="${SCREEN}"/>
  </svg>`,
};

const ART_BY_ID = {
  P001: "laptop", P002: "keyboard", P003: "mouse", P004: "monitor",
  P005: "headphones", P006: "phone", P007: "tablet", P008: "webcam",
};

const ART_BY_CATEGORY = {
  laptops: "laptop", displays: "monitor", audio: "headphones",
  mobile: "phone", accessories: "keyboard",
};

function artwork(product) {
  const key = ART_BY_ID[product.product_id]
    || ART_BY_CATEGORY[(product.category || "").toLowerCase()]
    || "generic";
  return ART[key] || ART.generic;
}

/* ==========================================================
   3. Gateway client

   Every outcome — success, a service reporting an error, or
   the gateway itself being unreachable — is normalised into
   one shape, so each caller renders failures the same way.
   ========================================================== */
async function api(path, options) {
  try {
    // same-origin credentials keep the session cookie flowing
    // on every call without the page ever reading it.
    const response = await fetch(path, { credentials: "same-origin", ...(options || {}) });
    const body = await response.json().catch(() => null);

    if (!response.ok) {
      return {
        ok: false,
        status: response.status,
        text: (body && body.error) || `The request failed (HTTP ${response.status}).`,
        code: (body && body.code) || String(response.status),
      };
    }
    return { ok: true, status: response.status, body };
  } catch (err) {
    // fetch only rejects when the gateway itself cannot be
    // reached, so this is the whole-system-down case.
    return {
      ok: false,
      status: 0,
      text: "We could not reach the store. Please check your connection and try again.",
      code: "GATEWAY_UNREACHABLE",
    };
  }
}

const postJSON = (path, payload) => api(path, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(payload),
});

/* ==========================================================
   4. Messages and toasts
   ========================================================== */

// headingFor turns a status code into something a shopper can
// read, rather than showing them the raw gRPC name.
function headingFor(code) {
  switch (code) {
    case "NotFound":            return "Not found";
    case "InvalidArgument":
    case "EMPTY_INPUT":         return "Check your details";
    case "Unavailable":
    case "GATEWAY_UNREACHABLE": return "Service unavailable";
    case "DeadlineExceeded":    return "Timed out";
    case "FailedPrecondition":  return "Could not complete";
    case "ResourceExhausted":   return "Not enough stock";
    case "Unauthenticated":
    case "UNAUTHENTICATED":     return "Sign in required";
    case "AlreadyExists":       return "Already registered";
    default:                    return "Something went wrong";
  }
}

// Codes describing the request rather than a broken system are
// shown as advice, not as failures.
const SOFT_CODES = new Set([
  "NotFound", "InvalidArgument", "ResourceExhausted", "Unauthenticated",
  "UNAUTHENTICATED", "AlreadyExists", "EMPTY_INPUT", "WEAK_PASSWORD",
]);

const kindFor = (code) => (SOFT_CODES.has(code) ? "warn" : "error");

function note(kind, text, code) {
  return `<div class="note ${kind}">
    <span class="body">${esc(text)}${code ? `<code>${esc(code)}</code>` : ""}</span>
  </div>`;
}

function toast(kind, heading, text, code) {
  const node = document.createElement("div");
  node.className = "toast " + kind;
  node.innerHTML = `<b>${esc(heading)}</b><p>${esc(text)}</p>`;
  if (code) node.title = code;

  $("toasts").appendChild(node);

  setTimeout(() => {
    node.style.transition = "opacity .25s ease, transform .25s ease";
    node.style.opacity = "0";
    node.style.transform = "translateY(6px)";
    setTimeout(() => node.remove(), 260);
  }, 5000);
}

const failToast = (result) =>
  toast(kindFor(result.code), headingFor(result.code), result.text, result.code);

/* ==========================================================
   5. Authentication

   The browser never holds a customer ID. Signing in sets an
   HttpOnly session cookie the page cannot read, and every
   request touching personal data is resolved server-side from
   that session. `me` below is only what the UI displays.
   ========================================================== */
let me = null;
let pendingCheckout = false;   // was the shopper on the way to checkout?

function renderAccountButton() {
  const slot = $("account-slot");

  if (!me) {
    slot.innerHTML = `<button id="signin-button">Sign in</button>`;
    $("signin-button").addEventListener("click", () => openAuth("login"));
    return;
  }

  slot.innerHTML = `
    <button id="account-button" aria-haspopup="true" aria-expanded="false">
      <span class="avatar">${esc(initials(me.name))}</span>
      <span class="who">${esc(firstName(me.name))}</span>
    </button>
    <div class="menu" id="account-menu" hidden>
      <div class="head">
        <div class="n">${esc(me.name)}</div>
        <div class="e">${esc(me.email)}</div>
      </div>
      <button data-go="account">Account details</button>
      <button data-go="orders">Your orders</button>
      <button id="signout-button">Sign out</button>
    </div>`;

  const menu = $("account-menu");
  const button = $("account-button");

  button.addEventListener("click", (event) => {
    event.stopPropagation();
    menu.hidden = !menu.hidden;
    button.setAttribute("aria-expanded", String(!menu.hidden));
  });

  menu.querySelectorAll("[data-go]").forEach((item) =>
    item.addEventListener("click", () => { menu.hidden = true; show(item.dataset.go); }));

  $("signout-button").addEventListener("click", () => { menu.hidden = true; signOut(); });

  document.addEventListener("click", () => {
    if (!menu.hidden) {
      menu.hidden = true;
      button.setAttribute("aria-expanded", "false");
    }
  });
}

// setIdentity updates every part of the page that depends on
// who is signed in.
function setIdentity(customer) {
  me = customer;
  renderAccountButton();
  renderCart();
  if (!$("view-orders").hidden) renderOrders();
  if (!$("view-account").hidden) renderAccountView();
}

/* ---------- the sign-in dialog ---------- */
function openAuth(tab) {
  selectTab(tab || "login");
  $("login-error").innerHTML = "";
  $("register-error").innerHTML = "";
  $("auth-dialog").classList.add("open");
  $("scrim").classList.add("open");
  setTimeout(() => $(tab === "register" ? "register-name" : "login-email").focus(), 50);
}

function closeAuth() {
  $("auth-dialog").classList.remove("open");
  if (!cartOpen) $("scrim").classList.remove("open");
}

function selectTab(tab) {
  const isLogin = tab === "login";

  $("tab-login").setAttribute("aria-selected", String(isLogin));
  $("tab-register").setAttribute("aria-selected", String(!isLogin));
  $("login-form").hidden = !isLogin;
  $("register-form").hidden = isLogin;
  $("demo-note").hidden = !isLogin;

  $("auth-title").textContent = isLogin ? "Sign in" : "Create your account";
  $("auth-lead").textContent = isLogin
    ? "Sign in to place orders and track them."
    : "One account for orders, delivery details and history.";
}

$("tab-login").addEventListener("click", () => selectTab("login"));
$("tab-register").addEventListener("click", () => selectTab("register"));
$("auth-close").addEventListener("click", closeAuth);

$("fill-demo").addEventListener("click", () => {
  $("login-email").value = "alice@example.com";
  $("login-password").value = "alice1234";
});

$("login-form").addEventListener("submit", async (event) => {
  event.preventDefault();

  const email = $("login-email").value.trim();
  const password = $("login-password").value;
  const slot = $("login-error");
  const button = query("button[type=submit]", $("login-form"));

  if (email === "" || password === "") {
    slot.innerHTML = note("warn", "Please enter both your email and your password.", "EMPTY_INPUT");
    return;
  }

  button.disabled = true;
  button.textContent = "Signing in…";
  slot.innerHTML = "";

  const result = await postJSON("/api/auth/login", { email, password });

  button.disabled = false;
  button.textContent = "Sign in";

  if (!result.ok) {
    slot.innerHTML = note(kindFor(result.code), result.text, result.code);
    return;
  }

  setIdentity(result.body);
  closeAuth();
  $("login-password").value = "";
  toast("ok", `Welcome back, ${firstName(result.body.name)}`, "You are signed in.");
  if (pendingCheckout) { pendingCheckout = false; openCart(); }
});

$("register-form").addEventListener("submit", async (event) => {
  event.preventDefault();

  const slot = $("register-error");
  const button = query("button[type=submit]", $("register-form"));

  const payload = {
    name: $("register-name").value.trim(),
    email: $("register-email").value.trim(),
    address: $("register-address").value.trim(),
    password: $("register-password").value,
  };

  if (!payload.name || !payload.email || !payload.password) {
    slot.innerHTML = note("warn", "Name, email and password are all required.", "EMPTY_INPUT");
    return;
  }
  if (payload.password.length < 8) {
    slot.innerHTML = note("warn", "Your password must be at least 8 characters.", "WEAK_PASSWORD");
    return;
  }

  button.disabled = true;
  button.textContent = "Creating account…";
  slot.innerHTML = "";

  const result = await postJSON("/api/auth/register", payload);

  button.disabled = false;
  button.textContent = "Create account";

  if (!result.ok) {
    slot.innerHTML = note(kindFor(result.code), result.text, result.code);
    return;
  }

  setIdentity(result.body);
  closeAuth();
  $("register-form").reset();
  toast("ok", `Welcome, ${firstName(result.body.name)}`, `Your account is ${result.body.customer_id}.`);
  if (pendingCheckout) { pendingCheckout = false; openCart(); }
});

async function signOut() {
  await api("/api/auth/logout", { method: "POST" });
  setIdentity(null);
  show("shop");
  toast("info", "Signed out", "You can keep browsing without an account.");
}

// restoreSession asks the gateway who the session cookie
// belongs to, so a refresh does not sign the shopper out.
async function restoreSession() {
  const result = await api("/api/auth/me");
  setIdentity(result.ok ? result.body : null);
}

// signInState is what a signed-out shopper sees on a page that
// needs an account.
function signInState(what) {
  return `<div class="panel"><div class="state">
    <svg class="glyph" width="34" height="34" viewBox="0 0 24 24" fill="none"
         stroke="currentColor" stroke-width="1.5">
      <rect x="4" y="10" width="16" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/>
    </svg>
    <h3>Sign in to see ${esc(what)}</h3>
    <p>Your orders and delivery details are tied to your account.</p>
    <button class="btn btn-primary" data-signin>Sign in or create an account</button>
  </div></div>`;
}

function wireSignIn(root) {
  root.querySelectorAll("[data-signin]").forEach((button) =>
    button.addEventListener("click", () => openAuth("login")));
}

/* ==========================================================
   6. Navigation
   ========================================================== */
function show(view) {
  for (const name of ["shop", "orders", "account"]) {
    $("view-" + name).hidden = name !== view;
  }

  document.querySelectorAll(".navbar button[data-view]").forEach((button) => {
    if (button.dataset.view === view) button.setAttribute("aria-current", "page");
    else button.removeAttribute("aria-current");
  });

  if (view === "orders") renderOrders();
  if (view === "account") renderAccountView();

  window.scrollTo({ top: 0, behavior: "smooth" });
}

document.querySelectorAll(".navbar button[data-view]").forEach((button) =>
  button.addEventListener("click", () => show(button.dataset.view)));

$("hero-orders").addEventListener("click", () => show("orders"));
$("hero-browse").addEventListener("click", () =>
  $("product-grid").scrollIntoView({ behavior: "smooth", block: "start" }));

/* ==========================================================
   7. Catalogue
   ========================================================== */
let catalogue = [];
let activeCategory = "";
let nameFilter = "";

// Orders above this qualify for free delivery, matching the
// promise made in the banner and the footer.
// Mirrors the pricing function's free-delivery threshold, for the
// "Free delivery" hint on product cards only. What a customer is
// actually charged always comes from the pricing function.
const FREE_DELIVERY_THRESHOLD = 5000;

const CATEGORIES = [
  ["", "All"],
  ["laptops", "Laptops"],
  ["displays", "Displays"],
  ["audio", "Audio"],
  ["mobile", "Mobile"],
  ["accessories", "Accessories"],
];

function renderChips() {
  $("category-chips").innerHTML = CATEGORIES.map(([value, label]) =>
    `<button class="chip" data-category="${esc(value)}" aria-pressed="${value === activeCategory}">${esc(label)}</button>`
  ).join("");

  $("category-chips").querySelectorAll("button").forEach((chip) =>
    chip.addEventListener("click", () => {
      activeCategory = chip.dataset.category;
      renderChips();
      loadCatalogue();
    }));
}

// availability turns a stock level into the tag shown on a
// card, and decides whether the product can be bought at all.
function availability(product) {
  if (product.stock === undefined || product.stock === null) {
    return { kind: "warn", label: "Stock unknown", buyable: true };
  }
  if (product.stock <= 0) return { kind: "bad", label: "Out of stock", buyable: false };
  if (product.stock < 5) return { kind: "warn", label: `Only ${product.stock} left`, buyable: true };
  return { kind: "ok", label: "In stock", buyable: true };
}

const skeletons = (count) => Array.from({ length: count }, () =>
  `<div class="skeleton"><div class="thumb"></div><div class="bar short"></div>
   <div class="bar"></div><div class="bar short"></div></div>`).join("");

function renderGrid() {
  const grid = $("product-grid");
  const needle = nameFilter.trim().toLowerCase();

  const items = needle
    ? catalogue.filter((product) =>
        product.name.toLowerCase().includes(needle) ||
        product.product_id.toLowerCase().includes(needle))
    : catalogue;

  $("catalogue-note").textContent =
    items.length === 1 ? "1 product" : `${items.length} products, priced and stocked live`;

  if (!items.length) {
    grid.innerHTML = `<div class="state" style="grid-column:1/-1">
      <h3>No matching products</h3>
      <p>Try a different search or category.</p>
    </div>`;
    return;
  }

  grid.innerHTML = items.map((product) => {
    const stock = availability(product);
    const discount = discountOf(product);
    const freeDelivery = product.price >= FREE_DELIVERY_THRESHOLD;

    return `<article class="card">
      <div class="thumb">
        <div class="flags">
          ${product.badge ? `<span class="flag-badge ${discount ? "deal" : ""}">${esc(product.badge)}</span>` : ""}
          ${discount ? `<span class="flag-off">${discount.percent}% OFF</span>` : ""}
        </div>
        <span class="tag ${stock.kind}">${esc(stock.label)}</span>
        ${artwork(product)}
      </div>
      <div class="body">
        <div class="cat">${esc(product.category || "general")}</div>
        <h3>${esc(product.name)}</h3>
        <div class="sku">${esc(product.product_id)}</div>
        ${ratingHTML(product)}
        ${pricingHTML(product)}
        ${freeDelivery ? `<div class="delivery">Free delivery</div>` : ""}
        <div class="actions">
          <button class="btn btn-outline" data-detail="${esc(product.product_id)}">Details</button>
          <button class="btn btn-primary" data-add="${esc(product.product_id)}" ${stock.buyable ? "" : "disabled"}>
            ${stock.buyable ? "Add to cart" : "Sold out"}
          </button>
        </div>
      </div>
    </article>`;
  }).join("");

  grid.querySelectorAll("[data-add]").forEach((button) =>
    button.addEventListener("click", () => addToCart(button.dataset.add)));
  grid.querySelectorAll("[data-detail]").forEach((button) =>
    button.addEventListener("click", () => openProduct(button.dataset.detail)));
}

async function loadCatalogue() {
  const grid = $("product-grid");
  grid.innerHTML = skeletons(8);
  $("catalogue-note").textContent = "Loading the catalogue…";

  const filter = activeCategory ? `?category=${encodeURIComponent(activeCategory)}` : "";
  const result = await api("/api/products" + filter);

  if (!result.ok) {
    catalogue = [];
    $("catalogue-note").textContent = "The catalogue could not be loaded.";
    grid.innerHTML = `<div style="grid-column:1/-1;padding:22px">
      ${note(kindFor(result.code), result.text, result.code)}
      <button class="btn btn-primary" id="retry-catalogue" style="margin-top:14px">Try again</button>
    </div>`;
    $("retry-catalogue").addEventListener("click", loadCatalogue);
    return;
  }

  catalogue = result.body;
  renderGrid();
}

$("name-filter").addEventListener("input", (event) => {
  nameFilter = event.target.value;
  if (catalogue.length) renderGrid();
});

/* ==========================================================
   8. Product lookup and detail

   The header search is the explicit "enter an identifier,
   submit, see the details or an error" path.
   ========================================================== */
$("lookup-form").addEventListener("submit", async (event) => {
  event.preventDefault();

  const id = $("lookup-input").value.trim();
  const slot = $("lookup-result");

  show("shop");

  if (id === "") {
    slot.innerHTML = note("warn", "Please enter a product ID before searching, for example P001.", "EMPTY_INPUT");
    toast("warn", "Check your details", "Please enter a product ID before searching.");
    return;
  }

  slot.innerHTML = note("info", `Looking up ${id}…`);

  const result = await api("/api/products/" + encodeURIComponent(id));

  if (!result.ok) {
    slot.innerHTML = note(kindFor(result.code), result.text, result.code);
    failToast(result);
    return;
  }

  slot.innerHTML = "";
  renderProductDialog(result.body);
  openProductDialog();
});

function renderProductDialog(product) {
  const stock = availability(product);
  const discount = discountOf(product);
  const stockLine = product.stock === undefined || product.stock === null
    ? (product.stock_error || "Stock level unavailable")
    : `${product.stock} available`;

  $("product-sheet").innerHTML = `
    <div class="product-dialog">
      <div class="figure">${artwork(product)}</div>
      <div class="detail">
        <div style="display:flex;justify-content:space-between;align-items:flex-start;gap:12px">
          <div class="cat" style="font-size:11px;font-weight:600;letter-spacing:.07em;
               text-transform:uppercase;color:var(--muted)">${esc(product.category || "general")}</div>
          <button class="icon-button" id="product-close" aria-label="Close">×</button>
        </div>

        <h2>${esc(product.name)}</h2>
        ${ratingHTML(product)}
        <p class="desc" style="margin-top:10px">${esc(product.description || "No description available for this product.")}</p>
        ${pricingHTML(product)}
        ${discount ? `<p style="font-size:13px;color:var(--ok);font-weight:600;margin:-8px 0 14px">
             You save ${money(discount.saved)}</p>` : ""}

        <div class="rows" style="margin-bottom:16px">
          <div class="row"><span>Product ID</span><span>${esc(product.product_id)}</span></div>
          <div class="row"><span>Category</span><span>${esc(product.category || "—")}</span></div>
          <div class="row"><span>Availability</span><span>${esc(stockLine)}</span></div>
          <div class="row"><span>Delivery</span><span>${product.price >= FREE_DELIVERY_THRESHOLD
            ? "Free" : "Standard rate"}</span></div>
        </div>

        ${product.stock_error ? note("warn", product.stock_error, "stock unavailable") : ""}

        <div style="display:flex;gap:8px;margin-top:16px">
          <button class="btn btn-primary" id="product-add" style="flex:1" ${stock.buyable ? "" : "disabled"}>
            ${stock.buyable ? "Add to cart" : "Out of stock"}
          </button>
          <button class="btn btn-outline" id="product-dismiss">Close</button>
        </div>
      </div>
    </div>`;

  $("product-close").addEventListener("click", closeProductDialog);
  $("product-dismiss").addEventListener("click", closeProductDialog);

  if (stock.buyable) {
    $("product-add").addEventListener("click", () => {
      addToCart(product.product_id, product);
      closeProductDialog();
    });
  }
}

async function openProduct(id) {
  const known = catalogue.find((product) => product.product_id === id);
  if (known) {
    renderProductDialog(known);
    openProductDialog();
    return;
  }

  const result = await api("/api/products/" + encodeURIComponent(id));
  if (!result.ok) { failToast(result); return; }

  renderProductDialog(result.body);
  openProductDialog();
}

function openProductDialog() {
  $("product-dialog").classList.add("open");
  $("scrim").classList.add("open");
}

function closeProductDialog() {
  $("product-dialog").classList.remove("open");
  if (!cartOpen) $("scrim").classList.remove("open");
}

/* ==========================================================
   9. Cart and checkout

   The cart is a client-side staging area held in
   localStorage; nothing is committed until checkout calls the
   Order Service.
   ========================================================== */
let cart = [];
try { cart = JSON.parse(localStorage.getItem("cart") || "[]"); } catch (err) { cart = []; }

function saveCart() {
  try { localStorage.setItem("cart", JSON.stringify(cart)); } catch (err) { /* private mode */ }
}

function addToCart(id, known) {
  const product = known || catalogue.find((item) => item.product_id === id);
  if (!product) {
    toast("error", "Something went wrong", "That product is no longer in the catalogue.");
    return;
  }

  const line = cart.find((item) => item.product_id === id);
  if (line) {
    line.quantity += 1;
  } else {
    cart.push({
      product_id: product.product_id,
      name: product.name,
      price: product.price,
      category: product.category,
      quantity: 1,
    });
  }

  saveCart();
  renderCart();
  toast("ok", "Added to cart", `${product.name} — ${money(product.price)}`);
}

function changeQuantity(id, delta) {
  const line = cart.find((item) => item.product_id === id);
  if (!line) return;

  line.quantity += delta;
  if (line.quantity <= 0) cart = cart.filter((item) => item.product_id !== id);

  saveCart();
  renderCart();
}

function removeLine(id) {
  cart = cart.filter((item) => item.product_id !== id);
  saveCart();
  renderCart();
}

const cartTotal = () => cart.reduce((sum, line) => sum + line.price * line.quantity, 0);

function renderCart() {
  const count = cart.reduce((total, line) => total + line.quantity, 0);
  const badge = $("cart-count");
  badge.textContent = count;
  badge.hidden = count === 0;

  if (!cart.length) {
    $("cart-body").innerHTML = `<div class="state">
      <h3>Your cart is empty</h3>
      <p>Products you add will appear here.</p>
    </div>`;
    $("cart-foot").innerHTML = `<button class="btn btn-primary btn-block" disabled>Checkout</button>`;
    return;
  }

  $("cart-body").innerHTML = cart.map((line) => `
    <div class="line-item">
      <div class="pic">${artwork(line)}</div>
      <div class="detail">
        <div class="n">${esc(line.name)}</div>
        <div class="s">${esc(line.product_id)} · ${money(line.price)} each</div>
        <div class="p">${money(line.price * line.quantity)}</div>
        <div class="stepper">
          <button data-decrease="${esc(line.product_id)}" aria-label="Decrease quantity">−</button>
          <span>${line.quantity}</span>
          <button data-increase="${esc(line.product_id)}" aria-label="Increase quantity">+</button>
        </div>
        <button class="remove" data-remove="${esc(line.product_id)}">Remove</button>
      </div>
    </div>`).join("");

  $("cart-body").querySelectorAll("[data-increase]").forEach((button) =>
    button.addEventListener("click", () => changeQuantity(button.dataset.increase, 1)));
  $("cart-body").querySelectorAll("[data-decrease]").forEach((button) =>
    button.addEventListener("click", () => changeQuantity(button.dataset.decrease, -1)));
  $("cart-body").querySelectorAll("[data-remove]").forEach((button) =>
    button.addEventListener("click", () => removeLine(button.dataset.remove)));

  // Who is buying comes from the session, so checkout asks for
  // nothing but confirmation — and offers a sign-in when there
  // is no session yet.
  $("cart-foot").innerHTML = `
    <div class="total"><span>Subtotal</span><strong>${money(cartTotal())}</strong></div>
    <p style="font-size:12px;color:var(--muted);margin:-4px 0 12px">
      Bulk discounts and delivery are calculated at checkout.
    </p>
    ${me
      ? `<p style="font-size:13px;color:var(--muted);margin-bottom:12px">
           Delivering to <strong style="color:var(--ink);font-weight:500">${esc(me.name)}</strong>
         </p>
         <button class="btn btn-primary btn-block btn-lg" id="checkout">Proceed to checkout</button>`
      : `<p style="font-size:13px;color:var(--muted);margin-bottom:12px">
           Sign in to complete your order.
         </p>
         <button class="btn btn-primary btn-block btn-lg" id="checkout-signin">Sign in to checkout</button>`}`;

  if (me) {
    $("checkout").addEventListener("click", openCheckout);
  } else {
    $("checkout-signin").addEventListener("click", () => {
      pendingCheckout = true;
      closeCart();
      openAuth("login");
    });
  }
}

let cartOpen = false;

function openCart() {
  cartOpen = true;
  $("cart-drawer").classList.add("open");
  $("scrim").classList.add("open");
}

function closeCart() {
  cartOpen = false;
  $("cart-drawer").classList.remove("open");
  $("scrim").classList.remove("open");
}

$("cart-open").addEventListener("click", openCart);
$("cart-close").addEventListener("click", closeCart);
$("scrim").addEventListener("click", () => {
  closeCart(); closeProductDialog(); closeAuth(); closeCheckout();
});

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") { closeCart(); closeProductDialog(); closeAuth(); closeCheckout(); }
});

/* ---------- checkout and payment ----------

   Checkout is its own step rather than a button on the cart,
   so the customer sees what they are paying, who it is being
   delivered to and how it is being settled before anything is
   charged. Pressing Pay calls the Order Service, which in turn
   charges the Payment Service; the payment reference that
   comes back is shown on the receipt.
   ------------------------------------------------------- */
const PAYMENT_METHODS = [
  ["card", "Debit or credit card", "Settled through our payment gateway"],
  ["wallet", "Mobile wallet", "Settled through our payment gateway"],
  ["cod", "Cash on delivery", "Pay the courier when your order arrives"],
];

let paymentMethod = "card";

function openCheckout() {
  if (!me) {
    pendingCheckout = true;
    closeCart();
    openAuth("login");
    return;
  }
  if (!cart.length) return;

  closeCart();
  renderCheckout();
  $("checkout-dialog").classList.add("open");
  $("scrim").classList.add("open");
}

function closeCheckout() {
  $("checkout-dialog").classList.remove("open");
  if (!cartOpen) $("scrim").classList.remove("open");
}

function renderCheckout() {
  $("checkout-sheet").innerHTML = `
    <div class="head">
      <h2>Checkout</h2>
      <button class="icon-button" id="checkout-close" aria-label="Close">×</button>
    </div>

    <div class="section">
      <h3>Order summary</h3>
      <div id="checkout-summary"></div>
    </div>

    <div class="section">
      <h3>Delivering to</h3>
      <div class="deliver">
        <span class="avatar" style="background:var(--surface-3);color:var(--ink-2)">${esc(initials(me.name))}</span>
        <div>
          <div class="who">${esc(me.name)}</div>
          <div class="addr">${esc(me.address || "No delivery address on file")}</div>
          <div class="addr">${esc(me.email)}</div>
        </div>
      </div>
    </div>

    <div class="section">
      <h3>Payment method</h3>
      <div class="methods">
        ${PAYMENT_METHODS.map(([value, title, subtitle]) => `
          <label class="method">
            <input type="radio" name="payment-method" value="${esc(value)}"
                   ${value === paymentMethod ? "checked" : ""}>
            <span class="label"><span class="t">${esc(title)}</span><span class="s">${esc(subtitle)}</span></span>
          </label>`).join("")}
      </div>
    </div>

    <div class="foot">
      <button class="btn btn-primary btn-block btn-lg" id="pay-now" disabled>Calculating your price…</button>
      <div id="checkout-result" style="margin-top:12px"></div>
      <p class="disclaimer">Demonstration checkout — no card details are collected and no money changes hands.</p>
    </div>`;

  $("checkout-close").addEventListener("click", closeCheckout);
  $("pay-now").addEventListener("click", placeOrder);

  $("checkout-sheet").querySelectorAll("input[name=payment-method]").forEach((radio) =>
    radio.addEventListener("change", () => { paymentMethod = radio.value; }));

  loadQuotes();
}

/* ---------- pricing ----------

   The price the customer pays is not worked out in the page.
   Each cart line is quoted by the Order Service, which takes
   the unit price from the Product Service and sends it to the
   calculate-order-price serverless function for bulk discounts
   and delivery. Placing the order runs the same function, so
   the amount on the Pay button is the amount charged.
   ------------------------------------------------------- */

// quoteRun discards answers to a quote request that has since
// been superseded, e.g. when checkout is closed and reopened.
let quoteRun = 0;

async function loadQuotes() {
  const run = ++quoteRun;
  const slot = $("checkout-summary");
  const button = $("pay-now");
  if (!slot || !button) return;

  button.disabled = true;
  button.textContent = "Calculating your price…";
  slot.innerHTML = note("info", "Calculating discounts and delivery…");

  const results = await Promise.all(cart.map((line) =>
    postJSON("/api/orders/quote", { product_id: line.product_id, quantity: line.quantity })));

  if (run !== quoteRun || !$("checkout-summary")) return;

  const failed = results.find((result) => !result.ok);
  if (failed) {
    slot.innerHTML = note(kindFor(failed.code), `We couldn't price your order. ${failed.text}`, failed.code)
      + `<button class="btn btn-outline btn-block" id="quote-retry" style="margin-top:10px">Try again</button>`;
    $("quote-retry").addEventListener("click", loadQuotes);
    button.textContent = "Price unavailable";
    failToast(failed);
    return;
  }

  const quotes = results.map((result) => result.body);
  const total = quotes.reduce((sum, quote) => sum + quote.total, 0);
  const saved = quotes.reduce((sum, quote) => sum + quote.discount, 0);

  slot.innerHTML = `
    ${quotes.map(quoteLines).join("")}
    <div class="summary-line grand">
      <span>Total to pay</span><span>${money(total)}</span>
    </div>
    ${saved > 0 ? `<div class="summary-line saving"><span>You save</span><span>${money(saved)}</span></div>` : ""}
    <p class="priced-by">Priced by the pricing service · ${esc(quotes[0].pricing_version)}.
      Each item ships as its own order.</p>`;

  button.disabled = false;
  button.textContent = `Pay ${money(total)} and place order`;
}

function quoteLines(quote) {
  return `
    <div class="quote">
      <div class="summary-line">
        <span>${esc(quote.product_name || quote.product_id)} <span class="qty">× ${quote.quantity}</span></span>
        <span>${money(quote.subtotal)}</span>
      </div>
      ${quote.discount > 0 ? `
      <div class="summary-line minor saving">
        <span>Bulk discount (${Math.round(quote.discount_rate * 100)}%)</span>
        <span>− ${money(quote.discount)}</span>
      </div>` : ""}
      <div class="summary-line minor">
        <span>Delivery</span>
        <span>${quote.delivery_fee > 0 ? money(quote.delivery_fee) : "Free"}</span>
      </div>
    </div>`;
}

// breakdownRows renders the pricing function's breakdown on a
// placed order, for the receipt and the order history.
function breakdownRows(order) {
  if (!order.subtotal) return "";
  return `
    <div class="row"><span>Subtotal</span><span>${money(order.subtotal)}</span></div>
    ${order.discount > 0 ? `<div class="row saving"><span>Bulk discount</span><span>− ${money(order.discount)}</span></div>` : ""}
    <div class="row"><span>Delivery</span><span>${order.delivery_fee > 0 ? money(order.delivery_fee) : "Free"}</span></div>`;
}

// CreateOrder takes a single product, so a multi-line cart is
// checked out as one order per line. Each line reports its own
// outcome, which is what happens when one dependency fails
// partway through a basket.
async function placeOrder() {
  const slot = $("checkout-result");
  const button = $("pay-now");

  button.disabled = true;
  button.textContent = "Processing payment…";
  slot.innerHTML = note("info", "Charging your payment and placing the order…");

  const outcomes = [];

  for (const line of [...cart]) {
    const result = await postJSON("/api/orders", {
      product_id: line.product_id,
      quantity: line.quantity,
    });

    outcomes.push({ line, result });
    if (result.ok) removeLine(line.product_id);

    // A session that expired mid-checkout should stop the run
    // rather than fail every remaining line the same way.
    if (result.status === 401) { setIdentity(null); break; }
  }

  const placed = outcomes.filter((outcome) => outcome.result.ok);
  const failed = outcomes.filter((outcome) => !outcome.result.ok);

  renderCart();

  if (placed.length && !failed.length) {
    renderReceipt(placed.map((outcome) => outcome.result.body));
    return;
  }

  // Something went wrong, so the customer stays on checkout
  // and sees exactly which line failed and why. What is left in
  // the cart is priced again, so the Pay button stays truthful.
  if (cart.length) {
    loadQuotes();
  } else {
    button.textContent = "Nothing left to pay for";
  }

  const messages = [];

  if (placed.length) {
    const ids = placed.map((outcome) => outcome.result.body.order_id).join(", ");
    messages.push(note("ok", `Paid and placed: ${ids}. The remaining items are still in your cart.`));
  }

  for (const outcome of failed) {
    messages.push(note(kindFor(outcome.result.code),
      `${outcome.line.name}: ${outcome.result.text}`, outcome.result.code));
    failToast(outcome.result);
  }

  slot.innerHTML = messages.join("");
}

// renderReceipt replaces the checkout form with confirmation,
// including the payment reference the Payment Service issued.
function renderReceipt(orders) {
  const total = orders.reduce((sum, order) => sum + order.total_price, 0);

  $("checkout-sheet").innerHTML = `
    <div class="receipt">
      <div class="tick">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4">
          <path d="M5 12.5l4.5 4.5L19 7.5"/>
        </svg>
      </div>
      <h2>Payment successful</h2>
      <p class="sub">${orders.length === 1 ? "Your order is confirmed." : `All ${orders.length} orders are confirmed.`}</p>

      ${orders.map((order) => `
        <div class="rows" style="margin-bottom:12px">
          <div class="row"><span>Order</span><span>${esc(order.order_id)}</span></div>
          <div class="row"><span>Item</span><span>${esc(order.product_name || order.product_id)} × ${esc(order.quantity)}</span></div>
          ${breakdownRows(order)}
          <div class="row"><span>Paid</span><span>${money(order.total_price)}</span></div>
          <div class="row"><span>Payment reference</span><span>${esc(order.payment_id || "—")}</span></div>
          <div class="row"><span>Status</span><span>${esc(order.status)}</span></div>
        </div>`).join("")}

      <div class="rows" style="margin-bottom:18px">
        <div class="row"><span>Total charged</span><span>${money(total)}</span></div>
      </div>

      <div style="display:flex;gap:8px">
        <button class="btn btn-primary" style="flex:1" id="receipt-orders">View my orders</button>
        <button class="btn btn-outline" id="receipt-close">Keep shopping</button>
      </div>
    </div>`;

  $("receipt-orders").addEventListener("click", () => { closeCheckout(); show("orders"); });
  $("receipt-close").addEventListener("click", closeCheckout);

  toast("ok", "Payment successful", `Order ${orders[0].order_id} confirmed.`);
}

/* ==========================================================
   10. Orders
   ========================================================== */
async function renderOrders() {
  const slot = $("orders-result");
  const subtitle = $("orders-note");

  if (!me) {
    subtitle.textContent = "Sign in to see your order history.";
    slot.innerHTML = signInState("your orders");
    wireSignIn(slot);
    return;
  }

  subtitle.textContent = `Orders placed on account ${me.customer_id}.`;
  slot.innerHTML = note("info", "Fetching your orders…");

  const result = await api("/api/orders");

  if (!result.ok) {
    // A session can expire between page load and this call.
    if (result.status === 401) { setIdentity(null); renderOrders(); return; }
    slot.innerHTML = note(kindFor(result.code), result.text, result.code);
    failToast(result);
    return;
  }

  const orders = result.body;

  if (!orders.length) {
    slot.innerHTML = `<div class="panel"><div class="state">
      <h3>No orders yet</h3>
      <p>Once you place an order it will appear here.</p>
      <button class="btn btn-primary" id="orders-shop">Start shopping</button>
    </div></div>`;
    $("orders-shop").addEventListener("click", () => show("shop"));
    return;
  }

  slot.innerHTML = orders.map((order) => `
    <div class="order">
      <div class="head">
        <strong>${esc(order.order_id)}</strong>
        <span class="status-pill">${esc(order.status)}</span>
      </div>
      <div class="body">
        <div class="row"><span>Item</span><span>${esc(order.product_name || order.product_id)}</span></div>
        <div class="row"><span>Product ID</span><span>${esc(order.product_id)}</span></div>
        <div class="row"><span>Quantity</span><span>${esc(order.quantity)}</span></div>
        ${breakdownRows(order)}
        <div class="row"><span>Total paid</span><span>${money(order.total_price)}</span></div>
        <div class="row"><span>Payment reference</span><span>${esc(order.payment_id || "—")}</span></div>
      </div>
    </div>`).join("");
}

$("orders-refresh").addEventListener("click", renderOrders);

/* ==========================================================
   11. Account
   ========================================================== */
async function renderAccountView() {
  const slot = $("account-result");

  if (!me) {
    slot.innerHTML = signInState("your account");
    wireSignIn(slot);
    return;
  }

  slot.innerHTML = note("info", "Loading your details…");

  // Read the record back from the Customer Service rather than
  // trusting the copy held in the page.
  const result = await api("/api/auth/me");

  if (!result.ok) {
    if (result.status === 401) { setIdentity(null); renderAccountView(); return; }
    slot.innerHTML = note(kindFor(result.code), result.text, result.code);
    failToast(result);
    return;
  }

  const customer = result.body;

  slot.innerHTML = `<div class="panel">
    <div style="display:flex;align-items:center;gap:13px;margin-bottom:18px">
      <span class="avatar" style="width:42px;height:42px;font-size:15px">${esc(initials(customer.name))}</span>
      <div>
        <h3>${esc(customer.name)}</h3>
        <p class="sub">Account ${esc(customer.customer_id)}</p>
      </div>
    </div>

    <div class="rows">
      <div class="row"><span>Customer ID</span><span>${esc(customer.customer_id)}</span></div>
      <div class="row"><span>Name</span><span>${esc(customer.name)}</span></div>
      <div class="row"><span>Email</span><span>${esc(customer.email)}</span></div>
      <div class="row"><span>Delivery address</span><span>${esc(customer.address || "Not set")}</span></div>
    </div>

    <div style="display:flex;gap:8px;margin-top:18px">
      <button class="btn btn-outline" id="account-orders">View your orders</button>
      <button class="btn btn-outline" id="account-signout">Sign out</button>
    </div>
  </div>`;

  $("account-orders").addEventListener("click", () => show("orders"));
  $("account-signout").addEventListener("click", signOut);
}

/* ==========================================================
   12. Service status
   ========================================================== */
const SERVICES = [
  ["product", "Catalogue"],
  ["customer", "Accounts"],
  ["inventory", "Inventory"],
  ["payment", "Payments"],
  ["notification", "Notifications"],
  ["order", "Orders"],
];

async function refreshStatus() {
  const result = await api("/api/health");
  const strip = $("status-strip");

  if (!result.ok) {
    strip.innerHTML = `<span class="service"><span class="dot down"></span>Gateway
      <span class="state">UNREACHABLE</span></span>`;
    return;
  }

  strip.innerHTML = SERVICES.map(([key, label]) => {
    const state = result.body[key] || "UNKNOWN";
    // Only READY means the service actually answered;
    // CONNECTING and IDLE are still undecided.
    const dot = state === "READY" ? "up"
      : (state === "TRANSIENT_FAILURE" || state === "SHUTDOWN") ? "down"
      : "unknown";
    return `<span class="service"><span class="dot ${dot}"></span>${esc(label)}
      <span class="state">${esc(state)}</span></span>`;
  }).join("");
}

/* ==========================================================
   13. Boot
   ========================================================== */
$("hero-figure").innerHTML = ART.laptop;

renderChips();
renderAccountButton();
renderCart();
loadCatalogue();
restoreSession();
refreshStatus();
setInterval(refreshStatus, 5000);
