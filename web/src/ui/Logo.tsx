export function Logo({ size = 22 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="8" fill="var(--accent)" />
      <circle cx="9" cy="16" r="3" fill="var(--on-accent)" />
      <circle cx="23" cy="9" r="3" fill="var(--on-accent)" />
      <circle cx="23" cy="23" r="3" fill="var(--g-trigger)" />
      <path d="M12 16h4l5-6M16 16l5 6" stroke="var(--on-accent)" strokeWidth="2" fill="none" strokeLinecap="round" />
    </svg>
  );
}
