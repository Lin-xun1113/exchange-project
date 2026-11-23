import { useState } from 'react';
import type { Order, OrderStatus, Trade } from './api';
import { fmt, fmtTime } from './format';
import type { Market } from './markets';
import { ORDER_TYPES } from './OrderForm';
import { Tabs } from './Tabs';

const activityTabs = [
  { value: 'current', label: '当前委托' },
  { value: 'history', label: '历史委托' },
  { value: 'trades', label: '我的成交' },
] as const;
const activeStatuses: Record<OrderStatus, boolean> = {
  pending: true, open: true, partial_filled: true, filled: false, cancelled: false, rejected: false,
};
const statusNames: Record<OrderStatus, string> = {
  pending: '处理中', open: '挂单中', partial_filled: '部分成交', filled: '已成交', cancelled: '已撤销', rejected: '已拒绝',
};

type Props = {
  market: Market;
  signedIn: boolean;
  orders?: Order[];
  trades?: Trade[];
  orderError: Error | null;
  tradeError: Error | null;
  onLogin: () => void;
  onCancel: (orderId: string) => Promise<void>;
};

export function AccountActivity({ market, signedIn, orders, trades, orderError, tradeError, onLogin, onCancel }: Props) {
  const [tab, setTab] = useState<(typeof activityTabs)[number]['value']>('current');
  const [cancelSent, setCancelSent] = useState<ReadonlySet<string>>(new Set());
  const [cancelError, setCancelError] = useState('');
  const showTrades = tab === 'trades';
  const current = tab === 'current';
  const filtered = orders?.filter(({ status }) => activeStatuses[status] === current);
  const error = showTrades ? tradeError : orderError;
  const labelId = `activity-${tab}`;

  async function cancel(orderId: string) {
    if (cancelSent.has(orderId)) return;
    setCancelError('');
    setCancelSent((previous) => new Set(previous).add(orderId));
    try {
      await onCancel(orderId);
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') return;
      setCancelSent((previous) => {
        const next = new Set(previous);
        next.delete(orderId);
        return next;
      });
      setCancelError(error instanceof Error ? error.message : '撤单失败');
    }
  }

  return (
    <section className="panel activity-panel" aria-label="账户活动">
      <div className="panel-head activity-head">
        <Tabs id="activity" label="账户活动" items={activityTabs} active={tab} onChange={setTab} className="activity-tabs" />
      </div>
      <div id="activity-panel" role="tabpanel" aria-labelledby={labelId}>
        {!signedIn ? (
          <div className="activity-signin">
            <p>登录后查看委托和成交</p>
            <button type="button" className="button button-outline" onClick={onLogin}>登录</button>
          </div>
        ) : <>
          {error && <p className="panel-message fall" role="status">更新失败：{error.message}</p>}
          {current && cancelError && <p className="panel-message fall" role="alert">{cancelError}</p>}
          {showTrades ? (trades?.length ? (
            <div className="activity-scroll" role="region" aria-label="成交表" tabIndex={0}>
              <table className="data-table activity-table" aria-labelledby={labelId}>
                <thead><tr>
                  <th scope="col">时间</th><th scope="col">方向</th><th scope="col">价格 ({market.quote})</th>
                  <th scope="col">数量 ({market.base})</th><th scope="col">流动性</th>
                </tr></thead>
                <tbody>{trades.map((trade) => (
                  <tr key={`${trade.trade_id}:${trade.order_id}`}>
                    <td><time dateTime={trade.created_at} title={new Date(trade.created_at).toLocaleString('zh-CN')}>{fmtTime(trade.created_at)}</time></td>
                    <td className={trade.side === 'buy' ? 'rise' : 'fall'}>{trade.side === 'buy' ? '买入' : '卖出'}</td>
                    <td title={trade.price}>{fmt(trade.price, market.priceDp)}</td>
                    <td title={trade.quantity}>{fmt(trade.quantity, market.qtyDp)}</td>
                    <td>{trade.maker ? '挂单方' : '吃单方'}</td>
                  </tr>
                ))}</tbody>
              </table>
            </div>
          ) : !error && <p className="panel-message" role="status">{trades ? '暂无成交' : '正在加载成交'}</p>)
            : (filtered?.length ? (
              <div className="activity-scroll" role="region" aria-label="委托表" tabIndex={0}>
                <table className="data-table activity-table order-table" aria-labelledby={labelId}>
                  <thead><tr>
                    <th scope="col">时间</th><th scope="col">方向</th><th scope="col">类型</th>
                    <th scope="col">价格 ({market.quote})</th><th scope="col">数量 ({market.base})</th>
                    <th scope="col">已成交</th><th scope="col">状态</th>{current && <th scope="col">操作</th>}
                  </tr></thead>
                  <tbody>{filtered.map((order) => {
                    const cancelling = order.cancel_requested || cancelSent.has(order.order_id);
                    return (
                      <tr key={order.order_id}>
                        <td><time dateTime={order.created_at} title={new Date(order.created_at).toLocaleString('zh-CN')}>{fmtTime(order.created_at)}</time></td>
                        <td className={order.side === 'buy' ? 'rise' : 'fall'}>{order.side === 'buy' ? '买入' : '卖出'}</td>
                        <td>{ORDER_TYPES[order.type]}</td>
                        <td title={order.price}>{order.type === 'market' ? '—' : fmt(order.price, market.priceDp)}</td>
                        <td title={order.quantity}>{fmt(order.quantity, market.qtyDp)}</td>
                        <td title={order.filled_quantity}>{fmt(order.filled_quantity, market.qtyDp)}</td>
                        <td title={order.reject_reason || undefined}>{statusNames[order.status]}</td>
                        {current && <td>
                          <button type="button" className="button cancel-order" disabled={cancelling || order.status === 'pending'}
                            aria-label={`撤单 ${order.order_id.slice(0, 8)}`}
                            title={order.status === 'pending' ? '委托处理中' : undefined}
                            onClick={() => void cancel(order.order_id)}>{cancelling ? '撤单中' : '撤单'}</button>
                        </td>}
                      </tr>
                    );
                  })}</tbody>
                </table>
              </div>
            ) : !error && <p className="panel-message" role="status">{orders ? '暂无委托' : '正在加载委托'}</p>)}
        </>}
      </div>
    </section>
  );
}
