// Service mesh view — clients connected to the gateway via mTLS, with a topology map

const ServiceMeshView = () => {
  const { navigate, route } = window.useNav();
  const mesh = MOCK.SERVICE_MESH;
  const clients = MOCK.CLIENT_SERVICES;

  // If a specific client is selected
  if (route.params.clientId) {
    const client = clients.find(c => c.id === route.params.clientId);
    if (client) return <ClientDetailPage client={client}/>;
  }

  return (
    <div>
      {/* Top metrics */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 20, marginBottom: 36 }}>
        <MetricCard eyebrow="Connected clients" value={mesh.total_clients} sub={`${mesh.total_replicas} replicas total`} color="#0093D0"/>
        <MetricCard eyebrow="Active gRPC streams" value={fmt.number(mesh.active_connections)} sub={`${Math.round(mesh.total_rps)} req/s aggregate`}/>
        <MetricCard eyebrow="mTLS verified" value={fmt.pct(mesh.mtls_verified_pct)} sub={mesh.cert_expiring_soon > 0 ? `${mesh.cert_expiring_soon} cert expiring soon` : 'All certs valid'} color={mesh.cert_expiring_soon > 0 ? '#FBBF24' : '#34D399'}/>
        <MetricCard eyebrow="Aggregate error rate" value={`${(mesh.weighted_error_rate * 1000).toFixed(2)}‰`} sub="weighted by request volume" color={mesh.weighted_error_rate > 0.005 ? '#FBBF24' : '#34D399'}/>
      </div>

      {/* Topology */}
      <Card style={{ marginBottom: 36, padding: '28px 24px' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)' }}>TOPOLOGY</div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12, fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>
            <span style={{ display: 'flex', alignItems: 'center', gap: 5 }}><span style={{ width: 18, height: 2, background: '#34D399' }}></span>healthy</span>
            <span style={{ display: 'flex', alignItems: 'center', gap: 5 }}><span style={{ width: 18, height: 2, background: '#FBBF24' }}></span>warning</span>
            <span style={{ display: 'flex', alignItems: 'center', gap: 5 }}><span style={{ width: 18, height: 2, background: '#EF4444' }}></span>degraded</span>
          </div>
        </div>
        <TopologyMap clients={clients} backends={MOCK.BACKEND_DEPS} onClickClient={(c) => navigate('clients', c.name, { clientId: c.id })}/>
      </Card>

      {/* Connected clients list */}
      <SectionHeader eyebrow="Workload identities" title="Connected clients"/>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(360px, 1fr))', gap: 16 }}>
        {clients.map(c => (
          <ClientCard key={c.id} client={c} onClick={() => navigate('clients', c.name, { clientId: c.id })}/>
        ))}
      </div>
    </div>
  );
};

/* ─── Topology map (SVG) ────────────────────────────────────────── */
const TopologyMap = ({ clients, backends, onClickClient }) => {
  const W = 960, H = 520;
  const cx = W / 2, cy = H / 2;
  const clientRadius = 230;
  const backendRadius = 200;

  // Clients arranged on the LEFT half (angles 110°–250°)
  const cAngleStart = 110;
  const cAngleEnd = 250;
  const cAngleStep = (cAngleEnd - cAngleStart) / Math.max(clients.length - 1, 1);

  const clientPositions = clients.map((c, i) => {
    const angle = (cAngleStart + i * cAngleStep) * Math.PI / 180;
    return { client: c, x: cx + Math.cos(angle) * clientRadius, y: cy + Math.sin(angle) * clientRadius };
  });

  // Backends on the RIGHT
  const backendPositions = backends.map((b, i) => ({
    backend: b, x: cx + 220, y: cy - 60 + i * 110,
  }));

  return (
    <div style={{ position: 'relative', width: '100%', maxWidth: W, margin: '0 auto' }}>
      <svg viewBox={`0 0 ${W} ${H}`} style={{ width: '100%', height: 'auto', display: 'block' }}>
        <defs>
          <marker id="arrow-gw" markerWidth="6" markerHeight="6" refX="5" refY="3" orient="auto">
            <path d="M0,0 L6,3 L0,6 z" fill="var(--pb-fg4)" opacity="0.5"/>
          </marker>
        </defs>

        {/* Client → Gateway connections */}
        {clientPositions.map(({ client, x, y }) => {
          const color = client.status === 'healthy' ? '#34D399' : client.status === 'warning' ? '#FBBF24' : '#EF4444';
          const strokeWidth = 1 + Math.min(client.rps / 200, 3);
          // Bezier curve from client to gateway center
          const midX = (x + cx) / 2;
          return (
            <g key={client.id}>
              <path
                d={`M ${x},${y} Q ${midX},${y} ${cx - 70},${cy}`}
                stroke={color} strokeWidth={strokeWidth} fill="none" opacity={0.5}
              />
            </g>
          );
        })}

        {/* Gateway → Backend connections */}
        {backendPositions.map(({ backend, x, y }) => (
          <line
            key={backend.id}
            x1={cx + 70} y1={cy} x2={x - 50} y2={y}
            stroke="var(--pb-fg4)" strokeWidth="1.5" opacity={0.4}
            markerEnd="url(#arrow-gw)"
          />
        ))}

        {/* Gateway box (center) */}
        <g>
          <rect x={cx - 70} y={cy - 36} width="140" height="72" rx="10"
            fill="rgba(0,147,208,0.10)" stroke="var(--pb-primary)" strokeWidth="1.5"/>
          <text x={cx} y={cy - 14} textAnchor="middle" fill="var(--pb-primary)"
            fontFamily="var(--pb-font-mono)" fontSize="10" letterSpacing="0.18em" fontWeight="500">
            S.C.R.A.P.
          </text>
          <text x={cx} y={cy + 4} textAnchor="middle" fill="var(--pb-fg1)"
            fontFamily="var(--pb-font-heading)" fontSize="16" fontWeight="700">
            Gateway
          </text>
          <text x={cx} y={cy + 24} textAnchor="middle" fill="var(--pb-fg4)"
            fontFamily="var(--pb-font-mono)" fontSize="10">
            {MOCK.STORAGE_MEMBERS.length} members · {MOCK.CLUSTER_SUMMARY.shard_count} shards
          </text>
        </g>

        {/* Client nodes */}
        {clientPositions.map(({ client, x, y }) => {
          const color = client.status === 'healthy' ? '#34D399' : client.status === 'warning' ? '#FBBF24' : '#EF4444';
          const labelAnchor = x < cx ? 'end' : 'start';
          const labelOffset = x < cx ? -14 : 14;
          return (
            <g key={client.id} style={{ cursor: 'pointer' }} onClick={() => onClickClient(client)}>
              {/* Glow */}
              <circle cx={x} cy={y} r="14" fill={color} opacity="0.12"/>
              {/* Node dot */}
              <circle cx={x} cy={y} r="7" fill={color} stroke="var(--pb-background)" strokeWidth="2"/>
              {/* Service name */}
              <text x={x + labelOffset} y={y - 2} textAnchor={labelAnchor}
                fill="var(--pb-fg1)" fontFamily="var(--pb-font-mono)" fontSize="12" fontWeight="600">
                {client.name}
              </text>
              {/* RPS / namespace */}
              <text x={x + labelOffset} y={y + 14} textAnchor={labelAnchor}
                fill="var(--pb-fg4)" fontFamily="var(--pb-font-mono)" fontSize="10">
                {client.rps} req/s · {client.kind}
              </text>
            </g>
          );
        })}

        {/* Backend nodes */}
        {backendPositions.map(({ backend, x, y }) => (
          <g key={backend.id}>
            <rect x={x - 8} y={y - 8} width="16" height="16" rx="3"
              fill="var(--pb-surface)" stroke="var(--pb-fg4)" strokeWidth="1.5"/>
            <text x={x + 14} y={y - 2} fill="var(--pb-fg1)"
              fontFamily="var(--pb-font-mono)" fontSize="12" fontWeight="600">
              {backend.name}
            </text>
            <text x={x + 14} y={y + 14} fill="var(--pb-fg4)"
              fontFamily="var(--pb-font-mono)" fontSize="10">
              {backend.endpoint}
            </text>
          </g>
        ))}

        {/* mTLS badge near gateway */}
        <g>
          <rect x={cx - 38} y={cy + 38} width="76" height="20" rx="10"
            fill="var(--pb-surface)" stroke="var(--pb-border)" strokeWidth="1"/>
          <text x={cx} y={cy + 52} textAnchor="middle" fill="var(--pb-fg3)"
            fontFamily="var(--pb-font-mono)" fontSize="9" letterSpacing="0.12em">
            ▲ mTLS · SPIFFE
          </text>
        </g>
      </svg>
    </div>
  );
};

/* ─── Client card ───────────────────────────────────────────────── */
const ClientCard = ({ client, onClick }) => {
  const c = client;
  const statusColor = c.status === 'healthy' ? '#34D399' : c.status === 'warning' ? '#FBBF24' : '#EF4444';
  return (
    <Card onClick={onClick} hoverable style={{ padding: 22, cursor: 'pointer' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 14 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <StatusDot status={c.status === 'healthy' ? 'healthy' : c.status === 'warning' ? 'degraded' : 'unavailable'} size={9}/>
          <span style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 14, fontWeight: 600, color: 'var(--pb-fg1)' }}>{c.name}</span>
        </div>
        <Badge color={c.kind === 'writer' ? '#0093D0' : c.kind === 'reader' ? '#7DD3FC' : '#6366F1'}>{c.kind}</Badge>
      </div>

      <div style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', marginBottom: 14, wordBreak: 'break-all', lineHeight: 1.5 }}>{c.workload_identity}</div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 8, marginBottom: 14 }}>
        <Stat label="RPS" value={fmt.number(c.rps)}/>
        <Stat label="P95" value={`${c.p95_ms}ms`}/>
        <Stat label="Errors" value={`${(c.error_rate * 1000).toFixed(1)}‰`} warn={c.error_rate > 0.005}/>
      </div>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', paddingTop: 12, borderTop: '1px solid var(--pb-border)' }}>
        <span>{c.namespace} · {c.replicas} replicas</span>
        <span>{c.connections} streams</span>
      </div>

      {c.degraded_reason && (
        <div style={{ marginTop: 12, padding: '8px 10px', borderRadius: 'var(--pb-radius-sm)', background: c.status === 'warning' ? 'rgba(251,191,36,0.08)' : 'rgba(239,68,68,0.08)', border: `1px solid ${statusColor}30`, fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: statusColor, lineHeight: 1.5 }}>
          {c.degraded_reason}
        </div>
      )}
    </Card>
  );
};

const Stat = ({ label, value, warn }) => (
  <div>
    <div style={{ fontSize: 9, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', letterSpacing: '0.12em', textTransform: 'uppercase', marginBottom: 3 }}>{label}</div>
    <div style={{ fontSize: 16, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: warn ? '#FBBF24' : 'var(--pb-fg1)' }}>{value}</div>
  </div>
);

/* ─── Client detail page ────────────────────────────────────────── */
const ClientDetailPage = ({ client: c }) => {
  return (
    <div>
      {/* Hero */}
      <Card style={{ marginBottom: 24, padding: '28px 32px' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24, gap: 16, flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
            <StatusDot status={c.status === 'healthy' ? 'healthy' : c.status === 'warning' ? 'degraded' : 'unavailable'} size={12}/>
            <span style={{ fontFamily: 'var(--pb-font-heading)', fontSize: 22, fontWeight: 700, color: 'var(--pb-fg1)' }}>{c.name}</span>
            <Badge color={c.kind === 'writer' ? '#0093D0' : c.kind === 'reader' ? '#7DD3FC' : '#6366F1'}>{c.kind}</Badge>
            {c.mtls_verified && <Badge color="#34D399">mTLS verified</Badge>}
          </div>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 24 }}>
          {[
            { label: 'Request rate', value: `${fmt.number(c.rps)}/s` },
            { label: 'P95 latency', value: `${c.p95_ms}ms` },
            { label: 'Error rate', value: `${(c.error_rate * 1000).toFixed(2)}‰`, warn: c.error_rate > 0.005 },
            { label: 'Active streams', value: c.connections },
            { label: 'Replicas', value: c.replicas },
          ].map(s => (
            <div key={s.label}>
              <div style={{ fontSize: 24, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: s.warn ? '#FBBF24' : 'var(--pb-fg1)' }}>{s.value}</div>
              <div style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', letterSpacing: '0.06em', marginTop: 4 }}>{s.label}</div>
            </div>
          ))}
        </div>
      </Card>

      {/* Degraded notification */}
      {c.degraded_reason && (
        <div style={{ marginBottom: 24, padding: '14px 18px', borderRadius: 'var(--pb-radius-md)', background: c.status === 'warning' ? 'rgba(251,191,36,0.08)' : 'rgba(239,68,68,0.08)', border: `1px solid ${c.status === 'warning' ? 'rgba(251,191,36,0.25)' : 'rgba(239,68,68,0.25)'}` }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <StatusDot status={c.status === 'warning' ? 'degraded' : 'unavailable'} size={9}/>
            <span style={{ fontSize: 13, fontFamily: 'var(--pb-font-mono)', fontWeight: 600, color: c.status === 'warning' ? '#FBBF24' : '#EF4444' }}>
              {c.degraded_reason}
            </span>
          </div>
        </div>
      )}

      {/* Traffic card — full width */}
      <Card style={{ marginBottom: 24 }}>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.18em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16, paddingBottom: 10, borderBottom: '1px solid var(--pb-border)' }}>
          Traffic
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 24 }}>
          {[
            { label: 'Bytes in', value: fmt.bytesRate(c.bytes_in_per_sec) },
            { label: 'Bytes out', value: fmt.bytesRate(c.bytes_out_per_sec) },
            { label: 'Streams / replica', value: (c.connections / c.replicas).toFixed(1) },
            { label: 'Last seen', value: fmt.ago(c.last_seen_at) },
          ].map(t => (
            <div key={t.label}>
              <div style={{ fontSize: 10, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', letterSpacing: '0.12em', textTransform: 'uppercase', marginBottom: 6 }}>{t.label}</div>
              <div style={{ fontSize: 22, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: t.color || 'var(--pb-fg1)', fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }}>{t.value}</div>
            </div>
          ))}
        </div>
      </Card>

      {/* Identity (70%) + Capabilities (30%) */}
      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 7fr) minmax(0, 3fr)', gap: 24 }}>
        <Card>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.18em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16, paddingBottom: 10, borderBottom: '1px solid var(--pb-border)' }}>
            Workload identity
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: '0 24px' }}>
            <KVStack label="Service name" value={c.name}/>
            <KVStack label="Kubernetes namespace" value={c.namespace}/>
            <KVStack label="Replicas" value={c.replicas}/>
            <KVStack label="mTLS status" value={c.mtls_verified ? 'Verified' : 'Not verified'} color={c.mtls_verified ? '#34D399' : '#EF4444'}/>
            <KVStack label="Certificate expiry" value={`${c.cert_expires_in_days} days`} color={c.cert_expires_in_days < 30 ? '#FBBF24' : undefined}/>
          </div>
          <KVStack label="SPIFFE ID" value={c.workload_identity}/>
        </Card>

        <Card>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.18em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16, paddingBottom: 10, borderBottom: '1px solid var(--pb-border)' }}>
            Granted RPC capabilities
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {c.capabilities.map(cap => (
              <div key={cap} style={{
                display: 'flex', alignItems: 'center', gap: 10,
                padding: '10px 14px', borderRadius: 'var(--pb-radius-sm)',
                background: 'var(--pb-background)', border: '1px solid var(--pb-border-subtle)',
              }}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#34D399" strokeWidth="2"><path strokeLinecap="round" strokeLinejoin="round" d="m4.5 12.75 6 6 9-13.5"/></svg>
                <span style={{ fontSize: 13, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg1)', fontWeight: 500 }}>{cap}</span>
              </div>
            ))}
          </div>
        </Card>
      </div>
    </div>
  );
};

window.ServiceMeshView = ServiceMeshView;
