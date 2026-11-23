#!/usr/bin/env bash
set -euo pipefail

url=${GATEWAY_URL:-http://localhost:8080}
api=${url%/}/api/v1
password='ExchangeDemo1!'
suffix="$(date +%s)_$$"
a_token= b_token= http_status= body=

fail() {
    printf 'FAIL %s\n  expected: %s\n  actual:   %s\n' "$1" "$2" "$3" >&2
    exit 1
}

request() {
    local method=$1 endpoint=$2 token=${3:-} key=${4:-} data=${5:-} timeout=${6:-3} raw
    local args=(-sS --max-time "$timeout" -X "$method" -w $'\n%{http_code}')
    if [[ -n $token ]]; then args+=(-H "Authorization: Bearer $token"); fi
    if [[ -n $key ]]; then args+=(-H "Idempotency-Key: $key"); fi
    if [[ -n $data ]]; then args+=(-H 'Content-Type: application/json' --data "$data"); fi
    if raw=$(curl "${args[@]}" "$endpoint"); then
        http_status=${raw##*$'\n'}
        body=${raw%$'\n'*}
    else
        http_status=000
        body='gateway request failed'
    fi
}

expect() {
    local label=$1 code=$2 predicate=${3:-true}
    shift 3 || return 1
    if [[ $http_status != "$code" ]] || ! jq -e "$@" "$predicate" <<<"$body" >/dev/null 2>&1; then
        fail "$label" "HTTP $code, jq $predicate $*" "HTTP $http_status $body"
    fi
}

wait_for() {
    local label=$1 code=$2 predicate=$3 method=$4 endpoint=$5 token=$6
    shift 6
    local deadline=$((SECONDS + 10)) remaining
    while :; do
        remaining=$((deadline - SECONDS))
        if (( remaining <= 0 )); then
            fail "$label" "within 10s: HTTP $code, jq $predicate $*" "HTTP $http_status $body"
        fi
        request "$method" "$endpoint" "$token" '' '' "$((remaining < 3 ? remaining : 3))"
        if (( SECONDS < deadline )) && [[ $http_status == "$code" ]] &&
            jq -e "$@" "$predicate" <<<"$body" >/dev/null 2>&1 && (( SECONDS < deadline )); then
            return
        fi
        if (( SECONDS >= deadline )); then
            fail "$label" "within 10s: HTTP $code, jq $predicate $*" "HTTP $http_status $body"
        fi
        sleep 0.2
    done
}

account() {
    local username=$1 credentials
    credentials=$(jq -cn --arg username "$username" --arg password "$password" '{username:$username,password:$password}')
    request POST "$api/auth/register" '' '' "$credentials"
    expect 'register' 200 '.data.user_id > 0'
    request POST "$api/auth/login" '' '' "$credentials"
    expect 'login' 200 '.data.token | type == "string" and length > 0'
}

wait_balance() {
    local label=$1 token=$2 asset=$3 avail=$4 frozen=$5
    wait_for "$label" 200 \
        'any(.data[]; .asset == $asset and (.available|tonumber) == $avail and (.frozen|tonumber) == $frozen)' \
        GET "$api/account/balances" "$token" --arg asset "$asset" --argjson avail "$avail" --argjson frozen "$frozen"
}

account "a_$suffix"
a_token=$(jq -r '.data.token' <<<"$body")
account "b_$suffix"
b_token=$(jq -r '.data.token' <<<"$body")
request POST "$api/account/deposit" "$a_token" initial '{"asset":"USDT","amount":"10000"}'
expect 'deposit A' 200 '.data.asset == "USDT" and (.data.available|tonumber) == 10000'
request POST "$api/account/deposit" "$b_token" initial '{"asset":"BTC","amount":"1"}'
expect 'deposit B' 200 '.data.asset == "BTC" and (.data.available|tonumber) == 1'

sell='{"symbol":"BTC-USDT","side":"sell","type":"limit","price":"20000","quantity":"0.5"}'
request POST "$api/orders" "$b_token" b-1 "$sell"
expect 'submit maker' 200 '.data.order.order_id | type == "string" and length > 0'
b_order=$(jq -r '.data.order.order_id' <<<"$body")
wait_for 'open maker' 200 '.data.order.status == "open"' GET "$api/orders/$b_order" "$b_token"

buy='{"symbol":"BTC/USDT","side":"buy","type":"limit","price":"20100","quantity":"0.3"}'
request POST "$api/orders" "$a_token" a-1 "$buy"
expect 'submit taker' 200 '.data.order.order_id | type == "string" and length > 0'
a_order=$(jq -r '.data.order.order_id' <<<"$body")
wait_for 'filled taker' 200 '.data.order.status == "filled" and (.data.order.filled_quantity|tonumber) == 0.3' GET "$api/orders/$a_order" "$a_token"
request POST "$api/orders" "$a_token" a-1 "$buy"
expect 'replayed taker' 200 '.data.replayed == true and .data.order.order_id == $id' --arg id "$a_order"

wait_balance 'A quote after settlement' "$a_token" USDT 4000 0
wait_balance 'A base after settlement' "$a_token" BTC 0.3 0
wait_balance 'B base after settlement' "$b_token" BTC 0.5 0.2
wait_balance 'B quote after settlement' "$b_token" USDT 6000 0

if ! docker compose restart matching-svc; then
    fail 'restart matching' 'successful restart' 'docker compose restart failed'
fi
wait_for 'readiness after restart' 200 '.status == "ready"' GET "${url%/}/readyz" ''
request GET "$api/market/orderbook/BTC-USDT"
expect 'restored book' 200 '.data.bids == [] and (.data.asks | length == 1 and (.[0].price|tonumber) == 20000 and (.[0].quantity|tonumber) == 0.2 and .[0].orders == 1)'

request DELETE "$api/orders/$b_order" "$b_token"
expect 'cancel maker' 200 '.data.order.cancel_requested == true'
wait_for 'cancelled maker' 200 '.data.order.status == "cancelled" and (.data.order.filled_quantity|tonumber) == 0.3' GET "$api/orders/$b_order" "$b_token"
wait_balance 'B base after release' "$b_token" BTC 0.7 0
request GET "$api/market/orderbook/BTC-USDT"
expect 'empty book after cancel' 200 '.data.bids == [] and .data.asks == []'

fok='{"symbol":"BTC-USDT","side":"buy","type":"fok","price":"20000","quantity":"0.1"}'
request POST "$api/orders" "$a_token" a-2 "$fok"
expect 'submit FOK' 200 '.data.order.order_id | type == "string" and length > 0'
fok_order=$(jq -r '.data.order.order_id' <<<"$body")
wait_for 'rejected FOK' 200 '.data.order.status == "rejected" and .data.order.reject_reason == "fok_unfilled"' GET "$api/orders/$fok_order" "$a_token"
wait_balance 'A quote after FOK release' "$a_token" USDT 4000 0

request GET "$api/account/balances" "$a_token"
expect 'read A balances' 200 '.data | type == "array"'
a_balances=$body
request GET "$api/account/balances" "$b_token"
expect 'read B balances' 200 '.data | type == "array"'
body=$(jq -cs 'map(.data) | add' <<<"$a_balances"$'\n'"$body")
expect 'balance conservation' 200 'def total($asset): map(select(.asset == $asset) | (.available|tonumber) + (.frozen|tonumber)) | add; total("USDT") == 10000 and total("BTC") == 1'

trap 'docker compose start order-svc >&2 || true' EXIT
if ! docker compose stop order-svc; then
    fail 'stop order service' 'successful stop' 'docker compose stop failed'
fi
wait_for 'unready order service' 503 '.status == "not_ready" and .failed == ["order-svc"]' GET "${url%/}/readyz" ''
if ! docker compose start order-svc; then
    fail 'restore order service' 'successful start' 'docker compose start failed'
fi
trap - EXIT
wait_for 'readiness after order restart' 200 '.status == "ready"' GET "${url%/}/readyz" ''
printf 'E2E PASS\n'
