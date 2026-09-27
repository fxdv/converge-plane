import type { IconProps } from './types';

export function PullRequestLine({ size = 18, className, color }: IconProps) {
  const stroke = color ? color : 'currentColor';
  return (
    <svg
      width={size}
      height={size}
      className={className}
      viewBox="0 0 20 20"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
    >
      <g
        stroke={stroke}
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <circle cx="5" cy="4.5" r="2" />
        <circle cx="5" cy="15.5" r="2" />
        <circle cx="15" cy="15.5" r="2" />
        <path d="M5 6.5V13.5" />
        <path d="M15 13.5V8.5C15 6.84 13.66 5.5 12 5.5H9.5" />
        <path d="M11.5 3.5L9.5 5.5L11.5 7.5" />
      </g>
    </svg>
  );
}
