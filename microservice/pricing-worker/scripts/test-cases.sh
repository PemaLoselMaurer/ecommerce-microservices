#!/usr/bin/env bash
# Runs the Part B test cases against a deployed (or `wrangler dev`)
# calculate-order-price Worker and prints each request and response.
#
#   PRICING_API_TOKEN=... ./scripts/test-cases.sh https://calculate-order-price.<sub>.workers.dev
#
# The token is read from the environment, never from this file.
set -u

BASE="${1:?usage: $0 <worker base URL>}"
URL="${BASE%/}/price"
: "${PRICING_API_TOKEN:?set PRICING_API_TOKEN in the environment}"

bold=$'\e[1m'; dim=$'\e[2m'; green=$'\e[32m'; red=$'\e[31m'; reset=$'\e[0m'

run() {
  local name="$1" expect="$2" auth="$3" body="$4"
  printf '\n%s━━ %s%s\n' "$bold" "$name" "$reset"
  printf '%sPOST %s%s\n' "$dim" "$URL" "$reset"
  printf '%sbody: %s%s\n' "$dim" "$body" "$reset"
  local headers=(-H "Content-Type: application/json")
  [ -n "$auth" ] && headers+=(-H "Authorization: Bearer $auth")
  local out status
  out=$(curl -s -w $'\n%{http_code}' "${headers[@]}" -d "$body" "$URL")
  status="${out##*$'\n'}"
  out="${out%$'\n'*}"
  if [ "$status" = "$expect" ]; then mark="${green}PASS${reset}"; else mark="${red}FAIL (expected $expect)${reset}"; fi
  printf 'HTTP %s  %s\n' "$status" "$mark"
  if command -v jq >/dev/null; then echo "$out" | jq .
  elif command -v node >/dev/null; then echo "$out" | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{try{console.log(JSON.stringify(JSON.parse(s),null,2))}catch{console.log(s)}})'
  else echo "$out"; fi
}

run "1. Valid: 1 x Wireless Mouse (pays delivery)" 200 "$PRICING_API_TOKEN" \
  '{"product_id":"P003","category":"accessories","unit_price":1800,"quantity":1}'
run "2. Valid: 3 x Mechanical Keyboard (5% bulk discount, free delivery)" 200 "$PRICING_API_TOKEN" \
  '{"product_id":"P002","category":"accessories","unit_price":4500,"quantity":3}'
run "3. Valid: 5 x Monitor (10% bulk discount)" 200 "$PRICING_API_TOKEN" \
  '{"product_id":"P004","category":"displays","unit_price":25000,"quantity":5}'
run "4. Invalid: quantity is zero" 400 "$PRICING_API_TOKEN" \
  '{"product_id":"P002","category":"accessories","unit_price":4500,"quantity":0}'
run "5. Invalid: product_id missing, unit_price negative" 400 "$PRICING_API_TOKEN" \
  '{"category":"accessories","unit_price":-100,"quantity":1}'
run "6. Invalid: quantity sent as text" 400 "$PRICING_API_TOKEN" \
  '{"product_id":"P002","category":"accessories","unit_price":4500,"quantity":"three"}'
run "7. Cannot be completed: 20 x Laptop exceeds the order limit" 422 "$PRICING_API_TOKEN" \
  '{"product_id":"P001","category":"laptops","unit_price":75000,"quantity":20}'
run "8. Unauthorised: wrong API token" 401 "not-the-real-token" \
  '{"product_id":"P002","category":"accessories","unit_price":4500,"quantity":1}'
echo
