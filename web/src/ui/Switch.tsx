import { useId } from 'react';

interface Props {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  hideLabel?: boolean;
  disabled?: boolean;
}

export function Switch({ checked, onChange, label, hideLabel, disabled }: Props) {
  const id = useId();
  return (
    <label className="switch" htmlFor={id}>
      <input
        id={id}
        type="checkbox"
        role="switch"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
        aria-label={hideLabel ? label : undefined}
      />
      <span className="track" aria-hidden="true" />
      {!hideLabel && <span>{label}</span>}
    </label>
  );
}
