// Repair & corruption view — repair queue, corruption incidents, quarantine

const RepairView = () => {
  const queue = MOCK.REPAIR_QUEUE;

  return (
    <div>
      <SectionHeader eyebrow="Durability" title="Repair and corruption"/>

      {/* Top metrics */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 16, marginBottom: 28 }}>
        <MetricCard eyebrow="Repair queue" value={queue.length} sub={queue.length === 0 ? 'Empty' : `oldest: ${fmt.ago(Math.min(...queue.map(q => q.detected_at)))}`} color={queue.length > 0 ? '#FBBF24' : '#34D399'}/>
        <MetricCard eyebrow="Corruption (24h)" value="0" sub="all-sources-corrupt" color="#34D399"/>
        <MetricCard eyebrow="Quarantined" value="0" sub="sources quarantined"/>
        <MetricCard eyebrow="Integrity failures" value="0" sub="responses returned" color="#34D399"/>
      </div>

      {/* Repair queue */}
      {queue.length > 0 && (
        <div style={{ marginBottom: 28 }}>
          <SectionHeader eyebrow="Queue" title="Pending repairs"/>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {queue.map((item, i) => (
              <Card key={i} style={{ padding: '14px 20px' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                  <div style={{
                    width: 32, height: 32, borderRadius: 'var(--pb-radius-md)',
                    background: item.reason === 'checksum_mismatch' ? 'rgba(239,68,68,0.1)' : 'rgba(251,191,36,0.1)',
                    display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
                    color: item.reason === 'checksum_mismatch' ? '#EF4444' : '#FBBF24',
                  }}>
                    <AdminIcon path={ADMIN_ICONS.repair} size={16}/>
                  </div>

                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 2 }}>
                      <span style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 13, fontWeight: 500, color: 'var(--pb-fg1)' }}>
                        {item.target.type === 'block' ? `${item.target.shard_id} / ${item.target.block_id.slice(0, 16)}...` : `${item.target.tenant_id} / ${item.target.document_name}`}
                      </span>
                      <Badge color={item.reason === 'checksum_mismatch' ? '#EF4444' : item.reason === 'under_replicated' ? '#FBBF24' : '#94A3B8'}>
                        {item.reason.replace('_', ' ')}
                      </Badge>
                    </div>
                    <div style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>
                      {item.target.type} · detected {fmt.ago(item.detected_at)}
                    </div>
                  </div>
                </div>
              </Card>
            ))}
          </div>
        </div>
      )}

      {/* Corruption dashboard panels */}
      <SectionHeader eyebrow="Incidents" title="Corruption checks"/>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 28 }}>
        <Card>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>CHECKSUM MISMATCHES (24H)</div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
            <div style={{ fontSize: 48, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: 'var(--pb-fg1)' }}>1</div>
            <div style={{ flex: 1 }}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                {[
                  { source: 'local', count: 1 },
                  { source: 'peer', count: 0 },
                  { source: 'backend', count: 0 },
                ].map(s => (
                  <div key={s.source} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', width: 60 }}>{s.source}</span>
                    <div style={{ flex: 1 }}>
                      <ProgressBar value={s.count} max={1 || 1} color={s.count > 0 ? '#FBBF24' : 'var(--pb-primary)'} height={4}/>
                    </div>
                    <span style={{ fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: s.count > 0 ? '#FBBF24' : 'var(--pb-fg3)', width: 20, textAlign: 'right' }}>{s.count}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </Card>

        <Card>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>REPAIR OUTCOMES (7D)</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {[
              { label: 'Succeeded', count: 47, color: '#34D399' },
              { label: 'Failed (retrying)', count: 2, color: '#FBBF24' },
              { label: 'Blocked', count: 0, color: '#EF4444' },
            ].map(r => (
              <div key={r.label} style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <StatusDot status={r.count === 0 && r.label !== 'Succeeded' ? 'healthy' : r.label === 'Succeeded' ? 'healthy' : 'degraded'} size={7}/>
                <span style={{ fontSize: 13, color: 'var(--pb-fg2)', flex: 1 }}>{r.label}</span>
                <span style={{ fontSize: 16, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: r.color }}>{r.count}</span>
              </div>
            ))}
          </div>
        </Card>
      </div>

      {/* Peer catch-up lag */}
      <Card>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>PEER CATCH-UP LAG</div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(140px, 1fr))', gap: 12 }}>
          {MOCK.STORAGE_MEMBERS.map(m => {
            const lag = m.id === 'member-5' ? 12 : Math.floor(Math.random() * 3);
            return (
              <div key={m.id} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <StatusDot status={lag < 5 ? 'healthy' : lag < 30 ? 'degraded' : 'unavailable'} size={7}/>
                <div>
                  <div style={{ fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg2)' }}>{m.id}</div>
                  <div style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: lag > 5 ? '#FBBF24' : 'var(--pb-fg4)' }}>{lag}s</div>
                </div>
              </div>
            );
          })}
        </div>
      </Card>
    </div>
  );
};

window.RepairView = RepairView;
