export const MARKETS = [
  { symbol: 'BTC/USDT', path: 'BTC-USDT', base: 'BTC', quote: 'USDT', priceDp: 2, qtyDp: 4 },
  { symbol: 'ETH/USDT', path: 'ETH-USDT', base: 'ETH', quote: 'USDT', priceDp: 2, qtyDp: 3 },
] as const;

export type Market = (typeof MARKETS)[number];
export type MarketPath = Market['path'];
export type Asset = Market['base'] | Market['quote'];

export const ASSETS = ['USDT', 'BTC', 'ETH'] as const satisfies readonly Asset[];
export const ASSET_DP: Record<Asset, number> = { USDT: 2, BTC: 4, ETH: 3 };
export const INTERVALS = ['1m', '5m', '15m', '1h', '4h', '1d'] as const;
export type Interval = (typeof INTERVALS)[number];
