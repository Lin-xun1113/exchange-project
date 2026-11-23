import type { Asset, Interval, Market, MarketPath } from './markets';

export type Side = 'buy' | 'sell';
export type OrderType = 'limit' | 'ioc' | 'fok' | 'market';
export type OrderStatus = 'pending' | 'open' | 'partial_filled' | 'filled' | 'cancelled' | 'rejected';

type Envelope<T> = { code: number; message: string; data?: T };
type RequestOptions = {
  token?: string;
  body?: object;
  idempotent?: boolean;
  signal?: AbortSignal;
};

export class ApiError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
    this.name = 'ApiError';
  }
}

export async function request<T>(
  method: 'GET' | 'POST' | 'DELETE',
  path: string,
  { token, body, idempotent, signal }: RequestOptions = {},
): Promise<T> {
  const headers = new Headers({ Accept: 'application/json' });
  if (token) headers.set('Authorization', `Bearer ${token}`);
  if (body) headers.set('Content-Type', 'application/json');
  if (idempotent) headers.set('Idempotency-Key', crypto.randomUUID());

  let response: Response;
  try {
    response = await fetch(`/api/v1${path}`, {
      method,
      headers,
      body: body && JSON.stringify(body),
      signal,
    });
  } catch (error) {
    if (signal?.aborted) throw error;
    throw new ApiError(0, '网络错误，网关不可达');
  }

  let envelope: Envelope<T> | undefined;
  try {
    envelope = await response.json();
  } catch (error) {
    if (signal?.aborted) throw error;
  }
  if (!response.ok || envelope?.code !== 0 || envelope.data === undefined) {
    const message = typeof envelope?.message === 'string' && envelope.code !== 0 && envelope.message;
    throw new ApiError(response.status, message || `请求失败（${response.status}）`);
  }
  return envelope.data;
}

export type Level = { price: string; quantity: string; orders: number };
export type Book = { symbol: Market['symbol']; bids: Level[]; asks: Level[]; lsn: number };
export type Candle = { time: number; open: string; high: string; low: string; close: string; volume: string };
export type MarketTrade = {
  trade_id: string;
  price: string;
  quantity: string;
  taker_side: Side;
  created_at: string;
};
export type Ticker = {
  symbol: Market['symbol'];
  last_price: string;
  open_24h: string;
  high_24h: string;
  low_24h: string;
  volume_24h: string;
  quote_volume_24h: string;
  change_percent_24h: string;
  trade_count_24h: number;
};

export function orderbook(path: MarketPath, depth = 20, signal?: AbortSignal) {
  return request<Book>('GET', `/market/orderbook/${path}?depth=${depth}`, { signal });
}

export function marketTrades(path: MarketPath, limit = 40, signal?: AbortSignal) {
  return request<{ symbol: Market['symbol']; trades: MarketTrade[] }>(
    'GET', `/market/trades/${path}?limit=${limit}`, { signal },
  );
}

export function candles(path: MarketPath, interval: Interval, limit = 300, signal?: AbortSignal) {
  return request<{ symbol: Market['symbol']; interval: Interval; candles: Candle[] }>(
    'GET', `/market/candles/${path}?interval=${interval}&limit=${limit}`, { signal },
  );
}

export function tickers(paths: readonly MarketPath[] = [], signal?: AbortSignal) {
  const query = paths.length ? `?${new URLSearchParams({ symbols: paths.join(',') })}` : '';
  return request<Ticker[]>('GET', `/market/tickers${query}`, { signal });
}

export type Credentials = { username: string; password: string };
export type LoginResult = { token: string; expires_at: number; username: string; role: string };
export type Balance = { asset: string; available: string; frozen: string };
export type Order = {
  order_id: string;
  symbol: Market['symbol'];
  side: Side;
  type: OrderType;
  price: string;
  quantity: string;
  filled_quantity: string;
  status: OrderStatus;
  reject_reason: string;
  cancel_requested: boolean;
  created_at: string;
};
export type Trade = {
  trade_id: string;
  order_id: string;
  symbol: Market['symbol'];
  side: Side;
  price: string;
  quantity: string;
  maker: boolean;
  created_at: string;
};
export type OrderInput = {
  symbol: MarketPath;
  side: Side;
  type: OrderType;
  price: string;
  quantity: string;
};

export function register(body: Credentials & { email?: string }, signal?: AbortSignal) {
  return request<{ username: string }>('POST', '/auth/register', { body, signal });
}

export function login(body: Credentials, signal?: AbortSignal) {
  return request<LoginResult>('POST', '/auth/login', { body, signal });
}

export function balances(token: string, signal?: AbortSignal) {
  return request<Balance[]>('GET', '/account/balances', { token, signal });
}

export function deposit(token: string, body: { asset: Asset; amount: string }) {
  return request<Balance>('POST', '/account/deposit', { token, body, idempotent: true });
}

export function createOrder(token: string, body: OrderInput) {
  return request<{ order: Order; replayed: boolean }>('POST', '/orders', { token, body, idempotent: true });
}

export function cancelOrder(token: string, orderId: string) {
  return request<{ order: Order }>('DELETE', `/orders/${encodeURIComponent(orderId)}`, { token });
}

export function listOrders(path: MarketPath, token: string, status?: OrderStatus, signal?: AbortSignal) {
  const query = new URLSearchParams({ symbol: path, page_size: '50' });
  if (status) query.set('status', status);
  return request<{ orders: Order[]; next_page_token: string }>('GET', `/orders?${query}`, { token, signal });
}

export function listTrades(path: MarketPath, token: string, signal?: AbortSignal) {
  const query = new URLSearchParams({ symbol: path, page_size: '50' });
  return request<{ trades: Trade[]; next_page_token: string }>('GET', `/trades?${query}`, { token, signal });
}
