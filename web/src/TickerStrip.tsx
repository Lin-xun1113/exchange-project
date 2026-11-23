import type { Ticker } from './api';
import { changeTone, fmt, fmtChange } from './format';
import type { Market } from './markets';

type Props = { ticker?: Ticker; market: Market; error: Error | null };

export function TickerStrip({ ticker, market, error }: Props) {
  const columns = [
    { label: '最新价', value: ticker && fmt(ticker.last_price, market.priceDp), className: 'ticker-last' },
    { label: '24h 涨跌', value: ticker && fmtChange(ticker.change_percent_24h), className: ticker && changeTone(ticker.change_percent_24h) },
    { label: '24h 最高', value: ticker && fmt(ticker.high_24h, market.priceDp) },
    { label: '24h 最低', value: ticker && fmt(ticker.low_24h, market.priceDp) },
    { label: `24h 成交量 (${market.base})`, value: ticker && fmt(ticker.volume_24h, market.qtyDp) },
    { label: `24h 成交额 (${market.quote})`, value: ticker && fmt(ticker.quote_volume_24h, 2) },
  ];

  return (
    <section className="ticker-strip" aria-label={`${market.symbol} 行情`}>
      <dl>
        {columns.map(({ label, value, className }) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd className={className} title={value}>{value ?? '—'}</dd>
          </div>
        ))}
      </dl>
      {error && <p className="ticker-error" role="status">行情更新失败：{error.message}</p>}
    </section>
  );
}
