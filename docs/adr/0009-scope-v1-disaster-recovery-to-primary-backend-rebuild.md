# Scope v1 disaster recovery to primary-backend rebuild

Status: accepted

V1 disaster recovery restores a new cluster from primary backend block/index/envelope objects, published metadata snapshots/tails, and restored OpenBao Transit key material. V1 reports measured recovery evidence from drills and real restores, but it does not make a formal business RTO/RPO promise.

Always-on secondary backend replication remains post-v1. The schema and admin copy/verify hooks may support a later secondary design, but cross-region or cross-cloud failover requires separate lag SLOs, source fencing, secondary verification, and operator drills before it can become a product contract.
