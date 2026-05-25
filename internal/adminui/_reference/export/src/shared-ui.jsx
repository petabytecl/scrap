// Shared UI primitives for the S.C.R.A.P. admin dashboard

const { useState, useEffect, useRef, useMemo } = React;

/* ─── Formatting helpers ────────────────────────────────────────── */
const fmt = {
  bytes(b) {
    if (b == null) return '—';
    if (b < 1024) return b + ' B';
    if (b < 1_048_576) return (b / 1024).toFixed(1) + ' KiB';
    if (b < 1_073_741_824) return (b / 1_048_576).toFixed(1) + ' MiB';
    if (b < 1_099_511_627_776) return (b / 1_073_741_824).toFixed(1) + ' GiB';
    return (b / 1_099_511_627_776).toFixed(2) + ' TiB';
  },
  bytesRate(b) { return fmt.bytes(b) + '/s'; },
  number(n) {
    if (n == null) return '—';
    return n.toLocaleString('en-US');
  },
  compact(n) {
    if (n == null) return '—';
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
    if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
    return String(n);
  },
  pct(ratio) { return (ratio * 100).toFixed(1) + '%'; },
  duration(s) {
    if (s < 60) return s + 's';
    if (s < 3600) return Math.floor(s / 60) + 'm ' + (s % 60) + 's';
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    return h + 'h ' + m + 'm';
  },
  ago(ts) {
    const diff = Date.now() - ts;
    if (diff < 60_000) return Math.floor(diff / 1000) + 's ago';
    if (diff < 3_600_000) return Math.floor(diff / 60_000) + 'm ago';
    if (diff < 86_400_000) return Math.floor(diff / 3_600_000) + 'h ago';
    return Math.floor(diff / 86_400_000) + 'd ago';
  },
  sha(s) { return s ? s.slice(0, 10) : '—'; },
};

/* ─── Status badge ──────────────────────────────────────────────── */
const statusColors = {
  ONLINE: '#34D399', DRAINING: '#FBBF24', OFFLINE: '#EF4444',
  healthy: '#34D399', degraded: '#FBBF24', unavailable: '#EF4444',
  RUNNING: '#0093D0', QUEUED: '#FBBF24', SUCCEEDED: '#34D399',
  FAILED: '#EF4444', CANCELED: '#94A3B8', EXPIRED: '#64748B',
  PLANNED: '#7DD3FC',
  closed: '#34D399', open: '#EF4444', half_open: '#FBBF24',
  enabled: '#34D399', disabled: '#EF4444',
  leader: '#0093D0', follower: '#94A3B8',
};

const StatusDot = ({ status, size = 8 }) => {
  const color = statusColors[status] || '#64748B';
  return (
    <span style={{
      display: 'inline-block', width: size, height: size,
      borderRadius: '50%', background: color, flexShrink: 0,
      boxShadow: status === 'ONLINE' || status === 'healthy' || status === 'enabled' || status === 'closed'
        ? `0 0 6px ${color}60` : 'none',
    }}></span>
  );
};

const Badge = ({ children, color = '#94A3B8', bg }) => (
  <span style={{
    display: 'inline-flex', alignItems: 'center', gap: 5,
    padding: '2px 8px', borderRadius: 'var(--pb-radius-full)',
    fontSize: 10.5, fontFamily: 'var(--pb-font-mono)',
    fontWeight: 500, letterSpacing: '0.05em',
    color, background: bg || (color + '15'),
    border: `1px solid ${color}25`,
    lineHeight: '16px', whiteSpace: 'nowrap',
    fontVariantNumeric: 'tabular-nums',
  }}>{children}</span>
);

const StatusBadge = ({ status }) => {
  const color = statusColors[status] || '#64748B';
  const label = typeof status === 'string' ? status.toLowerCase() : status;
  return <Badge color={color}><StatusDot status={status} size={6}/> {label}</Badge>;
};

/* ─── Card ──────────────────────────────────────────────────────── */
const Card = ({ children, style = {}, onClick, hoverable = false }) => (
  <div onClick={onClick} style={{
    background: 'var(--pb-surface)', border: '1px solid var(--pb-border)',
    borderRadius: 'var(--pb-radius-lg)', padding: 28,
    transition: 'box-shadow 250ms var(--pb-ease-standard), border-color 150ms, transform 150ms',
    cursor: onClick ? 'pointer' : 'default',
    ...style,
  }}
  onMouseEnter={e => { if (hoverable || onClick) { e.currentTarget.style.boxShadow = 'var(--pb-shadow-lg)'; e.currentTarget.style.borderColor = '#475569'; }}}
  onMouseLeave={e => { if (hoverable || onClick) { e.currentTarget.style.boxShadow = 'none'; e.currentTarget.style.borderColor = 'var(--pb-border)'; }}}
  >{children}</div>
);

/* ─── Metric card ───────────────────────────────────────────────── */
const MetricCard = ({ eyebrow, value, sub, trend, color, sparkData, style = {} }) => {
  const tw = window.TweaksCtx ? React.useContext(window.TweaksCtx) : {};
  const showSpark = tw.showSparklines !== false;
  return (
    <Card style={{ padding: '22px 26px', ...style }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 12 }}>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{
            fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500,
            letterSpacing: '0.2em', textTransform: 'uppercase',
            color: color || 'var(--pb-primary)', marginBottom: 10,
            opacity: 0.92,
          }}>{eyebrow}</div>
          <div style={{
            fontFamily: 'var(--pb-font-heading)', fontSize: 30, fontWeight: 700,
            color: 'var(--pb-fg1)', lineHeight: 1.05, letterSpacing: '-0.02em',
            fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap',
            overflow: 'hidden', textOverflow: 'ellipsis',
          }}>{value}</div>
          {sub && <div style={{ fontSize: 13, color: 'var(--pb-fg3)', marginTop: 6, fontVariantNumeric: 'tabular-nums' }}>{sub}</div>}
        </div>
        {sparkData && showSpark && <Sparkline data={sparkData} color={color} />}
      </div>
    </Card>
  );
};

/* ─── Sparkline (SVG) ───────────────────────────────────────────── */
const Sparkline = ({ data, width = 60, height = 28, color = 'var(--pb-primary)' }) => {
  if (!data || !data.length) return null;
  const max = Math.max(...data);
  const min = Math.min(...data);
  const range = max - min || 1;
  const points = data.map((v, i) => {
    const x = (i / (data.length - 1)) * width;
    const y = height - ((v - min) / range) * (height - 4) - 2;
    return `${x},${y}`;
  }).join(' ');
  return (
    <svg width={width} height={height} style={{ flexShrink: 0 }}>
      <polyline fill="none" stroke={color} strokeWidth="1.5" points={points} strokeLinejoin="round" strokeLinecap="round"/>
    </svg>
  );
};

/* ─── Progress bar ──────────────────────────────────────────────── */
const ProgressBar = ({ value, max = 100, color = 'var(--pb-primary)', height = 6, showLabel = false, thresholds }) => {
  const pct = Math.min(value / max * 100, 100);
  const barColor = thresholds
    ? (pct > thresholds.critical ? '#EF4444' : pct > thresholds.warning ? '#FBBF24' : color)
    : color;
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%' }}>
      <div style={{ flex: 1, height, background: 'var(--pb-muted)', borderRadius: height / 2, overflow: 'hidden', position: 'relative' }}>
        {thresholds && (
          <>
            <div style={{ position: 'absolute', left: `${thresholds.warning}%`, top: 0, bottom: 0, width: 1, background: '#FBBF2440' }}></div>
            <div style={{ position: 'absolute', left: `${thresholds.critical}%`, top: 0, bottom: 0, width: 1, background: '#EF444440' }}></div>
          </>
        )}
        <div style={{
          height: '100%', width: `${pct}%`, background: barColor,
          borderRadius: height / 2, transition: 'width 400ms var(--pb-ease-standard)',
        }}></div>
      </div>
      {showLabel && <span style={{ fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: barColor, minWidth: 40, textAlign: 'right' }}>{pct.toFixed(1)}%</span>}
    </div>
  );
};

/* ─── Section header ────────────────────────────────────────────── */
const SectionHeader = ({ eyebrow, title, right }) => (
  <div style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', marginBottom: 24, gap: 16 }}>
    <div>
      {eyebrow && <div style={{
        fontFamily: 'var(--pb-font-mono)', fontSize: 12, fontWeight: 500,
        letterSpacing: '0.2em', textTransform: 'uppercase',
        color: 'var(--pb-primary)', marginBottom: 6,
      }}>{eyebrow}</div>}
      <h2 style={{
        fontFamily: 'var(--pb-font-heading)', fontSize: 24, fontWeight: 700,
        color: 'var(--pb-fg1)', margin: 0, lineHeight: 1.3,
      }}>{title}</h2>
    </div>
    {right && <div>{right}</div>}
  </div>
);

/* ─── Card header (used inside Card) ───────────────────────────── */
const CardHeader = ({ eyebrow, title, right, sub }) => (
  <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 20, gap: 12 }}>
    <div style={{ minWidth: 0 }}>
      {eyebrow && <div style={{
        fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500,
        letterSpacing: '0.2em', textTransform: 'uppercase',
        color: 'var(--pb-primary)', marginBottom: title ? 6 : 0,
      }}>{eyebrow}</div>}
      {title && <div style={{
        fontFamily: 'var(--pb-font-heading)', fontSize: 16, fontWeight: 600,
        color: 'var(--pb-fg1)', lineHeight: 1.3,
      }}>{title}</div>}
      {sub && <div style={{ fontSize: 12, color: 'var(--pb-fg4)', marginTop: 4 }}>{sub}</div>}
    </div>
    {right && <div style={{ flexShrink: 0 }}>{right}</div>}
  </div>
);

/* ─── Table ─────────────────────────────────────────────────────── */
const Table = ({ columns, data, onRowClick }) => (
  <div style={{ overflowX: 'auto', borderRadius: 'var(--pb-radius-lg)', border: '1px solid var(--pb-border)', background: 'var(--pb-surface)' }}>
    <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13, fontFamily: 'var(--pb-font-sans)' }}>
      <thead>
        <tr>
          {columns.map(c => (
            <th key={c.key} style={{
              textAlign: c.align || 'left', padding: '12px 18px',
              fontFamily: 'var(--pb-font-mono)', fontSize: 10.5,
              fontWeight: 500, letterSpacing: '0.16em', textTransform: 'uppercase',
              color: 'var(--pb-fg4)', background: 'var(--pb-background-alt)',
              borderBottom: '1px solid var(--pb-border)', whiteSpace: 'nowrap',
            }}>{c.label}</th>
          ))}
        </tr>
      </thead>
      <tbody style={{ fontVariantNumeric: 'tabular-nums' }}>
        {data.map((row, i) => (
          <tr key={i} onClick={() => onRowClick && onRowClick(row)}
            style={{ cursor: onRowClick ? 'pointer' : 'default', transition: 'background 150ms' }}
            onMouseEnter={e => e.currentTarget.style.background = 'rgba(148,163,184,0.04)'}
            onMouseLeave={e => e.currentTarget.style.background = 'transparent'}
          >
            {columns.map(c => (
              <td key={c.key} style={{
                padding: '12px 18px', borderBottom: i === data.length - 1 ? 'none' : '1px solid var(--pb-border-subtle)',
                color: 'var(--pb-fg2)', textAlign: c.align || 'left', whiteSpace: 'nowrap',
              }}>{c.render ? c.render(row) : row[c.key]}</td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  </div>
);

/* ─── Heatmap grid ──────────────────────────────────────────────── */
const HeatmapCell = ({ value, max, label, sub, size = 80 }) => {
  const ratio = value / max;
  const hue = ratio > 0.85 ? 0 : ratio > 0.7 ? 40 : 200;
  const sat = ratio > 0.5 ? 70 : 50;
  const light = 25 + ratio * 15;
  return (
    <div style={{
      width: size, height: size, borderRadius: 'var(--pb-radius-md)',
      background: `oklch(${light}% ${sat / 100 * 0.15} ${hue})`,
      border: '1px solid var(--pb-border)',
      display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
      gap: 2, padding: 4, fontSize: 11, transition: 'transform 150ms',
      cursor: 'default',
    }}
    onMouseEnter={e => e.currentTarget.style.transform = 'scale(1.06)'}
    onMouseLeave={e => e.currentTarget.style.transform = 'scale(1)'}
    title={`${label}: ${fmt.pct(ratio)} used`}
    >
      <span style={{ fontFamily: 'var(--pb-font-mono)', fontWeight: 600, color: 'var(--pb-fg1)', fontSize: 13 }}>
        {fmt.pct(ratio)}
      </span>
      <span style={{ fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg3)', fontSize: 10, letterSpacing: '0.04em' }}>
        {sub || label}
      </span>
    </div>
  );
};

/* ─── Area chart (SVG) ──────────────────────────────────────────── */
const AreaChart = ({ data, width = 400, height = 120, color = '#0093D0', labels, yFormat }) => {
  const max = Math.max(...data) * 1.1 || 1;
  const points = data.map((v, i) => {
    const x = (i / (data.length - 1)) * (width - 8) + 4;
    const y = height - (v / max) * (height - 24) - 12;
    return [x, y];
  });
  const linePath = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${p[0]},${p[1]}`).join(' ');
  const areaPath = linePath + ` L${width - 4},${height - 4} L4,${height - 4} Z`;
  const gradId = `grad-${color.replace('#','')}`;
  return (
    <svg viewBox={`0 0 ${width} ${height}`} style={{ width: '100%', height: 'auto', display: 'block' }} preserveAspectRatio="none">
      <defs>
        <linearGradient id={gradId} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.28"/>
          <stop offset="100%" stopColor={color} stopOpacity="0.02"/>
        </linearGradient>
      </defs>
      {/* Subtle horizontal grid */}
      <line x1="4" y1={height / 2} x2={width - 4} y2={height / 2} stroke="rgba(148,163,184,0.08)" strokeDasharray="2,4"/>
      <path d={areaPath} fill={`url(#${gradId})`}/>
      <path d={linePath} fill="none" stroke={color} strokeWidth="1.75" strokeLinejoin="round" strokeLinecap="round"/>
      {/* End-point dot */}
      <circle cx={points[points.length - 1][0]} cy={points[points.length - 1][1]} r="3" fill={color}/>
      <circle cx={points[points.length - 1][0]} cy={points[points.length - 1][1]} r="6" fill={color} opacity="0.2"/>
      {/* Y-axis label (max only) */}
      <text x="4" y="11" fill="#64748B" fontSize="9.5" fontFamily="var(--pb-font-mono)" letterSpacing="0.04em">{yFormat ? yFormat(max / 1.1) : Math.round(max / 1.1)}</text>
    </svg>
  );
};

/* ─── Export ─────────────────────────────────────────────────────── */
Object.assign(window, {
  fmt, StatusDot, Badge, StatusBadge,
  Card, CardHeader, MetricCard, Sparkline, ProgressBar,
  SectionHeader, Table, HeatmapCell, AreaChart,
});
