# Durable placement identity

Status: Accepted

Date: 2026-07-09

## Context

Finding `H-11` shows that changing a valid Shard placement/slot map can hide
existing Transactions and allow the same Document identity to be ACKed in a
different Shard. Startup validates only the current placement file.

## Decision

Each Cell persists a placement identity (digest and/or epoch) alongside Shard
data. Startup compares configured placement with the persisted identity.

Slot remapping that would move existing Transactions is rejected until an
explicit coordinated Shard-transfer protocol completes and updates the persisted
identity. Valid initial placement on an empty Cell is allowed.
