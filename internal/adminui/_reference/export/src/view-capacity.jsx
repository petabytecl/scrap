// Capacity view — disk usage heatmap, runway, guard bands, per-member breakdown

const CapacityView = () => {
  const { navigate } = window.useNav();
  const r = MOCK.CAPACITY_RUNWAY;
  const members = MOCK.K8S_MEMBERS;
  const gb = r.disk_guard_bands;
  const totalCap = r.local_disk.total_bytes;

  const goToMember = (m) => navigate('members', m.pod_name, { memberId: m.id });

  const bands = [
    { label: 'admission', bytes: gb.admission_min_free, color: '#FBBF24' },
    { label: 'warning', bytes: gb.warning_min_free, color: '#FB923C' },
    { label: 'critical', bytes: gb.critical_min_free, color: '#EF4444' },
    { label: 'fail-closed', bytes: gb.fail_closed_min_free, color: '#DC2626' },
  ];

  return (
    <div>
      <SectionHeader eyebrow="Storage" title="Capacity and runway"/>

      {/* Runway headline */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 20, marginBottom: 36 }}>
        <MetricCard eyebrow="Runway" value={`${r.runway_days} days`} color={r.runway_days < 7 ? '#FBBF24' : '#34D399'} sub={`at ${fmt.bytesRate(r.estimated_bytes_per_day / 86400)} sustained`}/>
        <MetricCard eyebrow="Free" value={fmt.bytes(r.usable_bytes_remaining)} sub={`of ${fmt.bytes(totalCap)}`}/>
        <MetricCard eyebrow="Reserved" value={fmt.bytes(r.local_disk.reserved_bytes)} sub="admission guard band"/>
        <MetricCard eyebrow="Ingress / day" value={fmt.bytes(r.estimated_bytes_per_day)} sub="estimated daily"/>
      </div>

      {/* Guard bands visualization */}
      <Card style={{ marginBottom: 36 }}>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 20 }}>DISK GUARD BANDS</div>
        <div style={{ position: 'relative', height: 40, background: 'var(--pb-muted)', borderRadius: 6, overflow: 'hidden', marginBottom: 8 }}>
          <div style={{
            position: 'absolute', left: 0, top: 0, bottom: 0,
            width: `${(r.local_disk.used_bytes / totalCap) * 100}%`,
            background: 'var(--pb-primary)', borderRadius: '6px 0 0 6px',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: '#fff', fontWeight: 600,
          }}>
            {fmt.pct(r.local_disk.used_bytes / totalCap)} used
          </div>
          {bands.map(b => (
            <div key={b.label} style={{
              position: 'absolute', right: `${(b.bytes / totalCap) * 100}%`, top: 0, bottom: 0,
              width: 2, background: b.color, opacity: 0.8,
            }}></div>
          ))}
        </div>
        <div style={{ display: 'flex', gap: 20, flexWrap: 'wrap', fontSize: 11, fontFamily: 'var(--pb-font-mono)' }}>
          {bands.map(b => (
            <div key={b.label} style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <div style={{ width: 10, height: 3, background: b.color, borderRadius: 2 }}></div>
              <span style={{ color: 'var(--pb-fg4)' }}>{b.label}</span>
              <span style={{ color: 'var(--pb-fg3)' }}>{fmt.bytes(b.bytes)}</span>
            </div>
          ))}
        </div>
      </Card>

      {/* Member heatmap — clickable */}
      <Card style={{ marginBottom: 36 }}>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 20 }}>MEMBER CAPACITY HEATMAP</div>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
          {members.map(m => {
            const ratio = m.bytes_used / m.bytes_capacity;
            const hue = ratio > 0.85 ? 0 : ratio > 0.7 ? 40 : 200;
            const light = 25 + ratio * 15;
            return (
              <button key={m.id} onClick={() => goToMember(m)} style={{
                width: 110, height: 110, borderRadius: 'var(--pb-radius-md)',
                background: `oklch(${light}% ${(ratio > 0.5 ? 70 : 50) / 100 * 0.15} ${hue})`,
                border: '1px solid var(--pb-border)',
                display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
                gap: 4, padding: 8, cursor: 'pointer',
                transition: 'transform 150ms, border-color 150ms, box-shadow 150ms',
              }}
              onMouseEnter={e => { e.currentTarget.style.transform = 'scale(1.06)'; e.currentTarget.style.borderColor = 'var(--pb-primary)'; e.currentTarget.style.boxShadow = 'var(--pb-shadow-glow-blue)'; }}
              onMouseLeave={e => { e.currentTarget.style.transform = 'scale(1)'; e.currentTarget.style.borderColor = 'var(--pb-border)'; e.currentTarget.style.boxShadow = 'none'; }}
              title={`${m.pod_name}: ${fmt.pct(ratio)} used — click to view details`}
              >
                <span style={{ fontFamily: 'var(--pb-font-mono)', fontWeight: 600, color: 'var(--pb-fg1)', fontSize: 15 }}>
                  {fmt.pct(ratio)}
                </span>
                <span style={{ fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg2)', fontSize: 11 }}>
                  {m.pod_name.replace('scrap-storage-', 'pod-')}
                </span>
                <span style={{ fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', fontSize: 9, letterSpacing: '0.04em' }}>
                  {m.node}
                </span>
              </button>
            );
          })}
        </div>
      </Card>

      {/* Per-member table — rows clickable */}
      <SectionHeader eyebrow="Per member" title="Per-member storage"/>
      <Table
        columns={[
          { key: 'id', label: 'Member', render: r => (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <StatusDot status={r.state} size={7}/>
              <span style={{ fontFamily: 'var(--pb-font-mono)', fontWeight: 500, color: 'var(--pb-primary)' }}>{r.pod_name || r.id}</span>
              {r.cordoned && <Badge color="#FBBF24">cordoned</Badge>}
            </div>
          )},
          { key: 'node', label: 'Node', render: r => <span style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 12 }}>{r.node}</span> },
          { key: 'zone', label: 'Zone', render: r => <span style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 12, color: 'var(--pb-fg3)' }}>{r.zone || '—'}</span> },
          { key: 'used', label: 'Used', align: 'right', render: r => fmt.bytes(r.bytes_used) },
          { key: 'capacity', label: 'Capacity', align: 'right', render: r => fmt.bytes(r.bytes_capacity) },
          { key: 'pct', label: 'Usage', align: 'right', render: r => (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, justifyContent: 'flex-end' }}>
              <div style={{ width: 80 }}>
                <ProgressBar value={r.bytes_used} max={r.bytes_capacity} color="var(--pb-primary)" height={4} thresholds={{ warning: 70, critical: 85 }}/>
              </div>
              <span style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, minWidth: 44, textAlign: 'right' }}>{fmt.pct(r.bytes_used / r.bytes_capacity)}</span>
            </div>
          )},
          { key: 'runway', label: 'Runway', align: 'right', render: r => <span style={{ fontFamily: 'var(--pb-font-mono)' }}>{r.disk_runway_days}d</span> },
          { key: 'shards', label: 'Shards', align: 'right', render: r => r.shard_count },
        ]}
        data={members}
        onRowClick={(row) => goToMember(row)}
      />
    </div>
  );
};

window.CapacityView = CapacityView;
