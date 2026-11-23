import type { KeyboardEvent, ReactNode } from 'react';

type Tab<T> = { value: T; label: ReactNode; className?: string; ariaLabel?: string };

type Props<T extends string> = {
  id: string;
  label: string;
  items: readonly Tab<T>[];
  active: T;
  onChange: (value: T) => void;
  className?: string;
};

export function Tabs<T extends string>({ id, label, items, active, onChange, className }: Props<T>) {
  function move(event: KeyboardEvent<HTMLButtonElement>, current: number) {
    let next: number;
    switch (event.key) {
      case 'ArrowLeft': next = (current + items.length - 1) % items.length; break;
      case 'ArrowRight': next = (current + 1) % items.length; break;
      case 'Home': next = 0; break;
      case 'End': next = items.length - 1; break;
      default: return;
    }
    event.preventDefault();
    event.currentTarget.parentElement?.querySelectorAll('button')[next]?.focus();
    onChange(items[next].value);
  }

  return (
    <div className={className ? `tabs ${className}` : 'tabs'} role="tablist" aria-label={label}>
      {items.map((item, index) => (
        <button
          type="button"
          key={item.value}
          className={item.className}
          role="tab"
          id={`${id}-${item.value}`}
          aria-label={item.ariaLabel}
          aria-selected={item.value === active}
          aria-controls={`${id}-panel`}
          tabIndex={item.value === active ? 0 : -1}
          onClick={() => onChange(item.value)}
          onKeyDown={(event) => move(event, index)}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
