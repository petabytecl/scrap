# Use a custom gRPC storage API

Status: accepted

S.C.R.A.P. uses a custom gRPC API as the primary service-fleet contract instead of exposing S3 compatibility in v1. The workload needs streaming writes, strong transaction-scoped document reads, typed restore/cold-read responses, explicit workflow metadata, and precise write admission semantics; forcing those through an S3-shaped API would hide important behavior behind object-store conventions.

Backend portability remains a requirement. S3, GCS, Azure Blob, and filesystem storage are backend implementations behind the gateway, not the API contract exposed to ETL services.
