// Raft health view — consensus state, per-member lag, term changes

const RaftView = () => {
  const { navigate } = window.useNav();
  const raft = MOCK.RAFT_HEALTH;
  const k8sById = Object.fromEntries(MOCK.K8S_MEMBERS.map(m => [m.id, m]));

  const goToMember = (memberId) => {
    const k = k8sById[memberId];
    if (k) navigate('members', k.pod_name, { memberId: k.id });
  };

  return (
    <div>
      <SectionHeader eyebrow="Consensus" title="Raft health"/>

      {/* Headline status */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 16, marginBottom: 28 }}>
        <MetricCard eyebrow="Leader" value={raft.leader_available ? 'Available' : 'Unavailable'} sub={raft.current_leader} color={raft.leader_available ? '#34D399' : '#EF4444'}/>
        <MetricCard eyebrow="Quorum" value={raft.quorum_available ? 'Healthy' : 'Lost'} color={raft.quorum_available ? '#34D399' : '#EF4444'}/>
        <MetricCard eyebrow="Term" value={raft.current_term} sub={`${raft.term_changes_24h} changes (24h)`}/>
        <MetricCard eyebrow="Proposal failures" value={raft.proposal_failures_1h} sub="last 1h" color={raft.proposal_failures_1h > 0 ? '#EF4444' : '#34D399'}/>
      </div>

      {/* Member consensus state */}
      <SectionHeader eyebrow="Per member" title="Consensus lag by member"/>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 16, marginBottom: 28 }}>
        {raft.members.map(m => {
          const k = k8sById[m.id];
          return (
            <Card key={m.id} onClick={() => goToMember(m.id)} hoverable style={{ cursor: 'pointer' }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 14 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <StatusDot status={m.role === 'leader' ? 'ONLINE' : 'healthy'} size={8}/>
                  <span style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 14, fontWeight: 600, color: 'var(--pb-primary)' }}>{k?.pod_name || m.id}</span>
                </div>
                <Badge color={m.role === 'leader' ? '#0093D0' : '#94A3B8'}>{m.role}</Badge>
              </div>

              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
                {[
                  { label: 'Commit lag', value: m.commit_lag, unit: '', warn: m.commit_lag > 5 },
                  { label: 'Apply lag', value: m.apply_lag, unit: '', warn: m.apply_lag > 5 },
                  { label: 'Snapshot age', value: fmt.duration(m.snapshot_age_s), warn: m.snapshot_age_s > 600 },
                  { label: 'Compaction lag', value: fmt.duration(m.compaction_lag_s), warn: m.compaction_lag_s > 120 },
                ].map(stat => (
                  <div key={stat.label}>
                    <div style={{ fontSize: 10, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', letterSpacing: '0.06em', marginBottom: 2 }}>{stat.label.toUpperCase()}</div>
                    <div style={{ fontSize: 18, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: stat.warn ? '#FBBF24' : 'var(--pb-fg1)' }}>{stat.value}{stat.unit || ''}</div>
                  </div>
                ))}
              </div>

              {k && (
                <div style={{ marginTop: 14, paddingTop: 12, borderTop: '1px solid var(--pb-border)', fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', display: 'flex', justifyContent: 'space-between' }}>
                  <span>{k.node}</span>
                  <span>{k.zone}</span>
                </div>
              )}
            </Card>
          );
        })}
      </div>

      {/* ReadIndex + membership health */}
      <Card>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>CONSENSUS HEALTH CHECKS</div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 20 }}>
          {[
            { label: 'ReadIndex failures (1h)', value: raft.read_index_failures_1h, ok: raft.read_index_failures_1h === 0 },
            { label: 'Proposal failures (1h)', value: raft.proposal_failures_1h, ok: raft.proposal_failures_1h === 0 },
            { label: 'Term changes (24h)', value: raft.term_changes_24h, ok: raft.term_changes_24h < 5 },
          ].map(c => (
            <div key={c.label} style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <StatusDot status={c.ok ? 'healthy' : 'degraded'} size={8}/>
              <div>
                <div style={{ fontSize: 20, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: 'var(--pb-fg1)' }}>{c.value}</div>
                <div style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>{c.label}</div>
              </div>
            </div>
          ))}
        </div>
      </Card>
    </div>
  );
};

window.RaftView = RaftView;
