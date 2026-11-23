import type { Balance } from './api';
import { fmt } from './format';
import { ASSETS, ASSET_DP } from './markets';

type Props = {
  signedIn: boolean;
  balances?: Balance[];
  error: Error | null;
  onDeposit: () => void;
};

export function Balances({ signedIn, balances, error, onDeposit }: Props) {
  return (
    <section className="panel balances-panel" aria-labelledby="balances-heading">
      <div className="panel-head">
        <h2 id="balances-heading">资产</h2>
        {signedIn && <button type="button" className="button button-outline" onClick={onDeposit}>充值</button>}
      </div>
      <table className="data-table balance-table" aria-labelledby="balances-heading">
        <thead><tr><th scope="col">币种</th><th scope="col">可用</th><th scope="col">冻结</th></tr></thead>
        <tbody>
          {ASSETS.map((asset) => {
            const balance = balances?.find((entry) => entry.asset === asset);
            return (
              <tr key={asset}>
                <th scope="row">{asset}</th>
                <td title={balance?.available}>{balances ? fmt(balance?.available ?? '0', ASSET_DP[asset]) : '—'}</td>
                <td title={balance?.frozen}>{balances ? fmt(balance?.frozen ?? '0', ASSET_DP[asset]) : '—'}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {error ? <p className="panel-message fall" role="status">资产更新失败：{error.message}</p>
        : signedIn && !balances ? <p className="panel-message" role="status">正在加载资产</p> : null}
    </section>
  );
}
