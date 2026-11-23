import type { MarketTrade } from './api';
import { fmt, fmtTime } from './format';
import type { Market } from './markets';

type Props = { market: Market; trades?: MarketTrade[]; error: Error | null };

export function MarketTrades({ market, trades, error }: Props) {
  return (
    <section className="panel trades-panel" aria-labelledby="trades-heading">
      <div className="panel-head">
        <h2 id="trades-heading">市场成交</h2>
        {error && !!trades?.length && <span className="panel-status fall" role="status" title={error.message}>更新失败</span>}
      </div>
      {trades?.length ? (
        <div className="market-tape" role="region" aria-label="最近 40 笔成交" tabIndex={0}>
          <table className="data-table tape-table" aria-labelledby="trades-heading">
            <thead><tr><th scope="col">价格 ({market.quote})</th><th scope="col">数量 ({market.base})</th><th scope="col">时间</th></tr></thead>
            <tbody>
              {trades.map((trade) => (
                <tr key={trade.trade_id}>
                  <td className={trade.taker_side === 'buy' ? 'rise' : 'fall'}>
                    <span className="sr-only">{trade.taker_side === 'buy' ? '买入 ' : '卖出 '}</span>{fmt(trade.price, market.priceDp)}
                  </td>
                  <td>{fmt(trade.quantity, market.qtyDp)}</td>
                  <td><time dateTime={trade.created_at}>{fmtTime(trade.created_at)}</time></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : <p className="panel-message" role="status">{error ? error.message : trades ? '暂无成交' : '正在加载成交'}</p>}
    </section>
  );
}
