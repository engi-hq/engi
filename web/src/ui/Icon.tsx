// Engi's own icon set: simple 24x24 stroke glyphs addressed by the icon keys in node descriptors.
const P: Record<string, string> = {
  play: 'M7 5l11 7-11 7z',
  clock: 'M12 7v5l3 2M12 3a9 9 0 100 18 9 9 0 000-18z',
  split: 'M5 12h5l4-6h5M10 12l4 6h5M17 4l2 2-2 2M17 16l2 2-2 2',
  'git-fork': 'M12 20v-6M12 14c0-3-6-3-6-6M12 14c0-3 6-3 6-6M6 8V5M18 8V5',
  merge: 'M5 5l7 7v7M19 5l-7 7',
  'arrow-left': 'M19 12H5M11 6l-6 6 6 6',
  'arrow-right': 'M5 12h14M13 6l6 6-6 6',
  reply: 'M9 7L4 12l5 5M4 12h10a6 6 0 016 6',
  globe: 'M12 3a9 9 0 100 18 9 9 0 000-18zM3 12h18M12 3c3 3 3 15 0 18M12 3c-3 3-3 15 0 18',
  layers: 'M12 4l9 5-9 5-9-5zM3 14l9 5 9-5',
  filter: 'M4 5h16l-6 7v6l-4 2v-8z',
  scissors: 'M6 6a2 2 0 100 4 2 2 0 000-4zM6 14a2 2 0 100 4 2 2 0 000-4zM8 9l11 8M8 15L19 7',
  pen: 'M4 20l1-4L16 5l3 3L8 19zM14 7l3 3',
  'arrow-up-down': 'M8 20V5M4 9l4-4 4 4M16 4v15M12 15l4 4 4-4',
  'unfold-vertical': 'M12 22v-6M9 19l3-3 3 3M12 2v6M9 5l3 3 3-3M4 12h16',
  webhook: 'M8 17a4 4 0 110-6l3-5M16 17h-5l-3-6M14 8a4 4 0 117 3',
  workflow: 'M4 4h6v6H4zM14 14h6v6h-6zM10 7h4a3 3 0 013 3v4',
  code: 'M8 8l-5 4 5 4M16 8l5 4-5 4M14 5l-4 14',
  key: 'M14 10a4 4 0 11-1 3l-9 3v-3l2-1v-2l3-1',
  plus: 'M12 5v14M5 12h14',
  search: 'M11 4a7 7 0 100 14 7 7 0 000-14zM21 21l-5-5',
  trash: 'M5 7h14M9 7V4h6v3M7 7l1 13h8l1-13',
  copy: 'M9 9h10v11H9zM5 15V4h10',
  undo: 'M9 14L4 9l5-5M4 9h10a6 6 0 010 12h-3',
  redo: 'M15 14l5-5-5-5M20 9H10a6 6 0 000 12h3',
  save: 'M5 4h12l3 3v13H5zM8 4v5h8V4M8 20v-6h8v6',
  stop: 'M6 6h12v12H6z',
  download: 'M12 4v11M7 11l5 5 5-5M5 20h14',
  upload: 'M12 16V5M7 9l5-5 5 5M5 20h14',
  close: 'M6 6l12 12M18 6L6 18',
  check: 'M5 12l5 5 9-10',
  alert: 'M12 4l9 16H3zM12 10v4M12 17v.5',
  power: 'M12 3v8M7 6a8 8 0 1010 0',
  eye: 'M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12zM12 9a3 3 0 100 6 3 3 0 000-6z',
  'eye-off': 'M3 3l18 18M10 5.2A9 9 0 0112 5c6 0 10 7 10 7a17 17 0 01-3 3.7M6.5 6.5C3.8 8.3 2 12 2 12s4 7 10 7a9 9 0 004-1',
  link: 'M10 14a4 4 0 005.7 0l3-3a4 4 0 00-5.7-5.7l-1 1M14 10a4 4 0 00-5.7 0l-3 3a4 4 0 005.7 5.7l1-1',
  logout: 'M10 5H5v14h5M15 8l4 4-4 4M19 12H9',
  list: 'M8 6h12M8 12h12M8 18h12M4 6h.5M4 12h.5M4 18h.5',
  grid: 'M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z',
  sun: 'M12 8a4 4 0 100 8 4 4 0 000-8zM12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5L19 19M5 19l1.5-1.5M17.5 6.5L19 5',
  help: 'M9.5 9a2.5 2.5 0 115 0c0 1.7-2.5 2-2.5 4M12 17v.5',
  pin: 'M9 4h6l-1 6 3 3H7l3-3zM12 13v7',
  settings: 'M4 7h9M17 7h3M4 17h3M11 17h9M15 4v6M9 14v6',
  fit: 'M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5',
  note: 'M5 4h14v11l-5 5H5zM14 20v-5h5M8 9h8M8 12h5',
};

export type IconName = keyof typeof P;

export function Icon({ name, size = 16, title }: { name: string; size?: number; title?: string }) {
  const d = P[name] ?? P['workflow'] ?? '';
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden={title ? undefined : true}
      role={title ? 'img' : undefined}
      aria-label={title}
      focusable="false"
    >
      <path d={d} />
    </svg>
  );
}
