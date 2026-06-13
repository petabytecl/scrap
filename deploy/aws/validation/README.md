# Real AWS Validation Infrastructure

Disposable AWS resources for the S.C.R.A.P. real S3/IAM production rehearsal
(tracker issue `#429`). See `docs/handoff-real-aws-validation-20260613.md` for
the full gate context and `docs/production-rehearsal.md` for the rehearsal
contract.

This stack provisions **only** what the rehearsal needs:

- One isolated S3 bucket (block-public, `BucketOwnerEnforced`, SSE-S3 default,
  TLS-only, lifecycle expiry).
- One least-privilege IAM role the operator assumes via AWS SSO, scoped to the
  validation bucket and Cell prefix.

It is **not** a S.C.R.A.P. production deployment. There is no EC2, ECS, Lambda,
VPC, Kubernetes, or OpenBao here — the rehearsal runs OpenBao locally in Docker
and only reaches real AWS S3.

## Why CloudFormation

A single template deploys with one `aws cloudformation deploy` command and adds
no Node/npm toolchain to this Go repo. If your platform standard is CDK instead,
the same five-resource shape (bucket, bucket policy, role + inline policy) ports
directly; ask and it can be regenerated as a CDK app.

## Prerequisites

- AWS CLI v2 with an SSO profile (`platform`).
- Permission to create S3 buckets and IAM roles in the target account.

```sh
aws sso login --profile platform
aws sts get-caller-identity --profile platform
```

Note the `Arn` from `get-caller-identity`. For SSO it looks like:

```text
arn:aws:sts::<account-id>:assumed-role/AWSReservedSSO_platform_<hash>/<you>
```

The trust pattern uses the IAM role form (session-independent):

```text
arn:aws:iam::<account-id>:role/aws-reserved/sso.amazonaws.com/*/AWSReservedSSO_platform_*
```

## Deploy

```sh
aws cloudformation deploy \
  --profile platform \
  --region <aws-region> \
  --stack-name scrap-real-aws-validation \
  --capabilities CAPABILITY_IAM \
  --template-file deploy/aws/validation/scrap-real-aws-validation.yaml \
  --parameter-overrides \
    TrustedPrincipalArnPattern='arn:aws:iam::<account-id>:role/aws-reserved/sso.amazonaws.com/*/AWSReservedSSO_platform_*' \
    CellPrefix=scrap-v2-real-s3-iam \
    RetentionDays=14
```

Read the outputs:

```sh
aws cloudformation describe-stacks \
  --profile platform --region <aws-region> \
  --stack-name scrap-real-aws-validation \
  --query 'Stacks[0].Outputs' --output table
```

## Wire the assume-role profile

Add a chained profile to `~/.aws/config` so the rehearsal's default credential
chain assumes the least-privilege validation role on top of your SSO session:

```ini
[profile scrap-validation]
role_arn = <ValidationRoleArn from stack outputs>
source_profile = platform
region = <aws-region>
```

Verify the role is assumable and scoped correctly:

```sh
aws sts get-caller-identity --profile scrap-validation
```

## Run the rehearsal

```sh
export AWS_PROFILE=scrap-validation
export AWS_EC2_METADATA_DISABLED=true
export SCRAP_S3_BUCKET="<ScrapS3Bucket output>"
export SCRAP_S3_REGION="<ScrapS3Region output>"
export SCRAP_PROD_REHEARSAL_CELL_ID="scrap-v2-real-s3-iam-$(date -u +%Y%m%dT%H%M%SZ)"
unset SCRAP_S3_ENDPOINT
unset SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3

env GOFLAGS=-buildvcs=false make production-rehearsal
jq . artifacts/production-rehearsal/report.json
```

`SCRAP_PROD_REHEARSAL_CELL_ID` must stay under `CellPrefix` (the IAM policy is
scoped to `<CellPrefix>*/shards/*`) and be unique per run. The rehearsal rejects
the literal value `production-rehearsal`.

> Important when `CreateValidationRole=true`: you MUST export
> `SCRAP_PROD_REHEARSAL_CELL_ID` with a value that starts with `CellPrefix`. If
> left unset, the rehearsal defaults the Cell ID to `production-rehearsal-<run>`,
> which falls outside the role's `<CellPrefix>*` scope and makes every S3
> `PutObject` fail with `AccessDenied`. (When running under a broad identity with
> no dedicated role — `CreateValidationRole=false` — this scoping does not apply,
> but a unique prefixed Cell ID is still recommended.)

## Teardown

The lifecycle rule expires objects after `RetentionDays`. To remove everything
sooner, empty the bucket first (the runtime role intentionally lacks
`s3:DeleteObject`, so use an admin identity), then delete the stack:

```sh
aws s3 rm "s3://<bucket>" --recursive --profile platform
aws cloudformation delete-stack \
  --profile platform --region <aws-region> \
  --stack-name scrap-real-aws-validation
```

## SSE-KMS (only if your account baseline requires it)

This template uses SSE-S3 (`AES256`), the low-friction path the gate expects. If
the account mandates SSE-KMS, switch `BucketEncryption` to `aws:kms` with a key,
and add `kms:Encrypt`, `kms:Decrypt`, `kms:GenerateDataKey` for that key to the
`ValidationRole` inline policy. The current gate does not require a KMS proof.
