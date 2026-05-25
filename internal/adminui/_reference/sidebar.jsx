// Sidebar navigation for the S.C.R.A.P. admin UI

const ADMIN_ICONS = {
  overview: "M3.75 6A2.25 2.25 0 0 1 6 3.75h2.25A2.25 2.25 0 0 1 10.5 6v2.25a2.25 2.25 0 0 1-2.25 2.25H6a2.25 2.25 0 0 1-2.25-2.25V6ZM3.75 15.75A2.25 2.25 0 0 1 6 13.5h2.25a2.25 2.25 0 0 1 2.25 2.25V18a2.25 2.25 0 0 1-2.25 2.25H6A2.25 2.25 0 0 1 3.75 18v-2.25ZM13.5 6a2.25 2.25 0 0 1 2.25-2.25H18A2.25 2.25 0 0 1 20.25 6v2.25A2.25 2.25 0 0 1 18 10.5h-2.25a2.25 2.25 0 0 1-2.25-2.25V6ZM13.5 15.75a2.25 2.25 0 0 1 2.25-2.25H18a2.25 2.25 0 0 1 2.25 2.25V18A2.25 2.25 0 0 1 18 20.25h-2.25a2.25 2.25 0 0 1-2.25-2.25v-2.25Z",
  capacity: "M20.25 6.375c0 2.278-3.694 4.125-8.25 4.125S3.75 8.653 3.75 6.375m16.5 0c0-2.278-3.694-4.125-8.25-4.125S3.75 4.097 3.75 6.375m16.5 0v11.25c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125V6.375m16.5 0v3.75m-16.5-3.75v3.75m16.5 0v3.75C20.25 16.153 16.556 18 12 18s-8.25-1.847-8.25-4.125v-3.75m16.5 0c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125",
  members: "M5.25 14.25h13.5m-13.5 0a3 3 0 0 1-3-3m3 3a3 3 0 1 0 0 6h13.5a3 3 0 1 0 0-6m-13.5 0a3 3 0 0 1-3-3m3 3h13.5m-13.5 0a3 3 0 0 0-3 3m19.5-6a3 3 0 0 0-3-3m3 3a3 3 0 0 1-3 3m3-3v-3m-19.5 9a3 3 0 0 1 3-3m-3 3v-3m3 3h13.5a3 3 0 0 0 3-3m-19.5 0a3 3 0 0 0 3 3",
  backend: "M12 16.5V9.75m0 0 3 3m-3-3-3 3M6.75 19.5a4.5 4.5 0 0 1-1.41-8.775 5.25 5.25 0 0 1 10.233-2.33 3 3 0 0 1 3.758 3.848A3.752 3.752 0 0 1 18 19.5H6.75Z",
  raft: "M7.5 21 3 16.5m0 0L7.5 12M3 16.5h13.5m0-13.5L21 7.5m0 0L16.5 12M21 7.5H7.5",
  openbao: "M16.5 10.5V6.75a4.5 4.5 0 1 0-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 0 0 2.25-2.25v-6.75a2.25 2.25 0 0 0-2.25-2.25H6.75a2.25 2.25 0 0 0-2.25 2.25v6.75a2.25 2.25 0 0 0 2.25 2.25Z",
  operations: "M3.75 12h16.5m-16.5 3.75h16.5M3.75 19.5h16.5M5.625 4.5h12.75a1.875 1.875 0 0 1 0 3.75H5.625a1.875 1.875 0 0 1 0-3.75Z",
  repair: "M11.42 15.17 17.25 21A2.652 2.652 0 0 0 21 17.25l-5.877-5.877M11.42 15.17l2.496-3.03c.317-.384.74-.626 1.208-.766M11.42 15.17l-4.655 5.653a2.548 2.548 0 1 1-3.586-3.586l6.837-5.63m5.108-.233c.55-.164 1.163-.188 1.743-.14a4.5 4.5 0 0 0 4.486-6.336l-3.276 3.277a3.004 3.004 0 0 1-2.25-2.25l3.276-3.276a4.5 4.5 0 0 0-6.336 4.486c.091 1.076-.071 2.264-.904 2.95l-.102.085m-1.745 1.437L5.909 7.5H4.5L2.25 3.75l1.5-1.5L7.5 4.5v1.409l4.26 4.26m-1.745 1.437 1.745-1.437m6.615 8.206L15.75 15.75M4.867 19.125h.008v.008h-.008v-.008Z",
  clients: "M18 18.72a9.094 9.094 0 0 0 3.741-.479 3 3 0 0 0-4.682-2.72m.94 3.198.001.031c0 .225-.012.447-.037.666A11.944 11.944 0 0 1 12 21c-2.17 0-4.207-.576-5.963-1.584A6.062 6.062 0 0 1 6 18.719m12 0a5.971 5.971 0 0 0-.941-3.197m0 0A5.995 5.995 0 0 0 12 12.75a5.995 5.995 0 0 0-5.058 2.772m0 0a3 3 0 0 0-4.681 2.72 8.986 8.986 0 0 0 3.74.477m.94-3.197a5.971 5.971 0 0 0-.94 3.197M15 6.75a3 3 0 1 1-6 0 3 3 0 0 1 6 0Zm6 3a2.25 2.25 0 1 1-4.5 0 2.25 2.25 0 0 1 4.5 0Zm-13.5 0a2.25 2.25 0 1 1-4.5 0 2.25 2.25 0 0 1 4.5 0Z",
  alerts: "M14.857 17.082a23.848 23.848 0 0 0 5.454-1.31A8.967 8.967 0 0 1 18 9.75V9A6 6 0 0 0 6 9v.75a8.967 8.967 0 0 1-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 0 1-5.714 0m5.714 0a3 3 0 1 1-5.714 0",
};

const AdminIcon = ({ path, size = 18 }) => (
  <svg width={size} height={size} fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor" aria-hidden="true">
    <path strokeLinecap="round" strokeLinejoin="round" d={path}/>
  </svg>
);

// Grouped nav structure for visual hierarchy
const NAV_GROUPS = [
  {
    label: 'Cluster',
    items: [
      { id: 'overview', label: 'Overview', icon: ADMIN_ICONS.overview },
      { id: 'capacity', label: 'Capacity', icon: ADMIN_ICONS.capacity },
    ],
  },
  {
    label: 'Infrastructure',
    items: [
      { id: 'members', label: 'Members', icon: ADMIN_ICONS.members },
      { id: 'raft', label: 'Raft consensus', icon: ADMIN_ICONS.raft },
    ],
  },
  {
    label: 'Data plane',
    items: [
      { id: 'clients', label: 'Service mesh', icon: ADMIN_ICONS.clients },
      { id: 'backend', label: 'Backend', icon: ADMIN_ICONS.backend },
      { id: 'openbao', label: 'OpenBao', icon: ADMIN_ICONS.openbao },
    ],
  },
  {
    label: 'Reliability',
    items: [
      { id: 'operations', label: 'Operations', icon: ADMIN_ICONS.operations },
      { id: 'repair', label: 'Repair', icon: ADMIN_ICONS.repair },
    ],
  },
];

// Flat list for label lookups
const NAV_ITEMS = NAV_GROUPS.flatMap(g => g.items);

const Sidebar = ({ activeView, onNavigate }) => {
  const alerts = MOCK.ALERTS_ACTIVE;
  return (
    <aside style={{
      width: 220, minHeight: '100vh', background: 'var(--pb-background-alt)',
      borderRight: '1px solid var(--pb-border-subtle)',
      display: 'flex', flexDirection: 'column',
      position: 'fixed', top: 0, left: 0, bottom: 0, zIndex: 40,
    }}>
      {/* Logo + brand */}
      <div style={{ padding: '24px 20px 8px' }}>
        <img src="assets/logo-petabyte-color-white.png" alt="Petabyte" style={{ height: 22, width: 'auto', display: 'block' }}/>
      </div>
      <div style={{
        padding: '0 20px 20px',
        fontFamily: 'var(--pb-font-mono)', fontSize: 10, fontWeight: 500,
        letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--pb-fg4)',
      }}>S.C.R.A.P. Admin</div>

      {/* Nav groups */}
      <nav style={{ flex: 1, padding: '0 12px', display: 'flex', flexDirection: 'column', gap: 18, overflowY: 'auto' }}>
        {NAV_GROUPS.map(group => (
          <div key={group.label}>
            <div style={{
              fontFamily: 'var(--pb-font-mono)', fontSize: 10, fontWeight: 500,
              letterSpacing: '0.16em', textTransform: 'uppercase',
              color: 'var(--pb-fg4)', padding: '0 8px 8px', opacity: 0.7,
            }}>{group.label}</div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
              {group.items.map(item => {
                const isActive = activeView === item.id;
                return (
                  <button key={item.id} onClick={() => onNavigate(item.id)} style={{
                    display: 'flex', alignItems: 'center', gap: 10,
                    padding: '7px 10px', borderRadius: 'var(--pb-radius-md)',
                    background: isActive ? 'rgba(0,147,208,0.10)' : 'transparent',
                    border: 'none', cursor: 'pointer',
                    color: isActive ? 'var(--pb-primary)' : 'var(--pb-fg2)',
                    fontSize: 13, fontFamily: 'var(--pb-font-sans)', fontWeight: isActive ? 600 : 400,
                    textAlign: 'left', transition: 'all 150ms var(--pb-ease-standard)',
                    width: '100%', position: 'relative',
                  }}
                  onMouseEnter={e => { if (!isActive) { e.currentTarget.style.background = 'rgba(148,163,184,0.06)'; e.currentTarget.style.color = 'var(--pb-fg1)'; }}}
                  onMouseLeave={e => { if (!isActive) { e.currentTarget.style.background = 'transparent'; e.currentTarget.style.color = 'var(--pb-fg2)'; }}}
                  >
                    {isActive && <span style={{ position: 'absolute', left: -8, top: 8, bottom: 8, width: 2, background: 'var(--pb-primary)', borderRadius: 2 }}></span>}
                    <AdminIcon path={item.icon} size={16}/>
                    <span>{item.label}</span>
                    {item.id === 'repair' && MOCK.REPAIR_QUEUE.length > 0 && (
                      <span style={{
                        marginLeft: 'auto', background: 'rgba(251,191,36,0.15)', color: '#FBBF24',
                        fontSize: 10, fontWeight: 600, borderRadius: 9999,
                        padding: '1px 7px', fontFamily: 'var(--pb-font-mono)',
                      }}>{MOCK.REPAIR_QUEUE.length}</span>
                    )}
                  </button>
                );
              })}
            </div>
          </div>
        ))}
      </nav>

      {/* Alerts bar */}
      {alerts.length > 0 && (
        <div style={{ padding: '12px 14px', margin: '12px 12px 8px', borderRadius: 'var(--pb-radius-md)', background: 'rgba(251,191,36,0.06)', border: '1px solid rgba(251,191,36,0.18)' }}>
          <div style={{ fontSize: 10, fontFamily: 'var(--pb-font-mono)', fontWeight: 500, letterSpacing: '0.16em', textTransform: 'uppercase', color: '#FBBF24', marginBottom: 8, display: 'flex', alignItems: 'center', gap: 6 }}>
            <AdminIcon path={ADMIN_ICONS.alerts} size={12}/> {alerts.length} active alert{alerts.length > 1 ? 's' : ''}
          </div>
          {alerts.map((a, i) => (
            <div key={i} style={{ fontSize: 11, color: 'var(--pb-fg3)', lineHeight: 1.45 }}>{a.message}</div>
          ))}
        </div>
      )}

      {/* Footer: cell identity */}
      <div style={{
        padding: '14px 20px', borderTop: '1px solid var(--pb-border-subtle)',
        fontSize: 11, color: 'var(--pb-fg4)', fontFamily: 'var(--pb-font-mono)',
        display: 'flex', flexDirection: 'column', gap: 5,
      }}>
        {[
          { k: 'cell', v: MOCK.CELL_ID },
          { k: 'profile', v: MOCK.PROFILE_ID },
          { k: 'sha', v: fmt.sha(MOCK.RELEASE_SHA) },
        ].map(row => (
          <div key={row.k} style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
            <span style={{ opacity: 0.7 }}>{row.k}</span>
            <span style={{ color: 'var(--pb-fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.v}</span>
          </div>
        ))}
      </div>
    </aside>
  );
};

Object.assign(window, { Sidebar, AdminIcon, ADMIN_ICONS, NAV_ITEMS, NAV_GROUPS });
