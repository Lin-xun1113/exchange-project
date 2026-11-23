import { useRef, useState } from 'react';
import type { Dispatch, FormEvent, SetStateAction } from 'react';
import type { Balance, Order, OrderInput, OrderType, Side } from './api';
import { notional, sizedQuantity, validAmount } from './decimal';
import { fmt } from './format';
import { ASSET_DP } from './markets';
import type { Market } from './markets';
import type { Session } from './session';
import { Tabs } from './Tabs';

export type OrderDraft = { side: Side; type: OrderType; price: string; quantity: string };
type Props = {
  market: Market;
  draft: OrderDraft;
  setDraft: Dispatch<SetStateAction<OrderDraft>>;
  session: Session | null;
  balances?: Balance[];
  onLogin: () => void;
  onSubmit: (input: OrderInput) => Promise<Order>;
};
type Feedback = { owner: Session; state: 'pending' } | { owner: Session; state: 'success' | 'error'; message: string };

const sides = [
  { value: 'buy', label: '买入', className: 'buy-tab' },
  { value: 'sell', label: '卖出', className: 'sell-tab' },
] as const;

export const ORDER_TYPES: Record<OrderType, string> = { limit: '限价', ioc: 'IOC', fok: 'FOK', market: '市价' };

export function OrderForm({ market, draft, setDraft, session, balances, onLogin, onSubmit }: Props) {
  const [attempted, setAttempted] = useState(false);
  const [feedback, setFeedback] = useState<Feedback>();
  const priceField = useRef<HTMLInputElement>(null);
  const quantityField = useRef<HTMLInputElement>(null);
  const visibleFeedback = feedback?.owner === session ? feedback : undefined;
  const pending = visibleFeedback?.state === 'pending';
  const { side, type } = draft;
  const buy = side === 'buy';
  const marketOrder = type === 'market';
  const price = marketOrder ? '' : draft.price.trim();
  const quantity = draft.quantity.trim();
  const priceValid = marketOrder || validAmount(price, true);
  const quantityValid = validAmount(quantity, true);
  const availableAsset = buy ? market.quote : market.base;
  const available = balances && (balances.find(({ asset }) => asset === availableAsset)?.available ?? '0');
  const total = !marketOrder && priceValid && quantityValid ? notional(price, quantity) : undefined;
  const invalidPrice = attempted && !priceValid;
  const invalidQuantity = attempted && !quantityValid;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!session) {
      onLogin();
      return;
    }
    if (pending) return;
    setAttempted(true);
    if (!priceValid || !quantityValid) {
      setFeedback(undefined);
      (priceValid ? quantityField : priceField).current?.focus();
      return;
    }

    setFeedback({ owner: session, state: 'pending' });
    try {
      const order = await onSubmit({ symbol: market.path, side, type, price, quantity });
      setFeedback({ owner: session, state: 'success', message: `已提交委托 ${order.order_id.slice(0, 8)}` });
      setAttempted(false);
      setDraft((current) => current === draft ? { ...current, quantity: '' } : current);
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') return;
      setFeedback({ owner: session, state: 'error', message: error instanceof Error ? error.message : '委托提交失败' });
    }
  }

  return (
    <section className="panel order-panel" aria-labelledby="order-heading">
      <div className="panel-head">
        <h2 id="order-heading">下单</h2>
        <span className="panel-status">{market.symbol}</span>
      </div>
      <div className="order-entry">
        <Tabs id="order-side" label="委托方向" items={sides} active={side} className="side-tabs" onChange={(next) => {
          setDraft((current) => ({ ...current, side: next, type: next === 'buy' && current.type === 'market' ? 'limit' : current.type }));
        }} />
        <div id="order-side-panel" role="tabpanel" aria-labelledby={`order-side-${side}`}>
          <form className="order-form" onSubmit={submit} noValidate>
            <label className="field">
              <span>委托类型</span>
              <select value={type} onChange={(event) => {
                const next = event.target.value as OrderType;
                setDraft((current) => ({ ...current, type: next }));
              }}>
                {Object.entries(ORDER_TYPES).map(([value, label]) => (
                  <option key={value} value={value} disabled={value === 'market' && buy}
                    title={value === 'market' && buy ? '市价单仅支持卖出' : undefined}>{label}</option>
                ))}
              </select>
            </label>
            {!marketOrder && (
              <div className="field">
                <label htmlFor="order-price">价格</label>
                <span className="input-unit">
                  <input id="order-price" ref={priceField} type="text" inputMode="decimal" autoComplete="off" required value={draft.price}
                    onChange={(event) => { const value = event.target.value; setDraft((current) => ({ ...current, price: value })); }}
                    aria-invalid={invalidPrice || undefined} aria-describedby={invalidPrice ? 'order-price-error' : undefined} />
                  <span>{market.quote}</span>
                </span>
                {invalidPrice && <span className="field-error" id="order-price-error">
                  {validAmount(price) ? '价格超出撮合范围' : '请输入大于 0、最多 8 位小数的价格'}
                </span>}
              </div>
            )}
            <div className="field">
              <label htmlFor="order-quantity">数量</label>
              <span className="input-unit">
                <input id="order-quantity" ref={quantityField} type="text" inputMode="decimal" autoComplete="off" required value={draft.quantity}
                  onChange={(event) => { const value = event.target.value; setDraft((current) => ({ ...current, quantity: value })); }}
                  aria-invalid={invalidQuantity || undefined} aria-describedby={invalidQuantity ? 'order-quantity-error' : undefined} />
                <span>{market.base}</span>
              </span>
              {invalidQuantity && <span className="field-error" id="order-quantity-error">
                {validAmount(quantity) ? '数量超出撮合范围' : '请输入大于 0、最多 8 位小数的数量'}
              </span>}
            </div>
            <div className="size-controls">
              <div className="order-available"><span>可用</span><span>{available === undefined ? '—' : `${fmt(available, ASSET_DP[availableAsset])} ${availableAsset}`}</span></div>
              <div className="size-chips" role="group" aria-label="按可用余额计算数量">
                {[25, 50, 75, 100].map((percent) => (
                  <button type="button" key={percent} disabled={available === undefined || (buy && !priceValid)}
                    aria-label={`使用 ${percent}% 可用余额`}
                    onClick={() => {
                      if (available === undefined || (buy && !priceValid)) return;
                      const value = sizedQuantity(available, percent, market.qtyDp, buy ? price : undefined);
                      setDraft((current) => ({ ...current, quantity: value }));
                    }}>{percent}%</button>
                ))}
              </div>
            </div>
            {!marketOrder && <div className="order-total"><span>合计</span><span title={total}>{total ? `${fmt(total, 2)} ${market.quote}` : '—'}</span></div>}
            <button type="submit" className={`button ${session ? `button-${side}` : 'button-accent'}`} disabled={pending}>
              {!session ? '登录后交易' : pending ? '正在提交' : `${buy ? '买入' : '卖出'} ${market.base}`}
            </button>
            {visibleFeedback?.state === 'success' && <p className="form-notice rise" role="status">{visibleFeedback.message}</p>}
            {visibleFeedback?.state === 'error' && <p className="form-notice fall" role="alert">{visibleFeedback.message}</p>}
          </form>
        </div>
      </div>
    </section>
  );
}
