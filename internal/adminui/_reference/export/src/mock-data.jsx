// Mock data derived from actual S.C.R.A.P. proto definitions and evidence files.
// All values are realistic for a production billing ETL storage gateway.

const CELL_ID = 'scrap-prod-cl-1';
const PROFILE_ID = 'scrap-prod-v1';
const RELEASE_SHA = '32dc18d3bd44469d2195987e30936a1885822e84';

const CLUSTER_SUMMARY = {
  cell_id: CELL_ID,
  profile_id: PROFILE_ID,
  release_sha: RELEASE_SHA,
  shard_count: 64,
  storage_member_count: 7,
  local_bytes_used: 348_917_760_000,
  local_bytes_capacity: 1_022_042_832_896,
  documents_total: 12_847_293,
  transactions_total: 3_214_823,
  degraded_document_count: 3,
  pending_operation_count: 2,
  write_ack_gate: 'enabled',
  uptime_seconds: 1_296_000, // 15 days
};

const STORAGE_MEMBERS = [
  { id: 'member-0', cell_id: CELL_ID, state: 'ONLINE', cordoned: false, bytes_used: 52_428_800_000, bytes_capacity: 146_006_118_912, shard_count: 10, raft_role: 'follower', last_seen_at: Date.now() - 2000, disk_runway_days: 18, node: 'k8s-storage-01' },
  { id: 'member-1', cell_id: CELL_ID, state: 'ONLINE', cordoned: false, bytes_used: 48_318_382_080, bytes_capacity: 146_006_118_912, shard_count: 9, raft_role: 'leader', last_seen_at: Date.now() - 1500, disk_runway_days: 19, node: 'k8s-storage-02' },
  { id: 'member-2', cell_id: CELL_ID, state: 'ONLINE', cordoned: false, bytes_used: 55_834_574_848, bytes_capacity: 146_006_118_912, shard_count: 10, raft_role: 'follower', last_seen_at: Date.now() - 3200, disk_runway_days: 17, node: 'k8s-storage-03' },
  { id: 'member-3', cell_id: CELL_ID, state: 'ONLINE', cordoned: false, bytes_used: 50_465_865_728, bytes_capacity: 146_006_118_912, shard_count: 9, raft_role: 'follower', last_seen_at: Date.now() - 800, disk_runway_days: 19, node: 'k8s-storage-04' },
  { id: 'member-4', cell_id: CELL_ID, state: 'ONLINE', cordoned: false, bytes_used: 47_244_640_256, bytes_capacity: 146_006_118_912, shard_count: 9, raft_role: 'follower', last_seen_at: Date.now() - 4100, disk_runway_days: 20, node: 'k8s-storage-05' },
  { id: 'member-5', cell_id: CELL_ID, state: 'ONLINE', cordoned: true, bytes_used: 51_002_736_640, bytes_capacity: 146_006_118_912, shard_count: 9, raft_role: 'follower', last_seen_at: Date.now() - 1100, disk_runway_days: 19, node: 'k8s-storage-06' },
  { id: 'member-6', cell_id: CELL_ID, state: 'ONLINE', cordoned: false, bytes_used: 43_622_759_424, bytes_capacity: 146_006_118_912, shard_count: 8, raft_role: 'follower', last_seen_at: Date.now() - 2700, disk_runway_days: 21, node: 'k8s-storage-07' },
];

const CAPACITY_RUNWAY = {
  profile_id: PROFILE_ID,
  usable_bytes_remaining: 673_125_072_896,
  estimated_bytes_per_day: 212_336_640_000,
  runway_days: 3,
  local_disk: {
    total_bytes: 1_022_042_832_896,
    used_bytes: 348_917_760_000,
    free_bytes: 673_125_072_896,
    reserved_bytes: 128_423_955_456,
  },
  disk_guard_bands: {
    admission_min_free: 128_423_955_456,
    warning_min_free: 64_211_977_728,
    critical_min_free: 32_105_988_864,
    fail_closed_min_free: 16_052_994_432,
  },
  warnings: [],
};

// 24h time series for write throughput (bytes/s), sampled hourly
const WRITE_THROUGHPUT_24H = [
  2_100_000, 2_300_000, 1_800_000, 1_200_000, 800_000, 600_000,
  500_000, 900_000, 1_500_000, 2_400_000, 2_800_000, 3_100_000,
  3_400_000, 3_200_000, 2_900_000, 3_500_000, 3_800_000, 3_600_000,
  3_200_000, 2_800_000, 2_400_000, 2_100_000, 1_900_000, 2_457_600,
];

// Backend upload lag over 24h (seconds behind)
const BACKEND_LAG_24H = [
  12, 8, 5, 3, 2, 1, 1, 2, 6, 14, 18, 22,
  28, 24, 19, 32, 38, 34, 26, 20, 15, 11, 9, 14,
];

const BACKEND_LANES = [
  { lane: 'ingest_upload', queue_depth: 142, oldest_age_s: 14, bytes_backlog: 583_008_256, workers_active: 2, workers_max: 2, bytes_per_second: 1_474_560 },
  { lane: 'repair', queue_depth: 3, oldest_age_s: 4, bytes_backlog: 12_288, workers_active: 1, workers_max: 2, bytes_per_second: 491_520 },
  { lane: 'restore', queue_depth: 0, oldest_age_s: 0, bytes_backlog: 0, workers_active: 0, workers_max: 2, bytes_per_second: 491_520 },
  { lane: 'dr_copy', queue_depth: 0, oldest_age_s: 0, bytes_backlog: 0, workers_active: 0, workers_max: 1, bytes_per_second: 245_760 },
  { lane: 'rewrap', queue_depth: 1, oldest_age_s: 2, bytes_backlog: 4_096, workers_active: 1, workers_max: 1, bytes_per_second: 245_760 },
  { lane: 'scrub', queue_depth: 8, oldest_age_s: 45, bytes_backlog: 32_768, workers_active: 1, workers_max: 1, bytes_per_second: 122_880 },
];

const RAFT_HEALTH = {
  leader_available: true,
  quorum_available: true,
  current_leader: 'member-1',
  current_term: 847,
  members: STORAGE_MEMBERS.map(m => ({
    id: m.id,
    role: m.raft_role,
    commit_lag: m.id === 'member-1' ? 0 : Math.floor(Math.random() * 3),
    apply_lag: m.id === 'member-1' ? 0 : Math.floor(Math.random() * 2),
    snapshot_age_s: 180 + Math.floor(Math.random() * 120),
    compaction_lag_s: Math.floor(Math.random() * 60),
  })),
  proposal_failures_1h: 0,
  read_index_failures_1h: 0,
  term_changes_24h: 2,
};

const OPENBAO_HEALTH = {
  state: 'healthy',
  transit_latency_p50_ms: 1,
  transit_latency_p95_ms: 2,
  transit_latency_p99_ms: 4,
  dek_cache_hit_rate: 0.97,
  rewrap_lag_s: 2,
  audit_device_healthy: true,
  errors_1h: 0,
  key_version: 3,
  key_id: 'scrap-transit-prod-v1',
};

const OPERATIONS = [
  { id: 'op-7f3a1b2c', type: 'scrub', state: 'RUNNING', requested_by: 'local-operator', requested_at: Date.now() - 3_600_000, started_at: Date.now() - 3_540_000, progress: { total: 1200, completed: 847, message: 'Scrubbing shard 42/64' }, targets: [{ type: 'shard', id: 'shard-42' }] },
  { id: 'op-a9c2d4e1', type: 'repair', state: 'QUEUED', requested_by: 'system-auto-repair', requested_at: Date.now() - 120_000, progress: { total: 3, completed: 0, message: 'Awaiting worker' }, targets: [{ type: 'block', id: 'blk-01926f4a' }] },
  { id: 'op-b1e8f2a3', type: 'key_rotation', state: 'SUCCEEDED', requested_by: 'local-operator', requested_at: Date.now() - 86_400_000, started_at: Date.now() - 86_340_000, finished_at: Date.now() - 82_800_000, progress: { total: 64, completed: 64, message: 'All shards rewrapped' }, targets: [{ type: 'capacity_profile', id: PROFILE_ID }] },
  { id: 'op-c3f9a7b2', type: 'drain', state: 'SUCCEEDED', requested_by: 'local-operator', requested_at: Date.now() - 172_800_000, started_at: Date.now() - 172_740_000, finished_at: Date.now() - 169_200_000, progress: { total: 9, completed: 9, message: 'All shard replicas migrated' }, targets: [{ type: 'member', id: 'member-5' }] },
  { id: 'op-d4a0b8c3', type: 'capacity_override', state: 'EXPIRED', requested_by: 'local-operator', requested_at: Date.now() - 604_800_000, progress: { total: 1, completed: 0, message: 'Plan expired before start' }, targets: [{ type: 'capacity_profile', id: PROFILE_ID }] },
  { id: 'op-e5b1c9d4', type: 'scrub', state: 'SUCCEEDED', requested_by: 'system-scheduled', requested_at: Date.now() - 43_200_000, started_at: Date.now() - 43_140_000, finished_at: Date.now() - 39_600_000, progress: { total: 64, completed: 64, message: 'Full scrub complete, 0 issues' }, targets: [] },
];

const REPAIR_QUEUE = [
  { target: { type: 'block', shard_id: 'shard-17', block_id: 'blk-01926f4a-7d3e-7b8a-9c1d-2e4f6a8b0c3d' }, reason: 'checksum_mismatch', detected_at: Date.now() - 120_000 },
  { target: { type: 'document', tenant_id: 'acme-retail', transaction_id: 'tx-20260524-001847', document_name: 'bill.xml' }, reason: 'under_replicated', detected_at: Date.now() - 300_000 },
  { target: { type: 'block', shard_id: 'shard-42', block_id: 'blk-01926e8b-4c2d-7a1f-8b3e-5d7f9a0b2c4e' }, reason: 'peer_catchup', detected_at: Date.now() - 45_000 },
];

const ALERTS_ACTIVE = [
  { name: 'SCRAPMemberCordoned', class: 'operator_action', severity: 'warning', message: 'member-5 cordoned for planned maintenance', since: Date.now() - 7_200_000 },
];

// Write admission over 24h (requests/min)
const WRITE_ADMISSION_24H = [
  420, 460, 360, 240, 160, 120, 100, 180, 300, 480, 560, 620,
  680, 640, 580, 700, 760, 720, 640, 560, 480, 420, 380, 490,
];

// Read latency p95 over 24h (ms)
const READ_LATENCY_24H = [
  2.1, 1.9, 1.6, 1.3, 1.1, 1.0, 1.0, 1.2, 1.8, 2.4, 2.8, 3.1,
  3.4, 3.2, 2.9, 3.5, 3.8, 3.6, 3.2, 2.8, 2.4, 2.1, 1.9, 2.5,
];

// Document class distribution
const DOC_CLASS_DIST = { permanent: 9_472_891, ephemeral: 3_374_402 };
const PRIORITY_DIST = { critical_ingest: 1_927_094, normal: 8_991_004, bulk: 1_929_195 };

// Per-tenant top 5
const TOP_TENANTS = [
  { id: 'acme-retail', documents: 3_841_293, bytes: 89_412_000_000 },
  { id: 'globex-industrial', documents: 2_714_821, bytes: 72_318_000_000 },
  { id: 'initech-services', documents: 2_103_472, bytes: 61_204_000_000 },
  { id: 'hooli-salud', documents: 1_892_341, bytes: 54_892_000_000 },
  { id: 'piedpiper-edu', documents: 1_295_366, bytes: 41_091_000_000 },
];

// Kubernetes-enriched member detail
const K8S_MEMBERS = STORAGE_MEMBERS.map((m, i) => ({
  ...m,
  pod_name: `scrap-storage-${i}`,
  namespace: 'scrap-system',
  statefulset: 'scrap-storage',
  image: `ghcr.io/petabytecl/scrapd:${RELEASE_SHA.slice(0, 12)}`,
  restart_count: i === 5 ? 1 : 0,
  pod_age_hours: 360 + i * 24,
  pod_ip: `10.244.${i + 1}.12`,
  service_account: 'scrap-storage',
  pvc_name: `data-scrap-storage-${i}`,
  storage_class: 'local-nvme',
  pvc_phase: 'Bound',
  volume_name: `pv-nvme-storage-0${i + 1}`,
  node_ip: `192.168.10.${20 + i}`,
  zone: i < 3 ? 'cl-stgo-1a' : i < 5 ? 'cl-stgo-1b' : 'cl-stgo-1c',
  instance_type: 'bare-metal',
  kubelet_version: 'v1.31.4',
  container_runtime: 'containerd://1.7.24',
  os_image: 'Fedora CoreOS 40',
  node_taints: [{ key: 'scrap.petabyte.cl/dedicated', value: 'storage', effect: 'NoSchedule' }],
  cpu_request: '4000m', cpu_limit: '8000m',
  cpu_usage_millicores: 1200 + Math.floor(Math.random() * 2400),
  mem_request_bytes: 8_589_934_592,
  mem_limit_bytes: 16_106_127_360,
  mem_usage_bytes: 6_442_450_944 + Math.floor(Math.random() * 4_294_967_296),
  conditions: [
    { type: 'Initialized', status: true }, { type: 'Ready', status: true },
    { type: 'ContainersReady', status: true }, { type: 'PodScheduled', status: true },
  ],
  readiness: {
    process_ready: true, routable: true, byte_serving: true,
    placement_healthy: !m.cordoned, safe_to_evict: m.cordoned,
    disk_pressure: false, repair_lag: i === 2 ? 4 : 0,
  },
  eviction_safe: i === 5,
  eviction_warnings: i === 5 ? [] : [{ code: 'ACTIVE_VOTER', message: `Holds ${m.shard_count} active shard voters` }],
  shards: Array.from({ length: m.shard_count }, (_, si) => ({
    id: `shard-${(i * 10 + si) % 64}`,
    role: si === 0 && m.raft_role === 'leader' ? 'leader' : 'voter',
    commit_index: 84700 + Math.floor(Math.random() * 200),
    local_bytes: Math.floor(m.bytes_used / m.shard_count) + Math.floor((Math.random() - 0.5) * 1e9),
    byte_verified: true,
  })),
  disk_read_bps: 2_400_000 + Math.floor(Math.random() * 8_000_000),
  disk_write_bps: 1_800_000 + Math.floor(Math.random() * 6_000_000),
  disk_iops_read: 120 + Math.floor(Math.random() * 400),
  disk_iops_write: 80 + Math.floor(Math.random() * 300),
  disk_fsync_us: 80 + Math.floor(Math.random() * 120),
  disk_fs: 'xfs', disk_mount: '/var/lib/scrap/data',
  net_rx_bps: 1_200_000 + Math.floor(Math.random() * 4_000_000),
  net_tx_bps: 800_000 + Math.floor(Math.random() * 3_000_000),
  grpc_conns: 12 + Math.floor(Math.random() * 20),
  peer_conns: 6,
  open_block_bytes: 4_194_304 + Math.floor(Math.random() * 60_000_000),
  prepare_log_bytes: 524_288 + Math.floor(Math.random() * 2_000_000),
  events: i === 5 ? [
    { type: 'Normal', reason: 'MemberCordoned', message: 'Cordoned for planned maintenance', age_s: 7200 },
    { type: 'Normal', reason: 'DrainSucceeded', message: 'All 9 shard replicas migrated', age_s: 3600 },
    { type: 'Warning', reason: 'OOMKilled', message: 'Container restarted (exit 137)', age_s: 86400 },
  ] : i === 2 ? [
    { type: 'Warning', reason: 'RepairLag', message: 'Peer catch-up lag 4s on shard-22', age_s: 45 },
    { type: 'Normal', reason: 'BlockSealed', message: 'Sealed blk-01926f4a, 412 docs', age_s: 120 },
  ] : [
    { type: 'Normal', reason: 'BlockSealed', message: `Sealed, ${200 + Math.floor(Math.random() * 300)} docs`, age_s: 60 + Math.floor(Math.random() * 300) },
  ],
}));

const K8S_NODES = [
  { name: 'k8s-storage-01', zone: 'cl-stgo-1a', status: 'Ready', cpu: '16', mem_gi: 64, pods: 24, age_d: 92 },
  { name: 'k8s-storage-02', zone: 'cl-stgo-1a', status: 'Ready', cpu: '16', mem_gi: 64, pods: 22, age_d: 92 },
  { name: 'k8s-storage-03', zone: 'cl-stgo-1a', status: 'Ready', cpu: '16', mem_gi: 64, pods: 25, age_d: 92 },
  { name: 'k8s-storage-04', zone: 'cl-stgo-1b', status: 'Ready', cpu: '16', mem_gi: 64, pods: 21, age_d: 45 },
  { name: 'k8s-storage-05', zone: 'cl-stgo-1b', status: 'Ready', cpu: '16', mem_gi: 64, pods: 23, age_d: 45 },
  { name: 'k8s-storage-06', zone: 'cl-stgo-1c', status: 'Ready,SchedulingDisabled', cpu: '16', mem_gi: 64, pods: 18, age_d: 45 },
  { name: 'k8s-storage-07', zone: 'cl-stgo-1c', status: 'Ready', cpu: '16', mem_gi: 64, pods: 20, age_d: 30 },
];

const PDB_STATUS = { name: 'scrap-storage', namespace: 'scrap-system', max_unavailable: 1, current_healthy: 7, desired_healthy: 6, disruptions_allowed: 1 };

// ─── Connected clients (mTLS-authenticated workload identities) ──────────────
const CLIENT_SERVICES = [
  {
    id: 'etl-ingest', name: 'etl-ingest', kind: 'writer',
    workload_identity: 'spiffe://petabyte.cl/ns/billing/sa/etl-ingest',
    namespace: 'billing', replicas: 8,
    mtls_verified: true, cert_expires_in_days: 87,
    capabilities: ['WriteDocument', 'CompleteTransaction'],
    rps: 642, error_rate: 0.0002, p95_ms: 8,
    bytes_in_per_sec: 2_457_600, bytes_out_per_sec: 12_288,
    connections: 12, last_seen_at: Date.now() - 800,
    status: 'healthy',
  },
  {
    id: 'etl-finalize', name: 'etl-finalize', kind: 'writer',
    workload_identity: 'spiffe://petabyte.cl/ns/billing/sa/etl-finalize',
    namespace: 'billing', replicas: 4,
    mtls_verified: true, cert_expires_in_days: 87,
    capabilities: ['CompleteTransaction', 'HeadDocument'],
    rps: 184, error_rate: 0.0001, p95_ms: 12,
    bytes_in_per_sec: 24_576, bytes_out_per_sec: 49_152,
    connections: 6, last_seen_at: Date.now() - 1200,
    status: 'healthy',
  },
  {
    id: 'billing-export', name: 'billing-export', kind: 'reader',
    workload_identity: 'spiffe://petabyte.cl/ns/billing/sa/billing-export',
    namespace: 'billing', replicas: 6,
    mtls_verified: true, cert_expires_in_days: 87,
    capabilities: ['ReadDocument', 'HeadDocument', 'FindDocuments'],
    rps: 412, error_rate: 0.0008, p95_ms: 4,
    bytes_in_per_sec: 4_096, bytes_out_per_sec: 3_686_400,
    connections: 14, last_seen_at: Date.now() - 600,
    status: 'healthy',
  },
  {
    id: 'tax-validator', name: 'tax-validator', kind: 'mixed',
    workload_identity: 'spiffe://petabyte.cl/ns/compliance/sa/tax-validator',
    namespace: 'compliance', replicas: 3,
    mtls_verified: true, cert_expires_in_days: 42,
    capabilities: ['ReadDocument', 'WriteDocument', 'FindDocuments'],
    rps: 87, error_rate: 0.0015, p95_ms: 18,
    bytes_in_per_sec: 491_520, bytes_out_per_sec: 614_400,
    connections: 4, last_seen_at: Date.now() - 2100,
    status: 'healthy',
  },
  {
    id: 'pdf-renderer', name: 'pdf-renderer', kind: 'mixed',
    workload_identity: 'spiffe://petabyte.cl/ns/rendering/sa/pdf-renderer',
    namespace: 'rendering', replicas: 5,
    mtls_verified: true, cert_expires_in_days: 87,
    capabilities: ['ReadDocument', 'WriteDocument'],
    rps: 231, error_rate: 0.0034, p95_ms: 22,
    bytes_in_per_sec: 1_228_800, bytes_out_per_sec: 2_457_600,
    connections: 8, last_seen_at: Date.now() - 1800,
    status: 'degraded',
    degraded_reason: 'Error rate above warning threshold (3.4‰)',
  },
  {
    id: 'audit-collector', name: 'audit-collector', kind: 'reader',
    workload_identity: 'spiffe://petabyte.cl/ns/security/sa/audit-collector',
    namespace: 'security', replicas: 2,
    mtls_verified: true, cert_expires_in_days: 14,
    capabilities: ['HeadDocument', 'FindDocuments'],
    rps: 32, error_rate: 0, p95_ms: 2,
    bytes_in_per_sec: 2_048, bytes_out_per_sec: 12_288,
    connections: 2, last_seen_at: Date.now() - 500,
    status: 'warning',
    degraded_reason: 'mTLS certificate expires in 14 days',
  },
  {
    id: 'archive-worker', name: 'archive-worker', kind: 'reader',
    workload_identity: 'spiffe://petabyte.cl/ns/archival/sa/archive-worker',
    namespace: 'archival', replicas: 2,
    mtls_verified: true, cert_expires_in_days: 87,
    capabilities: ['ReadDocument', 'FindDocuments'],
    rps: 64, error_rate: 0, p95_ms: 6,
    bytes_in_per_sec: 0, bytes_out_per_sec: 8_192_000,
    connections: 4, last_seen_at: Date.now() - 3400,
    status: 'healthy',
  },
  {
    id: 'webhook-dispatcher', name: 'webhook-dispatcher', kind: 'reader',
    workload_identity: 'spiffe://petabyte.cl/ns/integrations/sa/webhook-dispatcher',
    namespace: 'integrations', replicas: 3,
    mtls_verified: true, cert_expires_in_days: 87,
    capabilities: ['ReadDocument', 'HeadDocument'],
    rps: 124, error_rate: 0.0004, p95_ms: 5,
    bytes_in_per_sec: 0, bytes_out_per_sec: 1_843_200,
    connections: 6, last_seen_at: Date.now() - 1100,
    status: 'healthy',
  },
  {
    id: 'customer-portal', name: 'customer-portal-api', kind: 'reader',
    workload_identity: 'spiffe://petabyte.cl/ns/portal/sa/customer-portal-api',
    namespace: 'portal', replicas: 4,
    mtls_verified: true, cert_expires_in_days: 87,
    capabilities: ['ReadDocument', 'HeadDocument', 'FindDocuments'],
    rps: 218, error_rate: 0.0007, p95_ms: 7,
    bytes_in_per_sec: 0, bytes_out_per_sec: 4_915_200,
    connections: 10, last_seen_at: Date.now() - 400,
    status: 'healthy',
  },
];

// Service mesh metric aggregates
const SERVICE_MESH = {
  total_clients: CLIENT_SERVICES.length,
  total_replicas: CLIENT_SERVICES.reduce((s, c) => s + c.replicas, 0),
  active_connections: CLIENT_SERVICES.reduce((s, c) => s + c.connections, 0),
  total_rps: CLIENT_SERVICES.reduce((s, c) => s + c.rps, 0),
  weighted_error_rate: CLIENT_SERVICES.reduce((s, c) => s + c.rps * c.error_rate, 0) / CLIENT_SERVICES.reduce((s, c) => s + c.rps, 0),
  mtls_verified_pct: CLIENT_SERVICES.filter(c => c.mtls_verified).length / CLIENT_SERVICES.length,
  cert_expiring_soon: CLIENT_SERVICES.filter(c => c.cert_expires_in_days < 30).length,
};

// Backend dependencies (downstream services the gateway calls)
const BACKEND_DEPS = [
  {
    id: 'backend', name: 'S3 backend', kind: 'storage',
    endpoint: 'localstack.scrap-system:4566',
    status: 'healthy', rps: 38, p95_ms: 3, error_rate: 0,
  },
  {
    id: 'openbao', name: 'OpenBao Transit', kind: 'crypto',
    endpoint: 'openbao.scrap-system:8200',
    status: 'healthy', rps: 12, p95_ms: 1, error_rate: 0,
  },
];

// Circuit breaker state
const CIRCUIT_BREAKERS = {
  backend: { state: 'closed', consecutive_failures: 0, error_rate_permille: 0, samples: 1842 },
  openbao: { state: 'closed', consecutive_failures: 0, error_rate_permille: 0, samples: 924 },
  disk: { state: 'closed', consecutive_failures: 0, error_rate_permille: 0, samples: 3684 },
};

window.MOCK = {
  CELL_ID, PROFILE_ID, RELEASE_SHA,
  CLUSTER_SUMMARY, STORAGE_MEMBERS, CAPACITY_RUNWAY,
  WRITE_THROUGHPUT_24H, BACKEND_LAG_24H, BACKEND_LANES,
  RAFT_HEALTH, OPENBAO_HEALTH, OPERATIONS, REPAIR_QUEUE,
  ALERTS_ACTIVE, WRITE_ADMISSION_24H, READ_LATENCY_24H,
  DOC_CLASS_DIST, PRIORITY_DIST, TOP_TENANTS, CIRCUIT_BREAKERS,
  K8S_MEMBERS, K8S_NODES, PDB_STATUS,
  CLIENT_SERVICES, SERVICE_MESH, BACKEND_DEPS,
};
