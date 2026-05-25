// Overview dashboard — cluster summary, key metrics, write throughput, top tenants

const OverviewView = () => {
  const s = MOCK.CLUSTER_SUMMARY;
  const usedPct = s.local_bytes_used / s.local_bytes_capacity;
  const tw = React.useContext(window.TweaksCtx);
  const fmtNum = tw.compactNumbers ? fmt.compact : fmt.number;

  return (
    <div>
      {/* Status strip — system pulse */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 20, marginBottom: 32, padding: '14px 20px', background: 'var(--pb-surface)', borderRadius: 'var(--pb-radius-md)', border: '1px solid var(--pb-border)', flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <StatusDot status={s.write_ack_gate} size={9}/>
          <div>
            <div style={{ fontSize: 13, color: 'var(--pb-fg1)', fontWeight: 500, whiteSpace: 'nowrap' }}>Production write ACK</div>
            <div style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', letterSpacing: '0.04em', whiteSpace: 'nowrap' }}>{s.write_ack_gate} · gate satisfied</div>
          </div>
        </div>
        <div style={{ width: 1, height: 28, background: 'var(--pb-border)' }}></div>
        <div style={{ display: 'flex', gap: 18, fontSize: 12, fontFamily: 'var(--pb-font-mono)' }}>
          {Object.entries(MOCK.CIRCUIT_BREAKERS).map(([k, v]) => (
            <div key={k} style={{ display: 'flex', alignItems: 'center', gap: 6, whiteSpace: 'nowrap' }}>
              <StatusDot status={v.state} size={6}/>
              <span style={{ color: 'var(--pb-fg3)' }}>{k}</span>
              <span style={{ color: 'var(--pb-fg4)', opacity: 0.7 }}>{v.state}</span>
            </div>
          ))}
        </div>
        <div style={{ marginLeft: 'auto', fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', whiteSpace: 'nowrap' }}>
          uptime <span style={{ color: 'var(--pb-fg2)' }}>{Math.floor(s.uptime_seconds / 86400)}d {Math.floor((s.uptime_seconds % 86400) / 3600)}h</span>
        </div>
      </div>

      {/* Top metric cards — 3 columns for breathing room */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 20, marginBottom: 40 }}>
        <MetricCard eyebrow="Documents" value={fmtNum(s.documents_total)} sub={`${fmtNum(s.transactions_total)} transactions`} sparkData={MOCK.WRITE_ADMISSION_24H} color="#0093D0"/>
        <MetricCard eyebrow="Storage" value={fmt.bytes(s.local_bytes_used)} sub={`${fmt.pct(usedPct)} of ${fmt.bytes(s.local_bytes_capacity)}`} color={usedPct > 0.8 ? '#FBBF24' : '#0093D0'}/>
        <MetricCard eyebrow="Degraded" value={fmtNum(s.degraded_document_count)} sub={s.degraded_document_count === 0 ? 'All healthy' : 'Repair queued'} color={s.degraded_document_count > 0 ? '#FBBF24' : '#34D399'}/>
      </div>

      {/* Charts — full width, taller */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20, marginBottom: 40 }}>
        <Card>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>WRITE THROUGHPUT (24H)</div>
          <div style={{ height: 160 }}>
            <AreaChart data={MOCK.WRITE_THROUGHPUT_24H} color="#0093D0" height={160} yFormat={v => fmt.bytesRate(v)}/>
          </div>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 10, fontSize: 10, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>
            <span>24h ago</span><span>12h ago</span><span>now</span>
          </div>
        </Card>
        <Card>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>READ LATENCY P95 (24H)</div>
          <div style={{ height: 160 }}>
            <AreaChart data={MOCK.READ_LATENCY_24H} color="#7DD3FC" height={160} yFormat={v => v.toFixed(1) + 'ms'}/>
          </div>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 10, fontSize: 10, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>
            <span>24h ago</span><span>12h ago</span><span>now</span>
          </div>
        </Card>
      </div>

      {/* Capacity overview */}
      <Card style={{ marginBottom: 40 }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 18 }}>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)' }}>DISK CAPACITY</div>
          <span style={{ fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg3)' }}>
            {fmt.bytes(s.local_bytes_used)} / {fmt.bytes(s.local_bytes_capacity)}
          </span>
        </div>
        <ProgressBar value={usedPct * 100} color="var(--pb-primary)" height={12} showLabel thresholds={{ warning: 70, critical: 85 }}/>
        <div style={{ display: 'flex', gap: 32, marginTop: 18, fontSize: 13, color: 'var(--pb-fg3)' }}>
          <span>Runway: <strong style={{ color: 'var(--pb-fg1)' }}>{MOCK.CAPACITY_RUNWAY.runway_days} days</strong></span>
          <span>Ingress: <strong style={{ color: 'var(--pb-fg1)' }}>{fmt.bytesRate(MOCK.CAPACITY_RUNWAY.estimated_bytes_per_day / 86400)}</strong></span>
          <span>Members: <strong style={{ color: 'var(--pb-fg1)' }}>{s.storage_member_count}</strong> · Shards: <strong style={{ color: 'var(--pb-fg1)' }}>{s.shard_count}</strong></span>
        </div>
      </Card>

      {/* Bottom row: document distribution + top tenants */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        {/* Document distribution */}
        <Card>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 24 }}>DOCUMENT DISTRIBUTION</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
            <div>
              <div style={{ fontSize: 12, color: 'var(--pb-fg3)', marginBottom: 8, fontFamily: 'var(--pb-font-mono)', letterSpacing: '0.04em' }}>By class</div>
              <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                <div style={{ flex: MOCK.DOC_CLASS_DIST.permanent, height: 28, background: '#0093D0', borderRadius: '6px 0 0 6px', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: '#fff', fontWeight: 600 }}>permanent</div>
                <div style={{ flex: MOCK.DOC_CLASS_DIST.ephemeral, height: 28, background: '#6366F1', borderRadius: '0 6px 6px 0', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: '#fff', fontWeight: 600 }}>ephemeral</div>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 6, fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>
                <span>{fmtNum(MOCK.DOC_CLASS_DIST.permanent)}</span>
                <span>{fmtNum(MOCK.DOC_CLASS_DIST.ephemeral)}</span>
              </div>
            </div>
            <div>
              <div style={{ fontSize: 12, color: 'var(--pb-fg3)', marginBottom: 8, fontFamily: 'var(--pb-font-mono)', letterSpacing: '0.04em' }}>By priority</div>
              <div style={{ display: 'flex', gap: 0, alignItems: 'center' }}>
                {[
                  { key: 'critical_ingest', label: 'critical', color: '#EF4444' },
                  { key: 'normal', label: 'normal', color: '#0093D0' },
                  { key: 'bulk', label: 'bulk', color: '#94A3B8' },
                ].map((p, i) => (
                  <div key={p.key} style={{
                    flex: MOCK.PRIORITY_DIST[p.key], height: 28, background: p.color,
                    borderRadius: i === 0 ? '6px 0 0 6px' : i === 2 ? '0 6px 6px 0' : '0',
                    display: 'flex', alignItems: 'center', justifyContent: 'center',
                    fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: '#fff', fontWeight: 600, minWidth: 44,
                  }}>{p.label}</div>
                ))}
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 6, fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>
                <span>{fmtNum(MOCK.PRIORITY_DIST.critical_ingest)}</span>
                <span>{fmtNum(MOCK.PRIORITY_DIST.normal)}</span>
                <span>{fmtNum(MOCK.PRIORITY_DIST.bulk)}</span>
              </div>
            </div>
          </div>
        </Card>

        {/* Top tenants */}
        <Card>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 24 }}>TOP TENANTS</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            {MOCK.TOP_TENANTS.map((t, i) => (
              <div key={t.id} style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
                <span style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 12, color: 'var(--pb-fg4)', width: 20 }}>{String(i + 1).padStart(2, '0')}</span>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontSize: 14, color: 'var(--pb-fg1)', fontWeight: 500, marginBottom: 4 }}>{t.id}</div>
                  <ProgressBar value={t.bytes} max={MOCK.TOP_TENANTS[0].bytes} color="var(--pb-primary)" height={4}/>
                </div>
                <div style={{ textAlign: 'right', flexShrink: 0 }}>
                  <div style={{ fontSize: 13, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg2)' }}>{fmtNum(t.documents)}</div>
                  <div style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>{fmt.bytes(t.bytes)}</div>
                </div>
              </div>
            ))}
          </div>
        </Card>
      </div>
    </div>
  );
};

window.OverviewView = OverviewView;
