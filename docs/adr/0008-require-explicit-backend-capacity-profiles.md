# Require explicit backend capacity profiles

Status: accepted

Production deployments require explicit measured capacity profiles for accepted ingress, backend request budgets, backend byte budgets, lane budgets, ramp policy, circuit breakers, disk guard bands, and runway thresholds. Missing, invalid, or mathematically unsafe production profiles fail closed unless an explicit non-production risk mode is enabled.

This is stricter than relying on provider defaults or alerts, but S.C.R.A.P. acknowledges writes before backend upload. Local replicated durability and write admission must therefore prove that accepted workload can be protected and drained before disk runway becomes unsafe.
