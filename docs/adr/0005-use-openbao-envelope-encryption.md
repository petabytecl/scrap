# Use OpenBao Transit envelope encryption

Status: accepted

Backend encryption uses envelope encryption with OpenBao Transit as the deployment-scoped KEK provider and per-block DEKs for encrypted backend block/index payloads. Routine key rotation rewraps small `.env` envelope objects rather than re-encrypting large `.blk` payloads.

This keeps key rotation operationally feasible for long-lived archived data, but makes OpenBao availability, audit, snapshots, and key-version retention part of the durability model. Losing required Transit key material makes encrypted backend data unrecoverable.
