import { useLayoutEffect, useRef } from 'react';
import {
  CandlestickSeries, ColorType, CrosshairMode, HistogramSeries, createChart,
} from 'lightweight-charts';
import type { CandlestickData, HistogramData, IChartApi, ISeriesApi, UTCTimestamp } from 'lightweight-charts';
import type { Candle } from './api';
import { INTERVALS } from './markets';
import type { Interval, Market } from './markets';
import { Tabs } from './Tabs';

type Props = {
  market: Market;
  interval: Interval;
  candles?: Candle[];
  error: Error | null;
  onInterval: (interval: Interval) => void;
};

type Chart = { chart: IChartApi; price: ISeriesApi<'Candlestick'>; volume: ISeriesApi<'Histogram'> };
const intervalTabs = INTERVALS.map((interval) => ({ value: interval, label: interval }));

export function PriceChart({ market, interval, candles, error, onInterval }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const series = useRef<Chart | null>(null);
  const fitted = useRef(false);

  useLayoutEffect(() => {
    if (!host.current) return;
    const chart = createChart(host.current, {
      autoSize: true,
      layout: {
        background: { type: ColorType.Solid, color: 'transparent' },
        textColor: '#8590AE',
        fontFamily: getComputedStyle(host.current).fontFamily,
        fontSize: 12,
      },
      grid: { vertLines: { color: '#1D2848' }, horzLines: { color: '#1D2848' } },
      timeScale: { timeVisible: true, secondsVisible: false, borderColor: '#263257' },
      rightPriceScale: { borderColor: '#263257', scaleMargins: { top: 0.1, bottom: 0.24 } },
      crosshair: { mode: CrosshairMode.Normal },
    });
    const price = chart.addSeries(CandlestickSeries, {
      upColor: '#2FC48D', downColor: '#EE5D6C', wickUpColor: '#2FC48D', wickDownColor: '#EE5D6C', borderVisible: false,
    });
    const volume = chart.addSeries(HistogramSeries, {
      priceScaleId: '', priceFormat: { type: 'volume' }, priceLineVisible: false, lastValueVisible: false,
    });
    chart.priceScale('').applyOptions({ scaleMargins: { top: 0.8, bottom: 0 } });
    series.current = { chart, price, volume };
    return () => {
      series.current = null;
      chart.remove();
    };
  }, []);

  useLayoutEffect(() => {
    fitted.current = false;
  }, [market.path, interval]);

  useLayoutEffect(() => {
    if (!series.current) return;
    const { chart, price, volume } = series.current;
    const prices: CandlestickData[] = [];
    const volumes: HistogramData[] = [];
    const localOffset = new Date().getTimezoneOffset() * 60;
    for (const candle of candles ?? []) {
      const time = (candle.time - localOffset) as UTCTimestamp;
      const open = Number(candle.open);
      const close = Number(candle.close);
      prices.push({ time, open, close, high: Number(candle.high), low: Number(candle.low) });
      volumes.push({ time, value: Number(candle.volume), color: close >= open ? '#2FC48D73' : '#EE5D6C73' });
    }
    price.setData(prices);
    volume.setData(volumes);
    if (!fitted.current && prices.length > 0) {
      chart.timeScale().fitContent();
      fitted.current = true;
    }
  }, [candles, market.path, interval]);

  return (
    <section className="panel chart-panel" aria-labelledby="chart-heading">
      <div className="panel-head chart-head">
        <h2 id="chart-heading">K 线</h2>
        <Tabs id="interval" label="K 线周期" items={intervalTabs} active={interval} onChange={onInterval} className="interval-tabs" />
      </div>
      <div className="chart-body" id="interval-panel" role="tabpanel" aria-labelledby={`interval-${interval}`}>
        <div className="chart-host" ref={host} role="img" aria-label={`${market.symbol} ${interval} 价格和成交量`} />
        {!candles?.length && (
          <p className="chart-message" role="status">
            {error ? `K 线更新失败：${error.message}` : candles ? '暂无成交，K 线将在第一笔成交后出现' : '正在加载 K 线'}
          </p>
        )}
        {error && !!candles?.length && <p className="chart-message chart-stale" role="status">K 线更新失败：{error.message}</p>}
      </div>
    </section>
  );
}
