// Operations view — durable admin/background operations list and detail

const OperationsView = () => {
  const [filter, setFilter] = useState('all');
  const ops = MOCK.OPERATIONS;
  const filtered = filter === 'all' ? ops : ops.filter(o => o.state === filter);

  const stateCounts = {};
  ops.forEach(o => { stateCounts[o.state] = (stateCounts[o.state] || 0) + 1; });

  return (
    <div>
      <SectionHeader eyebrow="Control plane" title="Operations"/>

      {/* State filter tabs */}
      <div style={{ display: 'flex', gap: 6, marginBottom: 24, flexWrap: 'wrap' }}>
        {['all', 'RUNNING', 'QUEUED', 'SUCCEEDED', 'FAILED', 'CANCELED', 'EXPIRED'].map(s => {
          const isActive = filter === s;
          const count = s === 'all' ? ops.length : (stateCounts[s] || 0);
          return (
            <button key={s} onClick={() => setFilter(s)} style={{
              display: 'flex', alignItems: 'center', gap: 6,
              padding: '6px 14px', borderRadius: 'var(--pb-radius-full)',
              border: `1px solid ${isActive ? 'var(--pb-primary)' : 'var(--pb-border)'}`,
              background: isActive ? 'rgba(0,147,208,0.12)' : 'transparent',
              color: isActive ? 'var(--pb-primary)' : 'var(--pb-fg3)',
              fontSize: 12, fontFamily: 'var(--pb-font-mono)', fontWeight: 500,
              cursor: 'pointer', transition: 'all 150ms',
            }}
            onMouseEnter={e => { if (!isActive) e.currentTarget.style.borderColor = 'var(--pb-fg4)'; }}
            onMouseLeave={e => { if (!isActive) e.currentTarget.style.borderColor = 'var(--pb-border)'; }}
            >
              {s === 'all' ? 'All' : s.charAt(0) + s.slice(1).toLowerCase()}
              <span style={{ fontSize: 10, opacity: 0.7, marginLeft: 2 }}>{count}</span>
            </button>
          );
        })}
      </div>

      {/* Operations list */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        {filtered.map(op => (
          <OperationCard key={op.id} op={op}/>
        ))}
        {filtered.length === 0 && (
          <div style={{ textAlign: 'center', padding: 40, color: 'var(--pb-fg4)', fontFamily: 'var(--pb-font-mono)', fontSize: 13 }}>
            No operations matching filter
          </div>
        )}
      </div>
    </div>
  );
};

const OperationCard = ({ op }) => {
  const [expanded, setExpanded] = useState(false);
  const pct = op.progress.total > 0 ? (op.progress.completed / op.progress.total * 100) : 0;

  return (
    <Card onClick={() => setExpanded(!expanded)} hoverable style={{ padding: '16px 20px' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        {/* Type icon */}
        <div style={{
          width: 36, height: 36, borderRadius: 'var(--pb-radius-md)',
          background: 'rgba(0,147,208,0.1)', display: 'flex', alignItems: 'center', justifyContent: 'center',
          flexShrink: 0,
        }}>
          <AdminIcon path={opTypeIcon(op.type)} size={18}/>
        </div>

        {/* Main info */}
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 2 }}>
            <span style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 13, fontWeight: 600, color: 'var(--pb-fg1)' }}>{op.type}</span>
            <StatusBadge status={op.state}/>
          </div>
          <div style={{ fontSize: 12, color: 'var(--pb-fg3)', fontFamily: 'var(--pb-font-mono)' }}>
            {op.id} · {op.requested_by} · {fmt.ago(op.requested_at)}
          </div>
        </div>

        {/* Progress */}
        {op.state === 'RUNNING' && (
          <div style={{ width: 120, flexShrink: 0 }}>
            <ProgressBar value={pct} color="var(--pb-primary)" height={4} showLabel/>
          </div>
        )}

        {/* Chevron */}
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--pb-fg4)" strokeWidth="2" style={{
          transform: expanded ? 'rotate(180deg)' : 'rotate(0)',
          transition: 'transform 200ms', flexShrink: 0,
        }}>
          <path strokeLinecap="round" strokeLinejoin="round" d="m19.5 8.25-7.5 7.5-7.5-7.5"/>
        </svg>
      </div>

      {/* Expanded detail */}
      {expanded && (
        <div style={{ marginTop: 14, paddingTop: 14, borderTop: '1px solid var(--pb-border)', display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, fontSize: 12 }}>
          <div>
            <span style={{ color: 'var(--pb-fg4)', fontFamily: 'var(--pb-font-mono)', fontSize: 11 }}>Progress</span>
            <div style={{ color: 'var(--pb-fg2)', marginTop: 2 }}>{op.progress.completed}/{op.progress.total} — {op.progress.message}</div>
          </div>
          {op.started_at && (
            <div>
              <span style={{ color: 'var(--pb-fg4)', fontFamily: 'var(--pb-font-mono)', fontSize: 11 }}>Started</span>
              <div style={{ color: 'var(--pb-fg2)', marginTop: 2 }}>{fmt.ago(op.started_at)}</div>
            </div>
          )}
          {op.finished_at && (
            <div>
              <span style={{ color: 'var(--pb-fg4)', fontFamily: 'var(--pb-font-mono)', fontSize: 11 }}>Duration</span>
              <div style={{ color: 'var(--pb-fg2)', marginTop: 2 }}>{fmt.duration(Math.floor((op.finished_at - op.started_at) / 1000))}</div>
            </div>
          )}
          {op.targets.length > 0 && (
            <div>
              <span style={{ color: 'var(--pb-fg4)', fontFamily: 'var(--pb-font-mono)', fontSize: 11 }}>Targets</span>
              <div style={{ color: 'var(--pb-fg2)', marginTop: 2, fontFamily: 'var(--pb-font-mono)', fontSize: 12 }}>{op.targets.map(t => `${t.type}:${t.id}`).join(', ')}</div>
            </div>
          )}
        </div>
      )}
    </Card>
  );
};

function opTypeIcon(type) {
  const map = {
    scrub: ADMIN_ICONS.repair,
    repair: ADMIN_ICONS.repair,
    drain: ADMIN_ICONS.members,
    key_rotation: ADMIN_ICONS.openbao,
    capacity_override: ADMIN_ICONS.capacity,
    restore: ADMIN_ICONS.backend,
  };
  return map[type] || ADMIN_ICONS.operations;
}

window.OperationsView = OperationsView;
