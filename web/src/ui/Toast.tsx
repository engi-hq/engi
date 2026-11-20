import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';
import { ApiError } from '../api/client';

interface ToastItem { id: number; text: string; error: boolean }
interface ToastApi {
  info: (text: string) => void;
  error: (e: unknown) => void;
}

const Ctx = createContext<ToastApi>({ info: () => undefined, error: () => undefined });
let seq = 0;

export function describeError(e: unknown): string {
  if (e instanceof ApiError) {
    const extra = e.problem.errors?.map((x) => `${x.pointer}: ${x.message}`).join('; ');
    return extra ? `${e.message} (${extra})` : e.message;
  }
  return e instanceof Error ? e.message : String(e);
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const push = useCallback((text: string, error: boolean) => {
    const id = ++seq;
    setItems((xs) => [...xs, { id, text, error }]);
    setTimeout(() => setItems((xs) => xs.filter((x) => x.id !== id)), error ? 8000 : 4000);
  }, []);
  const api = useMemo<ToastApi>(
    () => ({ info: (t) => push(t, false), error: (e) => push(describeError(e), true) }),
    [push],
  );
  return (
    <Ctx.Provider value={api}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {items.map((x) => (
          <div key={x.id} className={`toast${x.error ? ' error' : ''}`}>{x.text}</div>
        ))}
      </div>
    </Ctx.Provider>
  );
}

export const useToast = () => useContext(Ctx);
