import { useState } from 'react';
import { AccountActivity } from './AccountActivity';
import {
  balances, cancelOrder, candles, createOrder, deposit, listOrders, listTrades, marketTrades, orderbook, tickers,
} from './api';
import type { OrderInput, Side } from './api';
import { AuthDialog } from './AuthDialog';
import { Balances } from './Balances';
import { DepositDialog } from './DepositDialog';
import { Header } from './Header';
import { MARKETS } from './markets';
import type { Asset, Interval, Market, MarketPath } from './markets';
import { MarketTrades } from './MarketTrades';
import { OrderBook } from './OrderBook';
import { OrderForm } from './OrderForm';
import type { OrderDraft } from './OrderForm';
import { PriceChart } from './PriceChart';
import { useSession } from './session';
import type { Session } from './session';
import { TickerStrip } from './TickerStrip';
import { usePoll } from './usePoll';

type Dialog = { kind: 'auth' } | { kind: 'deposit'; owner: Session };

export function App() {
  const [market, setMarket] = useState<Market>(MARKETS[0]);
  const [interval, setInterval] = useState<Interval>('15m');
  const [draft, setDraft] = useState<OrderDraft>({ side: 'buy', type: 'limit', price: '', quantity: '' });
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const { session, expiryNotice, signIn, signOut, runPrivate } = useSession();
  const { path } = market;

  const book = usePoll((signal) => orderbook(path, 20, signal), 1_000, [path]);
  const tape = usePoll((signal) => marketTrades(path, 40, signal), 2_000, [path]);
  const tickerPoll = usePoll((signal) => tickers(undefined, signal), 5_000, []);
  const chart = usePoll((signal) => candles(path, interval, 300, signal), 15_000, [path, interval]);
  const funds = usePoll(session && ((signal) => runPrivate(session, (token) => balances(token, signal))), 3_000, [session]);
  const orders = usePoll(session && ((signal) => runPrivate(session, (token) => listOrders(path, token, undefined, signal))), 3_000, [path, session]);
  const fills = usePoll(session && ((signal) => runPrivate(session, (token) => listTrades(path, token, signal))), 3_000, [path, session]);
  const ticker = tickerPoll.data?.find(({ symbol }) => symbol === market.symbol);

  function selectMarket(next: MarketPath) {
    const selected = MARKETS.find((entry) => entry.path === next);
    if (!selected || selected === market) return;
    setMarket(selected);
    setDraft((current) => ({ ...current, price: '', quantity: '' }));
  }

  function pickPrice(price: string, side: Side) {
    setDraft((current) => ({ ...current, price, side, type: current.type === 'market' ? 'limit' : current.type }));
  }

  function showLogin() {
    setDialog({ kind: 'auth' });
  }

  function showDeposit() {
    if (session) setDialog({ kind: 'deposit', owner: session });
  }

  function refreshAfterWrite() {
    funds.refresh();
    orders.refresh();
    fills.refresh();
    book.refresh();
  }

  async function submitOrder(input: OrderInput) {
    if (!session) throw new DOMException('请先登录', 'AbortError');
    const { order } = await runPrivate(session, (token) => createOrder(token, input));
    refreshAfterWrite();
    return order;
  }

  async function submitCancel(id: string) {
    if (!session) throw new DOMException('请先登录', 'AbortError');
    await runPrivate(session, (token) => cancelOrder(token, id));
    refreshAfterWrite();
  }

  async function submitDeposit(asset: Asset, amount: string) {
    if (dialog?.kind !== 'deposit') throw new DOMException('充值已关闭', 'AbortError');
    await runPrivate(dialog.owner, (token) => deposit(token, { asset, amount }));
    refreshAfterWrite();
    setDialog((current) => current === dialog ? null : current);
  }

  return (
    <>
      <Header path={path} tickers={tickerPoll.data ?? []} session={session} onMarket={selectMarket}
        onLogin={showLogin} onDeposit={showDeposit} onLogout={() => { signOut(); setDialog(null); }} />
      <main>
        {expiryNotice && <p className="session-notice" role="status">登录已失效，请重新登录</p>}
        <div id="market-panel" role="tabpanel" aria-labelledby={`market-${path}`}>
          <TickerStrip market={market} ticker={ticker} error={tickerPoll.error} />
          <div className="terminal-grid">
            <PriceChart market={market} interval={interval} onInterval={setInterval} candles={chart.data?.candles} error={chart.error} />
            <OrderBook key={`book:${path}`} market={market} book={book.data} last={ticker?.last_price} error={book.error} onPickPrice={pickPrice} />

            <OrderForm key={`entry:${path}`} market={market} draft={draft} setDraft={setDraft} session={session} balances={funds.data}
              onLogin={showLogin} onSubmit={submitOrder} />
            <MarketTrades market={market} trades={tape.data?.trades} error={tape.error} />
            <Balances signedIn={session !== null} balances={funds.data} error={funds.error} onDeposit={showDeposit} />
            <AccountActivity key={`${path}:${session !== null}`} market={market} signedIn={session !== null}
              orders={orders.data?.orders} trades={fills.data?.trades} orderError={orders.error} tradeError={fills.error}
              onLogin={showLogin} onCancel={submitCancel} />
          </div>
        </div>
      </main>
      {dialog?.kind === 'auth' && <AuthDialog onClose={() => setDialog(null)} onAuthenticated={(next) => {
        signIn(next);
        setDialog(null);
        requestAnimationFrame(() => {
          if (document.activeElement === document.body) document.getElementById('account-control')?.focus();
        });
      }} />}
      {dialog?.kind === 'deposit' && dialog.owner === session && <DepositDialog onClose={() => setDialog(null)} onDeposit={submitDeposit} />}
    </>
  );
}
