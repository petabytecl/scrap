// Backend view — upload lag, lane queues, provider health

const BackendView = () => {
  const lanes = MOCK.BACKEND_LANES;
  const totalBacklog = lanes.reduce((s, l) => s + l.bytes_backlog, 0);

  return (
    <div>
      <SectionHeader eyebrow="Backend" title="Upload lag and queues"/>

      {/* Top metrics */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 20, marginBottom: 36 }}>
        <MetricCard eyebrow="Total backlog" value={fmt.bytes(totalBacklog)} sparkData={MOCK.BACKEND_LAG_24H} color="#0093D0"/>
        <MetricCard eyebrow="Oldest upload" value={`${Math.max(...lanes.map(l => l.oldest_age_s))}s`} sub="ingest_upload lane" color={Math.max(...lanes.map(l => l.oldest_age_s)) > 60 ? '#FBBF24' : '#34D399'}/>
        <MetricCard eyebrow="Provider" value="S3" sub="LocalStack · local-kind"/>
        <MetricCard eyebrow="Circuit breaker" value={MOCK.CIRCUIT_BREAKERS.backend.state} color={statusColors[MOCK.CIRCUIT_BREAKERS.backend.state]}/>
      </div>

      {/* Backend lag chart */}
      <Card style={{ marginBottom: 36 }}>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>UPLOAD LAG (24H)</div>
        <AreaChart data={MOCK.BACKEND_LAG_24H} color="#FBBF24" height={100} yFormat={v => v.toFixed(0) + 's'}/>
        <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 6, fontSize: 10, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>
          <span>24h ago</span><span>12h ago</span><span>now</span>
        </div>
      </Card>

      {/* Lane detail cards */}
      <SectionHeader eyebrow="Operation lanes" title="Queue depth by lane"/>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))', gap: 20, marginBottom: 36 }}>
        {lanes.map(lane => {
          const saturation = lane.workers_active / lane.workers_max;
          return (
            <Card key={lane.lane}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
                <span style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 14, fontWeight: 600, color: 'var(--pb-fg1)' }}>{lane.lane}</span>
                <Badge color={lane.queue_depth > 50 ? '#FBBF24' : '#34D399'}>
                  {lane.queue_depth} queued
                </Badge>
              </div>

              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 14 }}>
                <div>
                  <div style={{ fontSize: 10, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', marginBottom: 2, letterSpacing: '0.06em' }}>BACKLOG</div>
                  <div style={{ fontSize: 16, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: 'var(--pb-fg1)' }}>{fmt.bytes(lane.bytes_backlog)}</div>
                </div>
                <div>
                  <div style={{ fontSize: 10, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', marginBottom: 2, letterSpacing: '0.06em' }}>OLDEST</div>
                  <div style={{ fontSize: 16, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: lane.oldest_age_s > 30 ? '#FBBF24' : 'var(--pb-fg1)' }}>{lane.oldest_age_s > 0 ? fmt.duration(lane.oldest_age_s) : '—'}</div>
                </div>
              </div>

              {/* Worker saturation */}
              <div style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)', marginBottom: 4 }}>
                Workers: {lane.workers_active}/{lane.workers_max}
              </div>
              <ProgressBar value={saturation * 100} color={saturation >= 1 ? '#FBBF24' : 'var(--pb-primary)'} height={4}/>

              <div style={{ marginTop: 10, fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>
                Budget: {fmt.bytesRate(lane.bytes_per_second)} · {lane.requests_per_second} req/s
              </div>
            </Card>
          );
        })}
      </div>

      {/* Backend request samples */}
      <SectionHeader eyebrow="Latency" title="Recent backend samples"/>
      <Table
        columns={[
          { key: 'operation', label: 'Operation', render: r => <span style={{ fontFamily: 'var(--pb-font-mono)', fontWeight: 500 }}>{r.operation}</span> },
          { key: 'method', label: 'Method', render: r => <Badge color={r.method === 'PUT' ? '#0093D0' : r.method === 'GET' ? '#34D399' : '#94A3B8'}>{r.method}</Badge> },
          { key: 'status', label: 'Status', align: 'center', render: r => <span style={{ fontFamily: 'var(--pb-font-mono)', color: r.status_code === 200 ? '#34D399' : '#EF4444' }}>{r.status_code}</span> },
          { key: 'bytes', label: 'Bytes', align: 'right', render: r => r.bytes ? fmt.bytes(r.bytes) : '—' },
          { key: 'latency', label: 'Latency', align: 'right', render: r => <span style={{ fontFamily: 'var(--pb-font-mono)' }}>{r.latency_millis}ms</span> },
        ]}
        data={[
          { operation: 'put', method: 'PUT', status_code: 200, bytes: 4096, latency_millis: 3 },
          { operation: 'head', method: 'HEAD', status_code: 200, bytes: null, latency_millis: 1 },
          { operation: 'get', method: 'GET', status_code: 200, bytes: 4096, latency_millis: 1 },
          { operation: 'put', method: 'PUT', status_code: 200, bytes: 4096, latency_millis: 1 },
          { operation: 'head', method: 'HEAD', status_code: 200, bytes: null, latency_millis: 1 },
          { operation: 'get', method: 'GET', status_code: 200, bytes: 4096, latency_millis: 1 },
        ]}
      />
    </div>
  );
};

window.BackendView = BackendView;
