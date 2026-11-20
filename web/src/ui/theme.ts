import { useCallback, useState } from 'react';

type Theme = 'system' | 'light' | 'dark';
const KEY = 'engi.theme';

function read(): Theme {
  try {
    const v = localStorage.getItem(KEY);
    return v === 'light' || v === 'dark' ? v : 'system';
  } catch {
    return 'system';
  }
}

export function applyTheme(theme: Theme): void {
  if (theme === 'system') document.documentElement.removeAttribute('data-theme');
  else document.documentElement.setAttribute('data-theme', theme);
}

/** Cycles system -> light -> dark; the choice is a per-browser convenience. */
export function useTheme() {
  const [theme, setTheme] = useState<Theme>(read);
  const toggle = useCallback(() => {
    const next: Theme = theme === 'system' ? 'light' : theme === 'light' ? 'dark' : 'system';
    setTheme(next);
    applyTheme(next);
    try {
      localStorage.setItem(KEY, next);
    } catch {
      /* storage may be blocked */
    }
  }, [theme]);
  return { theme, toggle };
}
