import { useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { validAmount } from './decimal';
import { ASSETS } from './markets';
import type { Asset } from './markets';
import { Modal } from './Modal';

type Props = { onClose: () => void; onDeposit: (asset: Asset, amount: string) => Promise<void> };

export function DepositDialog({ onClose, onDeposit }: Props) {
  const [asset, setAsset] = useState<Asset>('USDT');
  const [amount, setAmount] = useState('');
  const [attempted, setAttempted] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const amountField = useRef<HTMLInputElement>(null);
  const valid = validAmount(amount.trim());
  const invalid = attempted && !valid;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;
    setAttempted(true);
    setError('');
    if (!valid) {
      amountField.current?.focus();
      return;
    }
    setPending(true);
    try {
      await onDeposit(asset, amount.trim());
    } catch (failure) {
      if (failure instanceof Error && failure.name === 'AbortError') return;
      setError(failure instanceof Error ? failure.message : '充值失败');
    } finally {
      setPending(false);
    }
  }

  return (
    <Modal title="充值" labelId="deposit-heading" onClose={onClose}>
      <form className="dialog-form deposit-form" onSubmit={submit} noValidate>
        <label className="field">
          <span>币种</span>
          <select value={asset} onChange={(event) => setAsset(event.target.value as Asset)} data-dialog-focus>
            {ASSETS.map((entry) => <option key={entry} value={entry}>{entry}</option>)}
          </select>
        </label>
        <div className="field">
          <label htmlFor="deposit-amount">金额</label>
          <input id="deposit-amount" ref={amountField} value={amount} type="text" inputMode="decimal" autoComplete="off" required
            onChange={(event) => setAmount(event.target.value)} aria-invalid={invalid || undefined}
            aria-describedby={invalid ? 'deposit-note deposit-amount-error' : 'deposit-note'} />
          {invalid && <span id="deposit-amount-error" className="field-error">请输入大于 0、最多 8 位小数且整数部分不超过 18 位的金额</span>}
        </div>
        <p id="deposit-note" className="dialog-note">演示环境：充值立即到账</p>
        {error && <p className="form-notice fall" role="alert">{error}</p>}
        <button type="submit" className="button button-accent" disabled={pending}>{pending ? '正在充值' : '确认充值'}</button>
      </form>
    </Modal>
  );
}
