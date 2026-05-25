// Members view — list + full-page detail with breadcrumb navigation

const MembersView = () => {
  const { route, navigate } = window.useNav();
  const members = MOCK.K8S_MEMBERS;

  // Sub-page: member detail
  if (route.params.memberId) {
    const member = members.find(m => m.id === route.params.memberId);
    if (member) return <MemberDetailPage member={member}/>;
  }

  return <MemberListPage members={members} onSelect={(m) => navigate('members', m.pod_name, { memberId: m.id })}/>;
};

/* ═══════════════════════════════════════════════════════════════════
   LIST PAGE
   ═══════════════════════════════════════════════════════════════════ */
const MemberListPage = ({ members, onSelect }) => (
  <div>
    {/* Zone topology */}
    <Card style={{ marginBottom: 32 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 18 }}>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)' }}>ZONE TOPOLOGY</div>
        <Badge color="#94A3B8">PDB: maxUnavailable={MOCK.PDB_STATUS.max_unavailable} · {MOCK.PDB_STATUS.disruptions_allowed} disruption allowed</Badge>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 16 }}>
        {['cl-stgo-1a', 'cl-stgo-1b', 'cl-stgo-1c'].map(zone => {
          const zm = members.filter(m => m.zone === zone);
          return (
            <div key={zone} style={{ background: 'var(--pb-background)', borderRadius: 'var(--pb-radius-md)', padding: '16px 18px', border: '1px solid var(--pb-border-subtle)' }}>
              <div style={{ fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', marginBottom: 12, letterSpacing: '0.06em' }}>{zone}</div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                {zm.map(m => (
                  <button key={m.id} onClick={() => onSelect(m)} style={{
                    display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px',
                    borderRadius: 'var(--pb-radius-sm)', border: '1px solid var(--pb-border)',
                    background: 'var(--pb-surface)', cursor: 'pointer',
                    fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg2)',
                    textAlign: 'left', width: '100%', transition: 'border-color 150ms',
                    minWidth: 0,
                  }}
                  onMouseEnter={e => e.currentTarget.style.borderColor = 'var(--pb-primary)'}
                  onMouseLeave={e => e.currentTarget.style.borderColor = 'var(--pb-border)'}
                  >
                    <StatusDot status={m.state} size={7}/>
                    <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{m.pod_name}</span>
                    {m.cordoned && <Badge color="#FBBF24">cordoned</Badge>}
                    <Badge color={statusColors[m.raft_role]}>{m.raft_role === 'leader' ? 'lead' : 'fol'}</Badge>
                  </button>
                ))}
              </div>
            </div>
          );
        })}
      </div>
    </Card>

    {/* Member cards */}
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(360px, 1fr))', gap: 20, marginBottom: 36 }}>
      {members.map(m => {
        const usedPct = m.bytes_used / m.bytes_capacity;
        return (
          <Card key={m.id} onClick={() => onSelect(m)} hoverable style={{ cursor: 'pointer' }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 18 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <StatusDot status={m.state} size={9}/>
                <span style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 15, fontWeight: 600, color: 'var(--pb-fg1)' }}>{m.pod_name}</span>
              </div>
              <div style={{ display: 'flex', gap: 6 }}>
                <Badge color={statusColors[m.raft_role]}>{m.raft_role}</Badge>
                {m.cordoned && <Badge color="#FBBF24">cordoned</Badge>}
              </div>
            </div>

            {/* Disk bar */}
            <div style={{ marginBottom: 18 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', marginBottom: 6 }}>
                <span>{fmt.bytes(m.bytes_used)}</span>
                <span>{fmt.bytes(m.bytes_capacity)}</span>
              </div>
              <ProgressBar value={usedPct * 100} color="var(--pb-primary)" height={6} showLabel thresholds={{ warning: 70, critical: 85 }}/>
            </div>

            {/* Stats */}
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12 }}>
              {[
                { label: 'Shards', value: m.shard_count },
                { label: 'Runway', value: `${m.disk_runway_days}d` },
                { label: 'Last seen', value: fmt.ago(m.last_seen_at) },
              ].map(s => (
                <div key={s.label} style={{ textAlign: 'center' }}>
                  <div style={{ fontSize: 18, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: 'var(--pb-fg1)' }}>{s.value}</div>
                  <div style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', letterSpacing: '0.06em', marginTop: 2 }}>{s.label}</div>
                </div>
              ))}
            </div>

            <div style={{ marginTop: 16, paddingTop: 14, borderTop: '1px solid var(--pb-border)', display: 'flex', justifyContent: 'space-between', fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>
              <span>{m.node}</span>
              <span>{m.zone}</span>
            </div>
          </Card>
        );
      })}
    </div>

    {/* Placement health */}
    <Card>
      <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 20 }}>PLACEMENT HEALTH</div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 24 }}>
        {[
          { label: 'Distinct nodes', value: members.length, expected: '7 required', ok: members.length >= 7 },
          { label: 'Online members', value: members.filter(m => m.state === 'ONLINE').length, expected: `of ${members.length}`, ok: true },
          { label: 'Cordoned', value: members.filter(m => m.cordoned).length, expected: '0 ideal', ok: members.filter(m => m.cordoned).length === 0 },
          { label: 'Quorum', value: 'Healthy', expected: '5-vote baseline', ok: true },
        ].map(p => (
          <div key={p.label} style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <StatusDot status={p.ok ? 'healthy' : 'degraded'} size={9}/>
            <div>
              <div style={{ fontSize: 20, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: 'var(--pb-fg1)' }}>{p.value}</div>
              <div style={{ fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', marginTop: 2 }}>{p.label}</div>
              <div style={{ fontSize: 11, color: 'var(--pb-fg4)' }}>{p.expected}</div>
            </div>
          </div>
        ))}
      </div>
    </Card>
  </div>
);

/* ═══════════════════════════════════════════════════════════════════
   DETAIL PAGE — full-page layout with sections
   ═══════════════════════════════════════════════════════════════════ */
const MemberDetailPage = ({ member: m }) => {
  const [tab, setTab] = useState('overview');
  const usedPct = m.bytes_used / m.bytes_capacity;
  const cpuPct = m.cpu_usage_millicores / parseInt(m.cpu_limit);
  const memPct = m.mem_usage_bytes / m.mem_limit_bytes;

  return (
    <div>
      {/* Hero strip */}
      <Card style={{ marginBottom: 32, padding: '28px 32px' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24, gap: 16, flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
            <StatusDot status={m.state} size={12}/>
            <span style={{ fontFamily: 'var(--pb-font-heading)', fontSize: 22, fontWeight: 700, color: 'var(--pb-fg1)', whiteSpace: 'nowrap' }}>{m.pod_name}</span>
            <Badge color={statusColors[m.raft_role]}>{m.raft_role}</Badge>
            {m.cordoned && <Badge color="#FBBF24">cordoned</Badge>}
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 16, fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', flexWrap: 'wrap' }}>
            <span style={{ whiteSpace: 'nowrap' }}>{m.node}</span>
            <span style={{ whiteSpace: 'nowrap' }}>{m.zone}</span>
            <span style={{ whiteSpace: 'nowrap' }}>{m.pod_ip}</span>
          </div>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(5, 1fr)', gap: 24 }}>
          {[
            { label: 'Shards', value: m.shard_count },
            { label: 'Runway', value: `${m.disk_runway_days}d` },
            { label: 'Restarts', value: m.restart_count, warn: m.restart_count > 0 },
            { label: 'Age', value: `${Math.floor(m.pod_age_hours / 24)}d` },
            { label: 'gRPC conns', value: m.grpc_conns },
          ].map(s => (
            <div key={s.label} style={{ textAlign: 'center' }}>
              <div style={{ fontSize: 24, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: s.warn ? '#FBBF24' : 'var(--pb-fg1)' }}>{s.value}</div>
              <div style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', letterSpacing: '0.06em', marginTop: 4 }}>{s.label}</div>
            </div>
          ))}
        </div>
      </Card>

      {/* Tab bar */}
      <div style={{ display: 'flex', gap: 4, marginBottom: 32, borderBottom: '1px solid var(--pb-border-subtle)', paddingBottom: 0 }}>
        {['overview', 'kubernetes', 'raft', 'storage', 'shards', 'events'].map(t => (
          <button key={t} onClick={() => setTab(t)} style={{
            padding: '12px 20px', border: 'none', cursor: 'pointer',
            background: 'transparent', color: tab === t ? 'var(--pb-primary)' : 'var(--pb-fg4)',
            fontSize: 13, fontFamily: 'var(--pb-font-mono)', fontWeight: tab === t ? 600 : 400,
            borderBottom: tab === t ? '2px solid var(--pb-primary)' : '2px solid transparent',
            textTransform: 'uppercase', letterSpacing: '0.08em',
            transition: 'color 150ms',
            marginBottom: -1,
          }}
          onMouseEnter={e => { if (tab !== t) e.currentTarget.style.color = 'var(--pb-fg2)'; }}
          onMouseLeave={e => { if (tab !== t) e.currentTarget.style.color = 'var(--pb-fg4)'; }}
          >{t}</button>
        ))}
      </div>

      {tab === 'overview' && <DetailOverview m={m} usedPct={usedPct} cpuPct={cpuPct} memPct={memPct}/>}
      {tab === 'kubernetes' && <DetailKubernetes m={m}/>}
      {tab === 'raft' && <DetailRaft m={m}/>}
      {tab === 'storage' && <DetailStorage m={m}/>}
      {tab === 'shards' && <DetailShards m={m}/>}
      {tab === 'events' && <DetailEvents m={m}/>}
    </div>
  );
};

/* ─── KV row helper ─────────────────────────────────────────────── */
const KV = ({ label, value, color }) => (
  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid var(--pb-border-subtle)', gap: 12 }}>
    <span style={{ fontSize: 13, color: 'var(--pb-fg4)', fontFamily: 'var(--pb-font-mono)', flexShrink: 0 }}>{label}</span>
    <span style={{ fontSize: 13, color: color || 'var(--pb-fg1)', fontFamily: 'var(--pb-font-mono)', fontWeight: 500, textAlign: 'right', wordBreak: 'break-all' }}>{value}</span>
  </div>
);

/* ─── Stacked KV (label above, value below — better for long values) */
const KVStack = ({ label, value, color, mono = true, copyable = false }) => (
  <div style={{ paddingBottom: 14, marginBottom: 14, borderBottom: '1px solid var(--pb-border-subtle)' }}>
    <div style={{
      fontSize: 10, color: 'var(--pb-fg4)', fontFamily: 'var(--pb-font-mono)',
      letterSpacing: '0.12em', textTransform: 'uppercase', marginBottom: 6,
    }}>{label}</div>
    <div style={{
      fontSize: 14, color: color || 'var(--pb-fg1)',
      fontFamily: mono ? 'var(--pb-font-mono)' : 'var(--pb-font-sans)',
      fontWeight: 500, wordBreak: 'break-all', lineHeight: 1.45,
    }}>{value}</div>
  </div>
);

window.KV = KV;
window.KVStack = KVStack;

/* ─── Detail: Overview ──────────────────────────────────────────── */
const DetailOverview = ({ m, usedPct, cpuPct, memPct }) => {
  const myRaft = MOCK.RAFT_HEALTH.members.find(r => r.id === m.id) || {};
  const isLeader = myRaft.role === 'leader';
  return (
  <div>
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 28, marginBottom: 28 }}>
    {/* Resources */}
    <Card>
      <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.15em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 20 }}>RESOURCES</div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
        {[
          { label: 'CPU', val: `${m.cpu_usage_millicores}m / ${m.cpu_limit}`, pct: cpuPct, color: 'var(--pb-primary)' },
          { label: 'Memory', val: `${fmt.bytes(m.mem_usage_bytes)} / ${fmt.bytes(m.mem_limit_bytes)}`, pct: memPct, color: '#6366F1' },
          { label: 'Disk', val: `${fmt.bytes(m.bytes_used)} / ${fmt.bytes(m.bytes_capacity)}`, pct: usedPct, color: '#0EA5E9' },
        ].map(r => (
          <div key={r.label}>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', marginBottom: 6 }}>
              <span>{r.label}</span><span>{r.val}</span>
            </div>
            <ProgressBar value={r.pct * 100} color={r.color} height={8} showLabel thresholds={{ warning: 70, critical: 90 }}/>
          </div>
        ))}
      </div>
    </Card>

    {/* Storage readiness */}
    <Card>
      <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.15em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 20 }}>STORAGE READINESS</div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        {Object.entries(m.readiness).map(([k, v]) => (
          <div key={k} style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <StatusDot status={v === true ? 'healthy' : v === false ? 'degraded' : (v > 0 ? 'degraded' : 'healthy')} size={8}/>
            <span style={{ flex: 1, fontSize: 13, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg2)' }}>{k.replace(/_/g, ' ')}</span>
            <span style={{ fontSize: 13, fontFamily: 'var(--pb-font-mono)', fontWeight: 500, color: v === true ? '#34D399' : v === false ? '#FBBF24' : v > 0 ? '#FBBF24' : '#34D399' }}>
              {typeof v === 'boolean' ? (v ? 'yes' : 'no') : `${v}s`}
            </span>
          </div>
        ))}
      </div>

      {/* Eviction safety */}
      <div style={{ marginTop: 24, padding: '14px 16px', borderRadius: 'var(--pb-radius-md)', background: m.eviction_safe ? 'rgba(52,211,153,0.06)' : 'rgba(251,191,36,0.06)', border: `1px solid ${m.eviction_safe ? 'rgba(52,211,153,0.15)' : 'rgba(251,191,36,0.15)'}` }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
          <StatusDot status={m.eviction_safe ? 'healthy' : 'degraded'} size={8}/>
          <span style={{ fontSize: 13, fontFamily: 'var(--pb-font-mono)', fontWeight: 600, color: m.eviction_safe ? '#34D399' : '#FBBF24' }}>
            {m.eviction_safe ? 'Safe to evict' : 'Not safe to evict'}
          </span>
        </div>
        {m.eviction_warnings.map((w, i) => (
          <div key={i} style={{ fontSize: 12, color: 'var(--pb-fg3)', marginTop: 4 }}>{w.message}</div>
        ))}
      </div>
    </Card>
  </div>

    {/* Raft summary row */}
    <Card>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 18 }}>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.15em', textTransform: 'uppercase', color: 'var(--pb-primary)' }}>RAFT CONSENSUS</div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>
          <Badge color={isLeader ? '#0093D0' : '#94A3B8'}>{myRaft.role || '—'}</Badge>
          <span>term {MOCK.RAFT_HEALTH.current_term}</span>
          {!isLeader && <span>· following <span style={{ color: 'var(--pb-fg2)' }}>{MOCK.RAFT_HEALTH.current_leader}</span></span>}
        </div>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))', gap: 16 }}>
        {[
          { label: 'Commit lag', value: myRaft.commit_lag ?? 0, unit: '', warn: (myRaft.commit_lag ?? 0) > 5 },
          { label: 'Apply lag', value: myRaft.apply_lag ?? 0, unit: '', warn: (myRaft.apply_lag ?? 0) > 5 },
          { label: 'Snapshot age', value: fmt.duration(myRaft.snapshot_age_s || 0), warn: (myRaft.snapshot_age_s || 0) > 600 },
          { label: 'Compaction lag', value: fmt.duration(myRaft.compaction_lag_s || 0), warn: (myRaft.compaction_lag_s || 0) > 120 },
        ].map(s => (
          <div key={s.label}>
            <div style={{ fontSize: 10, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', letterSpacing: '0.08em', marginBottom: 4, textTransform: 'uppercase' }}>{s.label}</div>
            <div style={{ fontSize: 20, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: s.warn ? '#FBBF24' : 'var(--pb-fg1)' }}>{s.value}{s.unit || ''}</div>
          </div>
        ))}
      </div>
    </Card>
  </div>
  );
};

/* ─── Detail: Kubernetes ────────────────────────────────────────── */

// Small section header (used inside cards)
const CardSection = ({ title, children, style = {} }) => (
  <div style={{ marginBottom: 24, ...style }}>
    <div style={{
      fontFamily: 'var(--pb-font-mono)', fontSize: 10.5, fontWeight: 500,
      letterSpacing: '0.18em', textTransform: 'uppercase',
      color: 'var(--pb-primary)', marginBottom: 16,
      paddingBottom: 10, borderBottom: '1px solid var(--pb-border)',
    }}>{title}</div>
    {children}
  </div>
);

const DetailKubernetes = ({ m }) => {
  const ageDays = Math.floor(m.pod_age_hours / 24);
  const ageHours = m.pod_age_hours % 24;
  return (
    <div>
      {/* Pod conditions — horizontal row at the top */}
      <Card style={{ marginBottom: 24 }}>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.18em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16, paddingBottom: 10, borderBottom: '1px solid var(--pb-border)' }}>
          Pod conditions
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 12 }}>
          {m.conditions.map(c => (
            <div key={c.type} style={{
              display: 'flex', alignItems: 'center', gap: 10,
              padding: '12px 14px', background: 'var(--pb-background)',
              borderRadius: 'var(--pb-radius-md)', border: '1px solid var(--pb-border-subtle)',
            }}>
              <StatusDot status={c.status ? 'healthy' : 'unavailable'} size={8}/>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 13, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg1)', fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{c.type}</div>
                <div style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: c.status ? '#34D399' : '#EF4444', marginTop: 2 }}>{c.status ? 'True' : 'False'}</div>
              </div>
            </div>
          ))}
        </div>
      </Card>

      {/* Pod card — full width */}
      <Card style={{ marginBottom: 24 }}>
        <CardSection title="Pod">
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: '0 24px' }}>
            <KVStack label="Pod name" value={m.pod_name}/>
            <KVStack label="Namespace" value={m.namespace}/>
            <KVStack label="StatefulSet" value={m.statefulset}/>
            <KVStack label="Service account" value={m.service_account}/>
            <KVStack label="Pod IP" value={m.pod_ip}/>
            <KVStack label="Age" value={`${ageDays}d ${ageHours}h`}/>
            <KVStack label="Restarts" value={m.restart_count} color={m.restart_count > 0 ? '#FBBF24' : undefined}/>
            <KVStack label="Phase" value="Running" color="#34D399"/>
          </div>
        </CardSection>
        <CardSection title="Container image" style={{ marginBottom: 0 }}>
          <div style={{
            padding: '12px 14px', background: 'var(--pb-background)',
            borderRadius: 'var(--pb-radius-md)', border: '1px solid var(--pb-border-subtle)',
            fontSize: 13, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg2)',
            wordBreak: 'break-all', lineHeight: 1.55,
          }}>{m.image}</div>
          <div style={{ marginTop: 10, fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>
            ghcr.io · IfNotPresent · linux/amd64
          </div>
        </CardSection>
      </Card>

      {/* Node card — full width */}
      <Card style={{ marginBottom: 24 }}>
        <CardSection title="Node">
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: '0 24px' }}>
            <KVStack label="Name" value={m.node}/>
            <KVStack label="Node IP" value={m.node_ip}/>
            <KVStack label="Zone" value={m.zone}/>
            <KVStack label="Instance type" value={m.instance_type}/>
            <KVStack label="OS" value={m.os_image}/>
            <KVStack label="Kernel" value="6.8.12-300.fc40"/>
            <KVStack label="Kubelet" value={m.kubelet_version}/>
            <KVStack label="Runtime" value={m.container_runtime}/>
          </div>
        </CardSection>
        <CardSection title="Node taints" style={{ marginBottom: 0 }}>
          <div style={{ display: 'flex', flexDirection: 'row', flexWrap: 'wrap', gap: 8 }}>
            {m.node_taints.map((t, i) => (
              <div key={i} style={{
                padding: '10px 14px', background: 'var(--pb-background)',
                borderRadius: 'var(--pb-radius-sm)', border: '1px solid var(--pb-border-subtle)',
                fontSize: 12, fontFamily: 'var(--pb-font-mono)',
                display: 'inline-flex', alignItems: 'center', gap: 6,
              }}>
                <span style={{ color: 'var(--pb-fg2)' }}>{t.key}</span>
                <span style={{ color: 'var(--pb-fg4)' }}>=</span>
                <span style={{ color: 'var(--pb-fg2)' }}>{t.value}</span>
                <Badge color="#FBBF24">{t.effect}</Badge>
              </div>
            ))}
          </div>
        </CardSection>
      </Card>

      {/* Persistent volume — full width at bottom */}
      <Card>
        <CardSection title="Persistent volume" style={{ marginBottom: 0 }}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: '0 24px' }}>
            <KVStack label="PVC" value={m.pvc_name}/>
            <KVStack label="Phase" value={m.pvc_phase} color="#34D399"/>
            <KVStack label="Storage class" value={m.storage_class}/>
            <KVStack label="PV name" value={m.volume_name}/>
            <KVStack label="Mount path" value={m.disk_mount}/>
            <KVStack label="Filesystem" value={m.disk_fs}/>
          </div>
        </CardSection>
      </Card>
    </div>
  );
};

/* ─── Detail: Storage I/O ───────────────────────────────────────── */
const DetailStorage = ({ m }) => (
  <div>
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 20, marginBottom: 32 }}>
      {[
        { eyebrow: 'DISK READ', value: fmt.bytesRate(m.disk_read_bps), sub: `${m.disk_iops_read} IOPS` },
        { eyebrow: 'DISK WRITE', value: fmt.bytesRate(m.disk_write_bps), sub: `${m.disk_iops_write} IOPS` },
        { eyebrow: 'FSYNC P50', value: `${m.disk_fsync_us}µs`, sub: m.disk_fs + ' on ' + m.disk_mount },
        { eyebrow: 'RUNWAY', value: `${m.disk_runway_days} days`, sub: fmt.bytes(m.bytes_capacity - m.bytes_used) + ' free' },
      ].map(s => (
        <MetricCard key={s.eyebrow} eyebrow={s.eyebrow} value={s.value} sub={s.sub}/>
      ))}
    </div>

    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 28 }}>
      <Card>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.15em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>DISK</div>
        <KV label="used" value={fmt.bytes(m.bytes_used)}/>
        <KV label="capacity" value={fmt.bytes(m.bytes_capacity)}/>
        <KV label="open blocks" value={fmt.bytes(m.open_block_bytes)}/>
        <KV label="prepare log" value={fmt.bytes(m.prepare_log_bytes)}/>
        <KV label="filesystem" value={m.disk_fs}/>
        <KV label="mount" value={m.disk_mount}/>
      </Card>
      <Card>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.15em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>NETWORK</div>
        <KV label="rx throughput" value={fmt.bytesRate(m.net_rx_bps)}/>
        <KV label="tx throughput" value={fmt.bytesRate(m.net_tx_bps)}/>
        <KV label="gRPC connections" value={m.grpc_conns}/>
        <KV label="peer connections" value={m.peer_conns}/>
        <KV label="pod IP" value={m.pod_ip}/>
        <KV label="node IP" value={m.node_ip}/>
      </Card>
    </div>
  </div>
);

/* ─── Detail: Shards ────────────────────────────────────────────── */
const DetailShards = ({ m }) => (
  <div>
    <div style={{ fontSize: 13, color: 'var(--pb-fg3)', marginBottom: 20 }}>{m.shards.length} shard replicas assigned to this member</div>
    <Table
      columns={[
        { key: 'id', label: 'Shard', render: r => <span style={{ fontFamily: 'var(--pb-font-mono)', fontWeight: 500 }}>{r.id}</span> },
        { key: 'role', label: 'Role', render: r => <Badge color={r.role === 'leader' ? '#0093D0' : '#94A3B8'}>{r.role}</Badge> },
        { key: 'commit', label: 'Commit index', align: 'right', render: r => <span style={{ fontFamily: 'var(--pb-font-mono)' }}>{r.commit_index}</span> },
        { key: 'bytes', label: 'Local bytes', align: 'right', render: r => fmt.bytes(r.local_bytes) },
        { key: 'verified', label: 'Byte verified', align: 'center', render: r => <StatusDot status={r.byte_verified ? 'healthy' : 'unavailable'} size={8}/> },
      ]}
      data={m.shards}
    />
  </div>
);

/* ─── Detail: Events ────────────────────────────────────────────── */
const DetailEvents = ({ m }) => (
  <div>
    {m.events.length === 0 && (
      <div style={{ color: 'var(--pb-fg4)', fontFamily: 'var(--pb-font-mono)', fontSize: 13, padding: 40, textAlign: 'center' }}>No recent events</div>
    )}
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      {m.events.map((e, i) => (
        <Card key={i} style={{ padding: '16px 20px' }}>
          <div style={{ display: 'flex', gap: 14, alignItems: 'flex-start' }}>
            <div style={{ width: 8, height: 8, borderRadius: '50%', marginTop: 6, flexShrink: 0, background: e.type === 'Warning' ? '#FBBF24' : '#34D399' }}></div>
            <div style={{ flex: 1 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 4 }}>
                <span style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 14, fontWeight: 600, color: 'var(--pb-fg1)' }}>{e.reason}</span>
                <Badge color={e.type === 'Warning' ? '#FBBF24' : '#94A3B8'}>{e.type}</Badge>
                <span style={{ fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', marginLeft: 'auto' }}>{fmt.duration(e.age_s)} ago</span>
              </div>
              <div style={{ fontSize: 13, color: 'var(--pb-fg3)', lineHeight: 1.5 }}>{e.message}</div>
            </div>
          </div>
        </Card>
      ))}
    </div>
  </div>
);

/* ─── Detail: Raft ──────────────────────────────────────────────── */
const DetailRaft = ({ m }) => {
  const raft = MOCK.RAFT_HEALTH;
  const myRaft = raft.members.find(r => r.id === m.id) || {};
  const isLeader = myRaft.role === 'leader';
  const peers = raft.members.filter(r => r.id !== m.id);

  return (
    <div>
      {/* Member raft headline */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 20, marginBottom: 32 }}>
        <MetricCard
          eyebrow="Role"
          value={isLeader ? 'Leader' : 'Follower'}
          sub={isLeader ? 'Coordinates writes for this term' : `Following ${raft.current_leader}`}
          color={isLeader ? '#0093D0' : '#94A3B8'}
        />
        <MetricCard
          eyebrow="Term"
          value={raft.current_term}
          sub={`${raft.term_changes_24h} term changes (24h)`}
        />
        <MetricCard
          eyebrow="Commit lag"
          value={`${myRaft.commit_lag ?? 0}`}
          sub="entries behind committed index"
          color={(myRaft.commit_lag ?? 0) > 5 ? '#FBBF24' : '#34D399'}
        />
        <MetricCard
          eyebrow="Apply lag"
          value={`${myRaft.apply_lag ?? 0}`}
          sub="entries behind applied index"
          color={(myRaft.apply_lag ?? 0) > 5 ? '#FBBF24' : '#34D399'}
        />
      </div>

      {/* Lower split: this member's state + cluster-wide context */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 28, marginBottom: 32 }}>
        <Card>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.15em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>THIS MEMBER</div>
          <KV label="role" value={myRaft.role || '—'} color={isLeader ? '#0093D0' : 'var(--pb-fg1)'}/>
          <KV label="commit lag" value={`${myRaft.commit_lag ?? 0} entries`} color={(myRaft.commit_lag ?? 0) > 5 ? '#FBBF24' : undefined}/>
          <KV label="apply lag" value={`${myRaft.apply_lag ?? 0} entries`} color={(myRaft.apply_lag ?? 0) > 5 ? '#FBBF24' : undefined}/>
          <KV label="snapshot age" value={fmt.duration(myRaft.snapshot_age_s || 0)} color={(myRaft.snapshot_age_s || 0) > 600 ? '#FBBF24' : undefined}/>
          <KV label="compaction lag" value={fmt.duration(myRaft.compaction_lag_s || 0)} color={(myRaft.compaction_lag_s || 0) > 120 ? '#FBBF24' : undefined}/>
          <KV label="byte-serving" value={m.readiness.byte_serving ? 'eligible' : 'not eligible'} color={m.readiness.byte_serving ? '#34D399' : '#FBBF24'}/>
        </Card>

        <Card>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.15em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>CLUSTER CONSENSUS</div>
          <KV label="leader" value={raft.current_leader}/>
          <KV label="quorum" value={raft.quorum_available ? 'healthy' : 'lost'} color={raft.quorum_available ? '#34D399' : '#EF4444'}/>
          <KV label="voters" value={`${raft.members.length} of 5 baseline`}/>
          <KV label="proposal failures (1h)" value={raft.proposal_failures_1h} color={raft.proposal_failures_1h > 0 ? '#EF4444' : undefined}/>
          <KV label="ReadIndex failures (1h)" value={raft.read_index_failures_1h} color={raft.read_index_failures_1h > 0 ? '#EF4444' : undefined}/>
          <KV label="term changes (24h)" value={raft.term_changes_24h}/>
        </Card>
      </div>

      {/* Peer state */}
      <Card>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.15em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>PEER MEMBERS</div>
        <Table
          columns={[
            { key: 'id', label: 'Member', render: r => {
              const k = MOCK.K8S_MEMBERS.find(x => x.id === r.id);
              return <span style={{ fontFamily: 'var(--pb-font-mono)', fontWeight: 500, color: 'var(--pb-fg1)' }}>{k?.pod_name || r.id}</span>;
            }},
            { key: 'role', label: 'Role', render: r => <Badge color={r.role === 'leader' ? '#0093D0' : '#94A3B8'}>{r.role}</Badge> },
            { key: 'commit', label: 'Commit lag', align: 'right', render: r => <span style={{ color: r.commit_lag > 5 ? '#FBBF24' : 'var(--pb-fg2)' }}>{r.commit_lag}</span> },
            { key: 'apply', label: 'Apply lag', align: 'right', render: r => <span style={{ color: r.apply_lag > 5 ? '#FBBF24' : 'var(--pb-fg2)' }}>{r.apply_lag}</span> },
            { key: 'snap', label: 'Snapshot age', align: 'right', render: r => fmt.duration(r.snapshot_age_s) },
          ]}
          data={peers}
        />
      </Card>
    </div>
  );
};

window.MembersView = MembersView;
