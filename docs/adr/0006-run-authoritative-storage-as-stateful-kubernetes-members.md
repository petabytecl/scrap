# Run authoritative storage as stateful Kubernetes members

Status: accepted

Authoritative S.C.R.A.P. deployments use a storage-member StatefulSet with local durable PVCs. Storage-member pods expose the service API, participate in shard membership, and forward requests internally when another member is the right leader or safe read replica.

V1 does not use a separate stateless ingress tier or one pod per shard. This keeps pod count tied to storage capacity instead of shard count, but requires S.C.R.A.P. to own placement health, byte-serving readiness, drain safety, and replica replacement rather than treating Kubernetes pod readiness as storage safety.
