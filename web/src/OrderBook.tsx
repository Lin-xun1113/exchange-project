import { useState } from 'react';
import type { Book, Level, Side } from './api';
import { decimalText, ticks } from './decimal';
import { fmt } from './format';
import type { Market } from './markets';

const LEVELS = 14;

type DepthLevel = Level & { cumulative: bigint };

function depth(levels: readonly Level[]): DepthLevel[] {
  let cumulative = 0n;
  return levels.slice(0, LEVELS).map((level) => {
    cumulative += ticks(level.quantity);
    return { ...level, cumulative };
  });
}

type SideProps = {
  rows: DepthLevel[];
  max: bigint;
  side: Side;
  market: Market;
  onPickPrice: (price: string, side: Side) => void;
};

function BookSide({ rows, max, side, market, onPickPrice }: SideProps) {
  const ask = side === 'sell';
  const levels = Array.from({ length: LEVELS }, (_, index) => rows[index]);
  if (ask) levels.reverse();
  return (
    <div className={`book-side book-scroll ${ask ? 'book-asks' : 'book-bids'}`} role="region" aria-label={ask ? '卖方深度' : '买方深度'} tabIndex={0}>
      <div className="book-levels">
        {levels.map((level, index) => {
          if (!level) return <div className="book-row empty-level" key={`empty-${index}`} aria-hidden="true"><span>—</span><span>—</span><span>—</span></div>;
          const price = fmt(level.price, market.priceDp);
          const quantity = fmt(level.quantity, market.qtyDp);
          const cumulative = fmt(decimalText(level.cumulative, 8), market.qtyDp);
          const width = max > 0n ? Number(level.cumulative) / Number(max) * 100 : 0;
          return (
            <button type="button" className="book-row" key={level.price}
              onClick={() => onPickPrice(level.price, ask ? 'buy' : 'sell')}
              aria-label={`${ask ? '以卖价买入' : '以买价卖出'} ${price} ${market.quote}，数量 ${quantity}，累计 ${cumulative}`}
              title={`${level.price} ${market.quote} / ${level.quantity} ${market.base}`}>
              <span className="depth-bar" style={{ width: `${width}%` }} aria-hidden="true" />
              <span className="level-price">{price}</span>
              <span>{quantity}</span>
              <span>{cumulative}</span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

function Spread({ market, last, bestAsk, bestBid }: { market: Market; last?: string; bestAsk?: Level; bestBid?: Level }) {
  const current = last === undefined ? null : ticks(last);
  const [movement, setMovement] = useState({ price: current, direction: 0 });
  if (movement.price !== current) {
    const direction = current === null || movement.price === null || movement.price === 0n
      ? 0 : current > movement.price ? 1 : -1;
    setMovement({ price: current, direction });
  }

  let spread = '—';
  if (bestAsk && bestBid) {
    const bid = ticks(bestBid.price);
    const gap = ticks(bestAsk.price) - bid;
    const percent = (gap * 10_000n + bid / 2n) / bid;
    spread = `${fmt(decimalText(gap, 8), market.priceDp)} (${decimalText(percent, 2)}%)`;
  }

  return (
    <div className="book-spread">
      {movement.direction !== 0 && <span className="spread-flash" key={String(current)} aria-hidden="true" />}
      <div className="spread-price">
        <span>{last === undefined ? '—' : fmt(last, market.priceDp)}</span>
        {movement.direction !== 0 && (
          <span className={movement.direction > 0 ? 'rise' : 'fall'} aria-label={movement.direction > 0 ? '上涨' : '下跌'}>
            {movement.direction > 0 ? '▲' : '▼'}
          </span>
        )}
      </div>
      <span className="spread-detail">价差 <span>{spread}</span></span>
    </div>
  );
}

type Props = {
  market: Market;
  book?: Book;
  last?: string;
  error: Error | null;
  onPickPrice: (price: string, side: Side) => void;
};

export function OrderBook({ market, book, last, error, onPickPrice }: Props) {
  const asks = depth(book?.asks ?? []);
  const bids = depth(book?.bids ?? []);
  const askTotal = asks.at(-1)?.cumulative ?? 0n;
  const bidTotal = bids.at(-1)?.cumulative ?? 0n;
  const max = askTotal > bidTotal ? askTotal : bidTotal;

  return (
    <section className="panel book-panel" aria-labelledby="book-heading">
      <div className="panel-head">
        <h2 id="book-heading">订单簿</h2>
        {error ? <span className="panel-status fall" role="status" title={error.message}>更新失败</span>
          : !book ? <span className="panel-status" role="status">正在加载</span> : null}
      </div>
      <div className="book-column-head book-row" aria-hidden="true">
        <span>价格 ({market.quote})</span><span>数量 ({market.base})</span><span>累计 ({market.base})</span>
      </div>
      <BookSide rows={asks} max={max} side="sell" market={market} onPickPrice={onPickPrice} />
      <Spread market={market} last={last} bestAsk={asks[0]} bestBid={bids[0]} />
      <BookSide rows={bids} max={max} side="buy" market={market} onPickPrice={onPickPrice} />
    </section>
  );
}
