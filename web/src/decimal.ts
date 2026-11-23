const TICK_DP = 8;
const TICKS = 100_000_000n;
const MAX_TICK = 9_223_372_036_854_775_807n;
const LEDGER_DP = 18;
const LEDGER_UNITS = 10n ** BigInt(LEDGER_DP);

function units(text: string, dp: number): bigint {
  const negative = text.startsWith('-');
  const [whole, fraction = ''] = (negative ? text.slice(1) : text).split('.');
  const value = BigInt(`${whole}${fraction.padEnd(dp, '0').slice(0, dp)}`);
  return negative ? -value : value;
}

export function ticks(text: string) {
  return units(text, TICK_DP);
}

export function decimalText(value: bigint, dp: number): string {
  const magnitude = (value < 0n ? -value : value).toString().padStart(dp + 1, '0');
  const decimal = dp === 0 ? magnitude : `${magnitude.slice(0, -dp)}.${magnitude.slice(-dp)}`;
  return value < 0n ? `-${decimal}` : decimal;
}

export function validAmount(text: string, order = false): boolean {
  if (!/^(?:\d+(?:\.\d{0,8})?|\.\d{1,8})$/.test(text)) return false;
  if (text.split('.')[0].replace(/^0+/, '').length > 18) return false;
  const value = ticks(text);
  return value > 0n && (!order || value <= MAX_TICK);
}

export function notional(price: string, quantity: string): string {
  return decimalText(ticks(price) * ticks(quantity), TICK_DP * 2);
}

export function sizedQuantity(available: string, percent: number, qtyDp: number, price?: string): string {
  const numerator = units(available, LEDGER_DP) * BigInt(percent) * 10n ** BigInt(qtyDp);
  const denominator = LEDGER_UNITS * 100n;
  // Divide exactly once, so the quantity is floored without overspending by a tick.
  const quantity = price === undefined ? numerator / denominator : numerator * TICKS / (denominator * ticks(price));
  return decimalText(quantity, qtyDp);
}
