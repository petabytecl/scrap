// OpenBao health view — Transit latency, DEK cache, key rotation, audit device

const OpenBaoView = () => {
  const ob = MOCK.OPENBAO_HEALTH;
  const cb = MOCK.CIRCUIT_BREAKERS.openbao;

  return (
    <div>
      <SectionHeader eyebrow="Encryption" title="OpenBao Transit" right={
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <StatusDot status={ob.state} size={9}/>
          <span style={{ fontSize: 13, fontFamily: 'var(--pb-font-mono)', color: statusColors[ob.state], fontWeight: 500 }}>{ob.state}</span>
        </div>
      }/>

      {/* Top metrics */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 16, marginBottom: 28 }}>
        <MetricCard eyebrow="P50 latency" value={`${ob.transit_latency_p50_ms}ms`} color="#34D399"/>
        <MetricCard eyebrow="P95 latency" value={`${ob.transit_latency_p95_ms}ms`} color="#34D399"/>
        <MetricCard eyebrow="P99 latency" value={`${ob.transit_latency_p99_ms}ms`} color={ob.transit_latency_p99_ms > 10 ? '#FBBF24' : '#34D399'}/>
        <MetricCard eyebrow="Errors (1h)" value={ob.errors_1h} color={ob.errors_1h > 0 ? '#EF4444' : '#34D399'}/>
      </div>

      {/* DEK cache + key info */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 28 }}>
        <Card>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>DEK CACHE</div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 20 }}>
            {/* Donut-style visualization */}
            <svg width="80" height="80" viewBox="0 0 80 80">
              <circle cx="40" cy="40" r="32" fill="none" stroke="var(--pb-muted)" strokeWidth="8"/>
              <circle cx="40" cy="40" r="32" fill="none" stroke="#34D399" strokeWidth="8"
                strokeDasharray={`${ob.dek_cache_hit_rate * 201} 201`}
                strokeDashoffset="0" strokeLinecap="round"
                transform="rotate(-90 40 40)"/>
              <text x="40" y="40" textAnchor="middle" dominantBaseline="central"
                fill="var(--pb-fg1)" fontFamily="var(--pb-font-heading)" fontSize="16" fontWeight="700">
                {fmt.pct(ob.dek_cache_hit_rate)}
              </text>
            </svg>
            <div>
              <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--pb-fg1)', marginBottom: 4 }}>Cache hit rate</div>
              <div style={{ fontSize: 12, color: 'var(--pb-fg3)' }}>
                DEK cache avoids redundant Transit calls for repeated block access.
                Target: &gt;95%
              </div>
            </div>
          </div>
        </Card>

        <Card>
          <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>KEY STATUS</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {[
              { label: 'Key ID', value: ob.key_id },
              { label: 'Key version', value: `v${ob.key_version}` },
              { label: 'Rewrap lag', value: `${ob.rewrap_lag_s}s` },
              { label: 'Audit device', value: ob.audit_device_healthy ? 'healthy' : 'unhealthy', ok: ob.audit_device_healthy },
            ].map(row => (
              <div key={row.label} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <span style={{ fontSize: 12, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>{row.label}</span>
                <span style={{ fontSize: 13, fontFamily: 'var(--pb-font-mono)', fontWeight: 500, color: row.ok === false ? '#EF4444' : row.ok === true ? '#34D399' : 'var(--pb-fg1)' }}>{row.value}</span>
              </div>
            ))}
          </div>
        </Card>
      </div>

      {/* Circuit breaker detail */}
      <Card style={{ marginBottom: 28 }}>
        <div style={{ fontFamily: 'var(--pb-font-mono)', fontSize: 11, fontWeight: 500, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-primary)', marginBottom: 16 }}>CIRCUIT BREAKER</div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 20 }}>
          {[
            { label: 'State', value: cb.state, ok: cb.state === 'closed' },
            { label: 'Consecutive failures', value: cb.consecutive_failures, ok: cb.consecutive_failures < 3 },
            { label: 'Error rate', value: `${cb.error_rate_permille / 10}%`, ok: cb.error_rate_permille < 100 },
            { label: 'Samples', value: cb.samples },
          ].map(item => (
            <div key={item.label}>
              <div style={{ fontSize: 22, fontWeight: 700, fontFamily: 'var(--pb-font-heading)', color: item.ok === false ? '#EF4444' : item.ok === true ? '#34D399' : 'var(--pb-fg1)' }}>{item.value}</div>
              <div style={{ fontSize: 11, fontFamily: 'var(--pb-font-mono)', color: 'var(--pb-fg4)' }}>{item.label}</div>
            </div>
          ))}
        </div>
      </Card>

      {/* Operation latency samples */}
      <SectionHeader eyebrow="Samples" title="Transit operation latency"/>
      <Table
        columns={[
          { key: 'op', label: 'Operation', render: r => <span style={{ fontFamily: 'var(--pb-font-mono)', fontWeight: 500 }}>{r.op}</span> },
          { key: 'samples', label: 'Samples', align: 'center' },
          { key: 'success', label: 'Success', align: 'center', render: r => <span style={{ color: '#34D399' }}>{r.success}</span> },
          { key: 'errors', label: 'Errors', align: 'center', render: r => <span style={{ color: r.errors > 0 ? '#EF4444' : 'var(--pb-fg3)' }}>{r.errors}</span> },
          { key: 'p50', label: 'P50', align: 'right', render: r => <span style={{ fontFamily: 'var(--pb-font-mono)' }}>{r.p50}ms</span> },
          { key: 'p95', label: 'P95', align: 'right', render: r => <span style={{ fontFamily: 'var(--pb-font-mono)' }}>{r.p95}ms</span> },
          { key: 'max', label: 'Max', align: 'right', render: r => <span style={{ fontFamily: 'var(--pb-font-mono)' }}>{r.max}ms</span> },
        ]}
        data={[
          { op: 'wrap', samples: 3, success: 3, errors: 0, p50: 1, p95: 2, max: 2 },
          { op: 'unwrap', samples: 3, success: 3, errors: 0, p50: 1, p95: 1, max: 1 },
          { op: 'rewrap', samples: 3, success: 3, errors: 0, p50: 1, p95: 1, max: 1 },
        ]}
      />
    </div>
  );
};

window.OpenBaoView = OpenBaoView;
