import { useId } from "react";

interface SparklineProps {
  values: number[];
  min?: number;
  max?: number;
  color?: string;
  height?: number;
  strokeWidth?: number;
  className?: string;
}

const VIEW_W = 200;
const PAD = 4;

export function Sparkline({
  values,
  min,
  max,
  color = "currentColor",
  height = 44,
  strokeWidth = 2,
  className,
}: SparklineProps) {
  const gradientId = useId();
  const viewBox = `0 0 ${VIEW_W} ${height}`;

  if (values.length < 2) {
    return (
      <svg className={className} height={height} viewBox={viewBox} preserveAspectRatio="none" aria-hidden="true" />
    );
  }

  const lo = min ?? Math.min(...values);
  const hi = max ?? Math.max(...values);
  const span = hi - lo || 1;
  const n = values.length;
  const x = (i: number) => (i / (n - 1)) * VIEW_W;
  const y = (v: number) => height - PAD - ((v - lo) / span) * (height - PAD * 2);

  const line = values.map((v, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  const area = `M0,${height} ${values
    .map((v, i) => `L${x(i).toFixed(1)},${y(v).toFixed(1)}`)
    .join(" ")} L${VIEW_W},${height} Z`;

  return (
    <svg className={className} height={height} viewBox={viewBox} preserveAspectRatio="none" aria-hidden="true">
      <defs>
        <linearGradient id={gradientId} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity={0.3} />
          <stop offset="100%" stopColor={color} stopOpacity={0} />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#${gradientId})`} />
      <path
        d={line}
        fill="none"
        stroke={color}
        strokeWidth={strokeWidth}
        strokeLinejoin="round"
        strokeLinecap="round"
      />
      <circle cx={x(n - 1)} cy={y(values[n - 1])} r={strokeWidth + 0.5} fill={color} />
    </svg>
  );
}
