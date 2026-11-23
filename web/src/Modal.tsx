import { useLayoutEffect, useRef } from 'react';
import type { ReactNode } from 'react';

type Props = { labelId: string; title: string; onClose: () => void; children: ReactNode };

export function Modal({ labelId, title, onClose, children }: Props) {
  const ref = useRef<HTMLDialogElement>(null);

  useLayoutEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    const opener = document.activeElement;
    dialog.showModal();
    dialog.querySelector<HTMLElement>('[data-dialog-focus]')?.focus();
    return () => {
      dialog.close();
      if (opener instanceof HTMLElement && opener.isConnected) opener.focus();
    };
  }, []);

  return (
    <dialog ref={ref} className="dialog" aria-labelledby={labelId} onCancel={(event) => {
      event.preventDefault();
      onClose();
    }}>
      <div className="dialog-head">
        <h2 id={labelId}>{title}</h2>
        <button type="button" className="dialog-close" aria-label="关闭" onClick={onClose}>×</button>
      </div>
      <div className="dialog-content">{children}</div>
    </dialog>
  );
}
