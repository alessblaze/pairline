export const tabButtonClass = (active: boolean) =>
  `relative flex h-11 w-full items-center justify-start gap-3 rounded-none px-5 text-sm font-bold uppercase tracking-[0.14em] transition-all duration-200 ${
    active
      ? 'border border-electric-cyan/40 bg-electric-cyan/10 text-electric-cyan shadow-[0_0_12px_rgba(34,211,238,0.12)]'
      : 'border border-transparent bg-transparent text-[var(--admin-text-soft)] hover:bg-[var(--admin-muted-surface)] hover:text-[var(--admin-text)] font-medium'
  }`;

export const filterButtonClass = (active: boolean, type: 'cyan' | 'rose' = 'cyan') => {
  const activeStyles =
    type === 'rose'
      ? 'border-danger-rose/40 bg-danger-rose/[0.08] text-danger-rose shadow-[0_0_12px_rgba(244,63,94,0.12)]'
      : 'border-electric-cyan/40 bg-electric-cyan/[0.08] text-electric-cyan shadow-[0_0_12px_rgba(34,211,238,0.12)]';

  return `inline-flex h-11 items-center justify-center rounded-none border px-4 text-[10px] font-bold uppercase tracking-[0.14em] transition-all duration-200 ${
    active
      ? activeStyles
      : 'border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] text-[var(--admin-text-soft)] hover:text-[var(--admin-text)] hover:bg-[var(--admin-muted-surface-hover)]'
  }`;
};

export const metricCardClass = (type: 'active' | 'inactive' | 'total' | 'pending' | 'approved') => {
  const glowShadow =
    type === 'active' || type === 'pending'
      ? 'shadow-[inset_0_0_20px_rgba(244,63,94,0.05)] border-danger-rose/20'
      : type === 'total'
        ? 'shadow-[inset_0_0_20px_rgba(34,211,238,0.05)] border-electric-cyan/20'
        : type === 'approved'
          ? 'shadow-[inset_0_0_20px_rgba(52,211,153,0.05)] border-success-emerald/20'
          : 'border-white/[0.07]';

  return `surface-card rounded-none flex flex-row items-center gap-4 p-5 transition-all duration-300 hover:border-[var(--admin-surface-border-strong)] ${glowShadow}`;
};

export const surfaceCardClass = 'surface-card rounded-none p-5';

export const inputClass =
  'h-11 w-full rounded-none border border-[var(--admin-input-border)] bg-[var(--admin-input-bg)] px-4 font-mono text-[16px] text-[var(--admin-input-text)] placeholder:text-[var(--admin-placeholder)] outline-none transition-all duration-200 focus:border-electric-cyan/50 focus:ring-2 focus:ring-electric-cyan/15 focus:bg-[var(--admin-input-focus-bg)] scanlines';

export const compactSelectClass =
  'h-11 w-full rounded-none border border-[var(--admin-input-border)] bg-[var(--admin-input-bg)] px-3 font-semibold uppercase tracking-wider text-sm text-[var(--admin-input-text)] outline-none transition-all duration-200 focus:border-electric-cyan/50 focus:ring-2 focus:ring-electric-cyan/15 [&>option]:bg-[var(--admin-select-option-bg)] [&>option]:text-[var(--admin-select-option-text)]';

export const segmentedToggleButtonClass = (active: boolean) =>
  `flex h-10 flex-1 items-center justify-center rounded-none px-4 text-[10px] font-bold uppercase tracking-[0.14em] transition-all duration-200 ${
    active
      ? 'bg-[var(--admin-text)] text-[var(--admin-bg)] shadow-[0_0_14px_rgba(255,255,255,0.1)]'
      : 'text-[var(--admin-text-soft)] hover:bg-[var(--admin-muted-surface)] hover:text-[var(--admin-text)]'
  }`;

export const actionButtonClass =
  'flex h-11 items-center justify-center gap-2 rounded-none px-4 text-xs font-bold uppercase tracking-wide transition-all duration-100 disabled:cursor-not-allowed disabled:opacity-50 active:scale-[0.97]';
