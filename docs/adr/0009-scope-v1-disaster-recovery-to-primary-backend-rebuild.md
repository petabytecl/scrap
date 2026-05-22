# Scope v1 disaster recovery to primary-backend rebuild

Status: accepted

V1 disaster recovery restores a new cluster from primary backend block/index/envelope objects, published metadata snapshots/tails, and restored OpenBao Transit key material. Always-on secondary backend replication is supported only as schema and admin copy/verify jobs, not as a v1 RPO/RTO promise.

This keeps the first release focused on no acknowledged data loss through local replication plus primary backend recovery. Cross-region or cross-cloud failover requires separate lag SLOs, source fencing, secondary verification, and operator drills before it can become a product contract.
