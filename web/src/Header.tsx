import { useRef } from 'react';
import type { Ticker } from './api';
import { changeTone, fmt, fmtChange } from './format';
import { MARKETS } from './markets';
import type { MarketPath } from './markets';
import type { Session } from './session';
import { Tabs } from './Tabs';

type Props = {
  path: MarketPath;
  tickers: readonly Ticker[];
  session: Session | null;
  onMarket: (path: MarketPath) => void;
  onLogin: () => void;
  onDeposit: () => void;
  onLogout: () => void;
};

export function Header({ path, tickers, session, onMarket, onLogin, onDeposit, onLogout }: Props) {
  const menu = useRef<HTMLDetailsElement>(null);
  const summary = useRef<HTMLElement>(null);
  const markets = MARKETS.map((market) => {
    const ticker = tickers.find(({ symbol }) => symbol === market.symbol);
    return {
      value: market.path,
      ariaLabel: market.symbol,
      label: <>
        <span className="market-symbol">{market.symbol}</span>
        <span className="market-quote">
          <span>{ticker ? fmt(ticker.last_price, market.priceDp) : '—'}</span>
          {ticker && <span className={changeTone(ticker.change_percent_24h)}>{fmtChange(ticker.change_percent_24h)}</span>}
        </span>
      </>,
    };
  });

  function closeMenu() {
    if (menu.current) menu.current.open = false;
    summary.current?.focus();
  }

  return (
    <header className="terminal-header">
      <h1 className="wordmark">交易台</h1>
      <Tabs id="market" label="交易对" items={markets} active={path} onChange={onMarket} className="market-tabs" />
      <div className="header-account">
        {session ? (
          <details
            className="account-menu"
            ref={menu}
            onBlur={(event) => {
              if (!event.currentTarget.contains(event.relatedTarget)) event.currentTarget.open = false;
            }}
            onKeyDown={(event) => {
              if (event.key === 'Escape' && event.currentTarget.open) closeMenu();
            }}
          >
            <summary id="account-control" ref={summary} title={session.username}>
              <span className="account-name">{session.username}</span><span aria-hidden="true">▾</span>
            </summary>
            <div className="account-menu-items">
              <button type="button" onClick={() => { closeMenu(); onDeposit(); }}>充值</button>
              <button type="button" onClick={onLogout}>退出登录</button>
            </div>
          </details>
        ) : <button id="account-control" type="button" className="button button-accent" onClick={onLogin}>登录</button>}
      </div>
    </header>
  );
}
