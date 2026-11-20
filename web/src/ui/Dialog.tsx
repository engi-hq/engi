import { useEffect, useRef, type ReactNode } from 'react';
import { t } from '../i18n/t';
import { Icon } from './Icon';

const FOCUSABLE =
  'a[href],button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled),[tabindex]:not([tabindex="-1"])';

interface Props {
  title: string;
  onClose: () => void;
  children: ReactNode;
  wide?: boolean;
}

/** Modal dialog: traps Tab, closes on Escape, restores focus to the opener. */
export function Dialog({ title, onClose, children, wide }: Props) {
  const ref = useRef<HTMLDivElement>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    const root = ref.current;
    const first = root?.querySelector<HTMLElement>('[data-autofocus]') ?? root?.querySelector<HTMLElement>(FOCUSABLE);
    first?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        closeRef.current();
      } else if (e.key === 'Tab' && root) {
        const items = [...root.querySelectorAll<HTMLElement>(FOCUSABLE)];
        if (items.length === 0) return;
        const a = items[0] as HTMLElement;
        const z = items[items.length - 1] as HTMLElement;
        if (e.shiftKey && document.activeElement === a) {
          e.preventDefault();
          z.focus();
        } else if (!e.shiftKey && document.activeElement === z) {
          e.preventDefault();
          a.focus();
        }
      }
    };
    document.addEventListener('keydown', onKey, true);
    return () => {
      document.removeEventListener('keydown', onKey, true);
      opener?.focus?.();
    };
  }, []);

  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div ref={ref} className={`card dialog${wide ? ' wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}>
        <div className="toolbar">
          <h2>{title}</h2>
          <span className="spacer" />
          <button className="btn icon ghost sm" onClick={onClose} aria-label={t('common.close')}>
            <Icon name="close" />
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

interface ConfirmProps {
  title: string;
  message: string;
  confirmLabel: string;
  danger?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}

export function ConfirmDialog({ title, message, confirmLabel, danger, onConfirm, onClose }: ConfirmProps) {
  return (
    <Dialog title={title} onClose={onClose}>
      <p style={{ margin: 0 }}>{message}</p>
      <div className="dialog-actions">
        <button className="btn" onClick={onClose}>{t('common.cancel')}</button>
        <button
          className={`btn ${danger ? 'danger' : 'primary'}`}
          data-autofocus
          onClick={() => { onConfirm(); onClose(); }}
        >
          {confirmLabel}
        </button>
      </div>
    </Dialog>
  );
}
