import type { CSSProperties } from 'react';

/**
 * The mark: two rings and a check. The rings are the resource hierarchy, the check is a permission granted. It is one
 * colour (currentColor), so it takes the accent of the theme; /icon.svg is the same mark for the browser tab.
 */
function BrandMark({ size = 22, style }: { size?: number; style?: CSSProperties }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 64 64"
      aria-hidden="true"
      style={{ color: 'var(--licetum-accent)', ...style }}
    >
      <g fill="none" stroke="currentColor" strokeWidth="6">
        <circle cx="32" cy="32" r="26" />
        <circle cx="32" cy="32" r="14" />
      </g>
      <path
        d="M26.5 32.5 30.5 36.5 37.5 28.5"
        fill="none"
        stroke="currentColor"
        strokeWidth="6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function Brand({ size = 22 }: { size?: number }) {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 8,
        fontWeight: 700,
        fontSize: size * 0.8,
      }}
    >
      <BrandMark size={size} />
      Licetum
    </span>
  );
}
