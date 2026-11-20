import { useEffect, useRef } from 'react';

export interface Shortcut {
  /** e.g. "mod+s", "mod+shift+z", "d", "?" - mod is Ctrl or Cmd. */
  combo: string;
  run: (e: KeyboardEvent) => void;
}

export const isEditable = (el: EventTarget | null): boolean => {
  if (!(el instanceof HTMLElement)) return false;
  return el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName) || !!el.closest('.cm-editor, [role=dialog]');
};

export function comboOf(e: KeyboardEvent): string {
  const parts: string[] = [];
  if (e.ctrlKey || e.metaKey) parts.push('mod');
  if (e.shiftKey && (e.ctrlKey || e.metaKey || e.key.length > 1)) parts.push('shift');
  if (e.altKey) parts.push('alt');
  parts.push(e.key.toLowerCase());
  return parts.join('+');
}

/** Window-level shortcuts. Bare keys are ignored while typing or inside dialogs; mod-combos always work except in dialogs. */
export function useShortcuts(list: Shortcut[]): void {
  const ref = useRef(list);
  ref.current = list;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const combo = comboOf(e);
      const hit = ref.current.find((s) => s.combo === combo);
      if (!hit) return;
      const typing = isEditable(e.target);
      const inDialog = e.target instanceof HTMLElement && !!e.target.closest('[role=dialog]');
      if (inDialog) return;
      if (typing && !combo.startsWith('mod+')) return;
      // Let the code editor and inputs keep their own undo/copy/paste.
      if (typing && ['mod+z', 'mod+shift+z', 'mod+c', 'mod+v', 'mod+d'].includes(combo)) return;
      e.preventDefault();
      hit.run(e);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
}
