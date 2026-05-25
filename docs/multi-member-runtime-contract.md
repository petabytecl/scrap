# Multi-Member Runtime Contract

Status: implementation contract for issue `#240`

This document defines the runtime boundary that turns multiple `scrapd`
StatefulSet pods into one S.C.R.A.P. cell. It narrows the accepted ADRs and
design notes into the contract required by the implementation issues that add
member identity, peer discovery, admin aggregation, metadata routing, and
replicated write ACK validation.

Today, the local production-like kind environment schedules several `scrapd`
pods, but the process still enters local non-production mode. Each pod opens its
own local store and reports the hardcoded `local` member. That shape validates
Kubernetes scheduling and admin UI wiring only. It is not a distributed storage
runtime.

## Runtime Identities

`cell_id` is the stable operator-assigned identity for one authoritative
S.C.R.A.P. deployment. Every storage member in a StatefulSet uses the same
`cell_id`. The value must be a lowercase ASCII slug that is safe in backend
keys, logs, metrics, and operation records. It is configuration, not a value
derived from the Kubernetes namespace, cluster name, or pod IP.

`member_slot_id` is the Kubernetes scheduling slot. In the StatefulSet runtime
it is derived from the pod hostname:

```text
hostname:       scrapd-0
statefulset:    scrapd
ordinal:        0
member_slot_id: scrapd-0
peer DNS:       scrapd-0.scrapd.<namespace>.svc.<cluster_domain>
```

The slot is used for stable peer discovery and for operator messages such as
"pod `scrapd-0` is missing." It is not by itself the storage identity.

`member_id` is the durable storage member identity. Production startup obtains
it from an identity record on the member data PVC and verifies that the cluster
metadata still binds that `member_id` to the current `cell_id` and
`member_slot_id`. A normal pod restart must present the same `member_id` from
the same PVC.

Initial cluster bootstrap may create the first binding from
`member_slot_id -> member_id`, but the binding is then persisted both on the
member PVC and in cluster metadata. If the PVC is lost, the replacement pod may
reuse the same `member_slot_id`, but it must not silently reuse the old
`member_id`. The operator must run the lost-member or replacement workflow so
S.C.R.A.P. can catch up metadata, verify bytes, and perform membership changes
under the placement rules.

For explicit non-production local mode, `scrapd` may default
`cell_id=local` and `member_id=local` so existing one-process workflows keep
working. Multi-member development mode must make the weaker identity contract
visible in admin output and must not satisfy production write ACK gates.

## Peer Discovery

Authoritative Kubernetes deployments use:

- one storage-member StatefulSet;
- one durable data PVC per storage member;
- one headless member Service for stable peer DNS;
- separate public, admin, and peer listener ports;
- NetworkPolicies that restrict peer and admin traffic to allowed workloads.

StatefulSet discovery resolves every expected slot through the headless Service:

```text
<member_slot_id>.<headless_service>.<namespace>.svc.<cluster_domain>:<peer_port>
```

The shorter `<member_slot_id>.<headless_service>.<namespace>.svc` form is
only a search-path convenience when the cluster DNS domain is configured to
complete it. Runtime configuration should store or derive the full DNS name,
including the cluster domain, so non-default Kubernetes DNS domains work.

The peer handshake must verify `cell_id`, `member_id`, and
`member_slot_id`. A peer address alone is not authority to join a shard or serve
bytes. Peers whose identity record conflicts with cluster metadata are
non-serving and must surface an admin warning.

Non-kind deployments use the same logical model even if discovery is not
Kubernetes DNS. A static peer list, service discovery record, or future control
plane may provide candidate peer addresses, but the peer handshake and
cluster-metadata binding remain authoritative. Pod IPs, load-balanced Service
addresses, and DNS names are routability hints, not storage identity.

## Metadata Authority And Placement

Shard consensus metadata is the source of truth for document visibility,
physical byte references, replica membership, upload outbox entries, restore
state, repair state, and operation coordination.

Runtime responsibilities are assigned as follows:

| Responsibility | Owner |
| --- | --- |
| Public/admin gRPC validation, authz, and server wiring | `internal/api`, `internal/node`, `cmd/scrapd` |
| Cell/member startup, shard routing, leader forwarding, and visibility workflow | future `internal/shard` plus `cmd/scrapd` wiring |
| Durable command log, snapshots, replay, ReadIndex, and quorum checks | `internal/raftmeta` |
| Authoritative document, transaction, replica, operation, and upload state | `internal/metastore` |
| Replica set selection and distinct-node/failure-domain checks | `internal/placement` |
| Peer byte prepare, transfer validation, and replica receipts | `internal/replication` |
| Local durable block, index, openlog, and checksum verification | `internal/blockstore` and current `internal/localstorage` substrate |
| Backend upload, restore source verification, and retry-safe upload outbox execution | `internal/backendupload`, `internal/backend` |
| Aggregated member, capacity, placement, repair, and operation views | admin applications exposed through `internal/api` and `internal/adminui` |

Every write is assigned to a shard by the transaction-keyed shard function. If a
request reaches a member that is not the shard leader, that member either
forwards to the current leader or returns a retryable routing error that names
the required routing condition. It must not accept an isolated local write that
can diverge from the shard metadata authority.

Placement is production-fail-closed. A production shard must not silently lower
its replica or failure-domain requirements because Kubernetes has too few
eligible nodes. If the configured placement profile cannot be satisfied, the
shard reports placement-unhealthy and write admission may continue only while
the selected durability policy can still be met.

## Write ACK, Visibility, And Backend Upload

S.C.R.A.P. distinguishes these write states:

1. **Accepted for processing:** request validation, authz, idempotency key, and
   shard routing have succeeded. No durability promise has been made.
2. **Local bytes durable:** the receiving/leader member has written, verified,
   and synced prepared bytes to its local durable store. The document is still
   invisible.
3. **Peer bytes durable:** the required peer members have fsynced and
   checksum-validated the prepared bytes and returned replica receipts. The
   document is still invisible.
4. **Metadata committed:** shard consensus has durably committed the metadata
   command that records document visibility, physical refs, envelopes, and
   upload/repair obligations.
5. **Visible:** reads may use committed metadata and any committed,
   checksum-valid local or peer replica. Strong read-after-write is scoped to
   the authoritative cell.
6. **Backend upload recorded:** the durable metadata outbox records the need to
   upload sealed backend objects. Backend upload is retry-safe and asynchronous.
7. **Backend uploaded and verified:** backend objects and envelopes are
   available as cold durability or restore sources according to the backend
   profile.

A production write ACK may be returned only after the configured local and peer
byte durability requirements are met and the authoritative metadata commit makes
the document visible. Backend upload is not in the client ACK path. Upload lag
can create admission pressure when it threatens the local durability window, but
lag alone does not invalidate an already acknowledged write.

If the client times out or the connection breaks after the write entered the
durability path, the outcome may be unknown to the client. Retrying the same
logical write must use deterministic command IDs and idempotency checks so the
retry resolves to the committed document or a clear conflict.

## Read Routing And Consistency

`HeadDocument` and `ReadDocument` consult authoritative shard metadata. Metadata
may be served by the leader or by a follower that proves freshness through the
accepted read protocol. Lease reads remain disabled until timing and fencing
assumptions have explicit evidence.

Bytes may be served from:

1. a committed checksum-valid local replica;
2. a committed checksum-valid peer replica;
3. a verified backend object when cold restore or read-through policy allows.

Prepared bytes that are not referenced by committed metadata are not visible.
Metadata catch-up without byte verification is not enough to make a member
byte-serving. Returning or replacement members start non-serving and become
read-eligible only after catching up metadata and verifying the local byte refs
they claim.

## Admin Aggregation

Admin APIs and the admin UI must report the cell view, not just the pod reached
by a port-forward or Service load balancer. Multi-member inspect output should
include:

- `cell_id`, release profile, and production write ACK gate state;
- every known member with `member_id`, `member_slot_id`, routability,
  byte-serving state, cordon/drain state, and last-seen time;
- per-member and aggregate capacity;
- shard leader/quorum/placement health;
- peer prepare, catch-up, repair, and backend upload lag;
- warnings for unreachable peers, identity conflicts, partial aggregation, and
  local non-production mode.

Partial aggregation must be explicit. If one peer cannot be queried, admin
output may still show healthy local state, but it must include a warning and
must not present the cell as fully healthy.

## Local Production-Like Kind Scope

`make local-dev-prod-up` is a manual validation environment, not production
readiness evidence by itself. OpenBao and LocalStack may remain in development
mode there.

The local production-like kind environment can validate:

- multiple Kubernetes worker nodes and a multi-replica `scrapd` StatefulSet;
- anti-affinity and pod spread across kind worker nodes;
- headless peer DNS and peer port reachability;
- distinct `member_slot_id` values and non-`local` multi-member admin output
  after #241;
- admin aggregation across all scheduled members after #242;
- metadata routing, leader forwarding, and divergent-write prevention after
  #243;
- replicated ACK success and fail-closed quorum behavior after #244.

It cannot validate:

- live production capacity, ingress, retention, or backend-provider budgets;
- OpenBao HA, unseal custody, audit retention, or key-loss recovery;
- real cloud object-store behavior, IAM, lifecycle, archive restore, or
  provider throttling;
- five-replica/seven-node production placement unless the local profile is
  explicitly expanded to that shape;
- zone, rack, region, cloud-provider, or subpoena-grade forensic guarantees;
- downstream GitOps approval, release-owner signoff, or production write ACK
  readiness gates.

Therefore, local production-like green smoke may support implementation
confidence, but the production write ACK gate remains blocked until release
evidence proves the target deployment profile.

## Implementation Sequence

The follow-up issues should preserve this order:

1. Add explicit `cell_id`, `member_slot_id`, `member_id`, and peer discovery
   configuration to `scrapd` startup.
2. Make admin member/capacity surfaces aggregate all discovered members and
   report partial aggregation honestly.
3. Wire shard metadata authority, placement decisions, and request routing into
   the multi-member runtime so Service-balanced writes cannot diverge.
4. Validate replicated write ACK success and fail-closed quorum behavior in the
   local production-like kind environment.

Each slice must keep single-node local non-production mode available and
clearly labeled as non-production.
