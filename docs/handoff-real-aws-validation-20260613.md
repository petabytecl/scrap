# Real AWS Validation Handoff

Date: 2026-06-13

Status: Documentation-only handoff. Do not treat this as implemented
infrastructure.

Repo: `/home/coto/dev/petabyte/scrap-v2`

Branch at handoff creation: `v2`

Tracker: issue `#429`, "Pre-v2 release: capture real S3/IAM production
rehearsal evidence"

## Purpose

This handoff describes the remaining real AWS validation work that must happen
on a machine with access to the target AWS account. The current development
machine does not have the AWS access required to complete the gate.

The goal is not to deploy S.C.R.A.P. to AWS. The goal is to run the existing
production rehearsal against a real non-local S3 Backend and real IAM
credentials, then link sanitized evidence to issue `#429`.

This document intentionally provides only infrastructure outlines. It must not
be read as a request to add CDK, CloudFormation, Terraform, or live AWS resources
to this repo.

## Existing Contract Sources

Read these before running the AWS validation:

- `docs/production-rehearsal.md` defines the rehearsal target and evidence use.
- `_bmad-output/implementation-artifacts/v2-real-s3-iam-production-rehearsal-evidence.md`
  records the real S3/IAM release gate.
- `scripts/production-rehearsal.sh` creates the machine-readable report and
  enforces rehearsal invariants.
- `scripts/check-real-s3-iam-gate.sh` rejects weak or local-only evidence.
- `docs/adr/0009-backend-object-key-format.md` defines Backend object key
  shape.

Current tracker state at handoff creation:

```text
gh issue view 429 --repo petabytecl/scrap --json number,title,state,labels,url,updatedAt
```

Result summary: issue `#429` is `OPEN`, labeled `ready-for-human`,
`production-readiness`, `v2`, and `e2e`, updated at
`2026-06-10T02:56:17Z`.

Refresh this issue before using the handoff. Tracker state can change.

## What The Gate Must Prove

A passing real AWS validation must prove all of the following from a real,
non-local AWS S3/IAM environment:

- `make production-rehearsal` completes successfully.
- The Backend is S3, not filesystem.
- `SCRAP_S3_BUCKET` and `SCRAP_S3_REGION` point at a real AWS bucket.
- AWS credentials come from the normal AWS provider chain, profile, or workload
  identity.
- `SCRAP_S3_ENDPOINT` is unset, or points at a real non-local AWS S3 endpoint.
- `SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3` is not used.
- Production security mode is enabled.
- OpenBao Transit is real and reached over TLS by the local rehearsal.
- Test hooks are disabled.
- pprof is disabled.
- An encrypted Document write/read succeeds.
- A sealed Block upload is confirmed by committed S.C.R.A.P. state.
- At least one Backend upload is observed.
- The generated report excludes secret material and other forbidden evidence.

LocalStack, localhost endpoints, filesystem Backend runs, screenshots, raw logs,
and stale reports cannot close issue `#429`.

## Required Runtime Inputs

On the AWS-enabled machine, set only non-secret config values in shell history
where possible. Do not commit credentials or write them into this repo.

Required:

```sh
export SCRAP_S3_BUCKET="<real-aws-validation-bucket>"
export SCRAP_S3_REGION="<aws-region>"
unset SCRAP_S3_ENDPOINT
unset SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3
```

Recommended:

```sh
export SCRAP_PROD_REHEARSAL_CELL_ID="scrap-v2-real-s3-iam-$(date -u +%Y%m%dT%H%M%SZ)"
export AWS_PROFILE="<profile-that-assumes-validation-role>"
export AWS_EC2_METADATA_DISABLED=true
```

`SCRAP_PROD_REHEARSAL_CELL_ID` should be unique for each run. Reusing a Cell
prefix can collide with prior Backend objects. The rehearsal script rejects the
literal unsafe value `production-rehearsal`.

Credentials may also come from environment variables, SSO, an assumed role, or
workload identity. Prefer short-lived credentials. Never copy credential values,
session tokens, private keys, generated TLS material, raw Backend object keys,
Document payloads, validation tokens, or raw logs into issue `#429`.

## AWS Resources Required

The minimum AWS side needs:

- One isolated S3 bucket in the same region as `SCRAP_S3_REGION`.
- One IAM principal for the validation run, preferably an assumed role.
- A policy granting access only to the validation bucket and Cell prefix.
- Optional lifecycle expiration for validation objects after the report has been
  captured and reviewed.
- Optional KMS key only if the bucket policy requires SSE-KMS.

The rehearsal starts OpenBao locally in Docker and generates local TLS material
under `artifacts/production-rehearsal/`. It does not require an AWS OpenBao
deployment, Kubernetes cluster, ECS service, Lambda function, VPC, or S.C.R.A.P.
service deployment.

## Bucket Configuration

Recommended S3 bucket posture:

- Block all public access.
- Use bucket-owner-enforced object ownership.
- Keep ACLs disabled.
- Enable default encryption with SSE-S3 unless the account policy requires
  SSE-KMS.
- Enable versioning if the account baseline requires it.
- Add a lifecycle rule to expire validation objects after 7 to 30 days.
- Restrict IAM access to a dedicated validation prefix.

Do not require the S.C.R.A.P. client to set server-side encryption headers unless
the code has been updated to do so. The current rehearsal contract is about real
S3/IAM and committed Backend upload confirmation, not about proving a new S3
encryption header policy. Bucket default encryption is the low-friction path.

Backend object keys follow:

```text
{cell_id}/shards/{shard_id}/{block_id}.blk
{cell_id}/shards/{shard_id}/{block_id}.idx
```

Use a unique Cell prefix for each run, for example:

```text
scrap-v2-real-s3-iam-20260613T022141Z/
```

Do not publish raw object keys in the tracker evidence.

## Least-Privilege IAM Shape

Grant the runtime validation principal object access only under the chosen Cell
prefix. The S3 Backend uses PUT, HEAD/GET, range reads, and list operations. AWS
does not have a separate `s3:HeadObject` action; `HeadObject` is authorized by
`s3:GetObject`.

Suggested runtime permissions:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ListValidationPrefix",
      "Effect": "Allow",
      "Action": [
        "s3:ListBucket",
        "s3:GetBucketLocation"
      ],
      "Resource": "arn:aws:s3:::<bucket-name>",
      "Condition": {
        "StringLike": {
          "s3:prefix": [
            "<cell-prefix>/shards/*"
          ]
        }
      }
    },
    {
      "Sid": "ReadWriteValidationBlocks",
      "Effect": "Allow",
      "Action": [
        "s3:PutObject",
        "s3:GetObject"
      ],
      "Resource": "arn:aws:s3:::<bucket-name>/<cell-prefix>/shards/*"
    }
  ]
}
```

Do not grant `s3:DeleteObject` to the runtime role unless the cleanup procedure
explicitly needs it. Prefer a separate cleanup role or bucket lifecycle policy.

If the bucket enforces SSE-KMS, add the minimum KMS permissions for the same
principal and key:

```json
{
  "Effect": "Allow",
  "Action": [
    "kms:Decrypt",
    "kms:Encrypt",
    "kms:GenerateDataKey"
  ],
  "Resource": "arn:aws:kms:<region>:<account-id>:key/<key-id>"
}
```

Only add KMS when the bucket policy requires it. The current gate does not
require a dedicated KMS proof.

## CDK Outline

This is a design outline, not code to add to this repo.

```text
Stack: ScrapRealAwsValidationStack

Inputs:
  bucketName?: string
  allowedPrincipalArn: string
  cellPrefix: string
  retentionDays: number = 14
  useKms: boolean = false

Resources:
  validationKey?: kms.Key
    - enabled only when useKms is true
    - rotation enabled if account baseline requires it
    - policy permits the validation role to Encrypt, Decrypt, GenerateDataKey

  validationBucket: s3.Bucket
    - blockPublicAccess: BLOCK_ALL
    - objectOwnership: BUCKET_OWNER_ENFORCED
    - enforceSSL: true
    - encryption: S3_MANAGED or KMS
    - versioned: account-baseline dependent
    - lifecycleRules:
        - prefix: "<cellPrefix>/"
        - expiration: retentionDays

  validationRole or existing principal binding:
    - if creating a role, trust only the operator identity or CI identity that
      will run the rehearsal
    - attach inline policy:
        - s3:ListBucket on bucket with s3:prefix condition
        - s3:GetBucketLocation on bucket
        - s3:PutObject and s3:GetObject on "<cellPrefix>/shards/*"
        - optional KMS actions if useKms is true

Outputs:
  SCRAP_S3_BUCKET = validationBucket.bucketName
  SCRAP_S3_REGION = stack.region
  ValidationRoleArn = role.roleArn or allowedPrincipalArn
  CellPrefixHint = cellPrefix
```

The CDK deployment should happen outside this repo unless a separate story
explicitly adds infrastructure-as-code. If it is later implemented, keep the
principal and bucket prefix configurable so the validation stack can be created
in a disposable AWS account.

## CloudFormation Outline

This is a pseudo-template outline, not a deployable template.

```yaml
AWSTemplateFormatVersion: "2010-09-09"
Description: Documentation-only outline for SCRAP real S3/IAM validation

Parameters:
  BucketName:
    Type: String
  CellPrefix:
    Type: String
  AllowedPrincipalArn:
    Type: String
  RetentionDays:
    Type: Number
    Default: 14
  UseKms:
    Type: String
    AllowedValues: ["true", "false"]
    Default: "false"

Resources:
  ValidationBucket:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Ref BucketName
      PublicAccessBlockConfiguration:
        BlockPublicAcls: true
        BlockPublicPolicy: true
        IgnorePublicAcls: true
        RestrictPublicBuckets: true
      OwnershipControls:
        Rules:
          - ObjectOwnership: BucketOwnerEnforced
      BucketEncryption:
        ServerSideEncryptionConfiguration:
          - ServerSideEncryptionByDefault:
              SSEAlgorithm: AES256
      LifecycleConfiguration:
        Rules:
          - Id: ExpireValidationObjects
            Status: Enabled
            Prefix: !Sub "${CellPrefix}/"
            ExpirationInDays: !Ref RetentionDays

  ValidationPolicy:
    Type: AWS::IAM::ManagedPolicy
    Properties:
      PolicyDocument:
        Version: "2012-10-17"
        Statement:
          - Sid: ListValidationPrefix
            Effect: Allow
            Action:
              - s3:ListBucket
              - s3:GetBucketLocation
            Resource: !Sub "arn:aws:s3:::${BucketName}"
            Condition:
              StringLike:
                s3:prefix:
                  - !Sub "${CellPrefix}/shards/*"
          - Sid: ReadWriteValidationBlocks
            Effect: Allow
            Action:
              - s3:PutObject
              - s3:GetObject
            Resource: !Sub "arn:aws:s3:::${BucketName}/${CellPrefix}/shards/*"
      Roles:
        - "<validation-role-name-if-created-in-this-stack>"

Outputs:
  Bucket:
    Value: !Ref ValidationBucket
  Region:
    Value: !Ref AWS::Region
  CellPrefix:
    Value: !Ref CellPrefix
```

A real template must decide whether to create a role, attach the managed policy
to an existing role, or use a permission boundary from the AWS account baseline.
Keep that account-specific decision outside this handoff.

## Run Procedure On The AWS-Enabled Machine

1. Sync the repo and check out the intended `v2` commit.

   ```sh
   git fetch origin
   git checkout v2
   git pull --ff-only
   git status --short --branch
   ```

2. Confirm issue `#429` is still the open real AWS gate.

   ```sh
   gh issue view 429 --repo petabytecl/scrap --json number,title,state,labels,url,updatedAt
   ```

3. Configure AWS credentials for the validation role.

   ```sh
   aws sts get-caller-identity
   aws s3api get-bucket-location --bucket "$SCRAP_S3_BUCKET"
   ```

4. Export the rehearsal environment.

   ```sh
   export SCRAP_S3_BUCKET="<bucket-name>"
   export SCRAP_S3_REGION="<bucket-region>"
   export SCRAP_PROD_REHEARSAL_CELL_ID="scrap-v2-real-s3-iam-$(date -u +%Y%m%dT%H%M%SZ)"
   unset SCRAP_S3_ENDPOINT
   unset SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3
   ```

5. Run the real S3/IAM rehearsal.

   ```sh
   env GOFLAGS=-buildvcs=false make production-rehearsal
   ```

6. Inspect the generated report locally.

   ```sh
   jq . artifacts/production-rehearsal/report.json
   ```

7. Update the evidence artifact if the release process requires a committed
   evidence row, then run the gate checker.

   ```sh
   scripts/check-real-s3-iam-gate.sh
   ```

   If the checker expects issue `#429` to be closed before final release PASS,
   keep that as a final closure step. Do not close the issue until the sanitized
   report has been linked and reviewed.

8. Link sanitized evidence to issue `#429`.

   Include:

   - commit/ref tested
   - command run
   - `SCRAP_S3_REGION`
   - bucket name or an approved redacted bucket identifier
   - report fields proving the required pass criteria
   - redaction proof summary

   Exclude:

   - AWS access keys or session tokens
   - OpenBao tokens
   - generated private keys or cert material
   - raw Backend object keys
   - Document payloads
   - validation tokens
   - raw logs
   - trace IDs and request IDs if account policy treats them as sensitive

9. Close issue `#429` only after the report is accepted, or record an explicit
   release waiver if leadership chooses to ship without the real AWS proof.

## Report Acceptance Criteria

The report at `artifacts/production-rehearsal/report.json` must prove:

- `status = passed`
- `command = make production-rehearsal`
- `evidence_tier = real-s3-iam`
- `environment = production-rehearsal`
- `backend = s3`
- `security_mode = production`
- `production_readiness_status = ready`
- `openbao_transit = real`
- `test_hooks_enabled = false`
- `pprof_enabled = false`
- `encrypted_write_read_ok = true`
- `plaintext_leak_scan_ok = true`
- `backend_upload_confirmed = true`
- `confirmed_upload_count >= 1`
- `local_overrides.real_s3_iam = true`
- `local_overrides.local_s3_endpoint_allowed = false`
- `local_overrides.filesystem_backend = false`
- `redaction_proof.status = passed`
- `redaction_proof.report_excludes_secret_material = true`
- `redaction_proof.tracker_ready_evidence_excludes_raw_logs = true`

If the worktree is dirty during the run, the report must include
`git_diff_sha256`. Final release evidence is easier to audit from a clean
worktree.

## Failure Modes To Expect

- Missing `SCRAP_S3_BUCKET` or `SCRAP_S3_REGION`: the script fails fast before
  producing real AWS evidence.
- `SCRAP_S3_ENDPOINT` points at localhost, LocalStack, or another test endpoint:
  the evidence cannot close issue `#429`.
- `SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3=true`: valid only for development
  diagnosis, not for the real release gate.
- IAM denies `s3:PutObject`: sealed Block upload cannot complete.
- IAM denies `s3:GetObject`: HeadObject/read verification can fail.
- IAM denies `s3:ListBucket`: Backend listing or upload confirmation evidence can
  fail.
- Bucket policy requires SSE-KMS but the role lacks KMS permissions: S3 PUT or
  GET fails.
- `confirmed_upload_count = 0`: no Backend upload was proven.
- Report contains raw secrets, raw Backend keys, private material, or raw logs:
  the gate checker must reject it.
- Issue `#429` remains open during final release gating: final PASS is blocked
  unless there is an explicit waiver.

## Non-Goals

This handoff does not ask for:

- A production AWS deployment of S.C.R.A.P.
- An AWS deployment of OpenBao.
- Kubernetes manifests.
- A new Backend implementation.
- New CDK or CloudFormation code in this repo.
- A broader release checklist rewrite.

## Suggested Skills For Continuation

- Use `bmad-code-review` before accepting any changes to gate scripts, rehearsal
  behavior, or committed evidence artifacts.
- Use `bmad-dev-story` only if the real AWS work becomes an implementation
  story instead of an operator evidence task.
- Use a security-focused review if IAM policy or KMS decisions become committed
  code.
- Use the git commit workflow only after the evidence or documentation change is
  intentionally staged and checked for secrets.

## Quick Checklist

- [ ] AWS validation bucket exists in the target region.
- [ ] Validation IAM principal can assume the intended role.
- [ ] Runtime policy grants only required bucket/prefix access.
- [ ] `SCRAP_S3_BUCKET` and `SCRAP_S3_REGION` are set.
- [ ] `SCRAP_S3_ENDPOINT` is unset.
- [ ] `SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3` is unset.
- [ ] `SCRAP_PROD_REHEARSAL_CELL_ID` is unique for the run.
- [ ] `env GOFLAGS=-buildvcs=false make production-rehearsal` passes.
- [ ] `artifacts/production-rehearsal/report.json` satisfies the acceptance
  criteria.
- [ ] `scripts/check-real-s3-iam-gate.sh` passes at the appropriate release
  stage.
- [ ] Sanitized evidence is linked to issue `#429`.
- [ ] Issue `#429` is closed or explicitly waived before final v2 release PASS.
