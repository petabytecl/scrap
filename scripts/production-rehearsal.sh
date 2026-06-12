#!/usr/bin/env bash
set -euo pipefail

command_name=${1:-run}

work_dir=${SCRAP_PROD_REHEARSAL_DIR:-artifacts/production-rehearsal}
backend=${SCRAP_PROD_REHEARSAL_BACKEND:-s3}
run_id=${SCRAP_PROD_REHEARSAL_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}
cell_id=${SCRAP_PROD_REHEARSAL_CELL_ID:-production-rehearsal-${run_id}}
member_id=${SCRAP_PROD_REHEARSAL_MEMBER_ID:-member-a}
server_name=${SCRAP_PROD_REHEARSAL_SERVER_NAME:-scrap.local}
openbao_image=${SCRAP_PROD_REHEARSAL_OPENBAO_IMAGE:-openbao/openbao:2.5.4}
openbao_container=${SCRAP_PROD_REHEARSAL_OPENBAO_CONTAINER:-scrap-openbao-production-rehearsal}
openbao_port=${SCRAP_PROD_REHEARSAL_OPENBAO_PORT:-18200}
client_port=${SCRAP_PROD_REHEARSAL_CLIENT_PORT:-19090}
peer_port=${SCRAP_PROD_REHEARSAL_PEER_PORT:-19091}
admin_port=${SCRAP_PROD_REHEARSAL_ADMIN_PORT:-19100}
block_seal_size=${SCRAP_PROD_REHEARSAL_BLOCK_SEAL_SIZE:-128}
keep_running=${SCRAP_PROD_REHEARSAL_KEEP_RUNNING:-false}

scrapd_bin=${SCRAPD_BIN:-./scrapd}
scrapctl_bin=${SCRAPCTL_BIN:-./scrapctl}
docker_bin=${DOCKER:-docker}
grpcurl_bin=${GRPCURL:-grpcurl}
curl_bin=${CURL:-curl}
jq_bin=${JQ:-jq}
openssl_bin=${OPENSSL:-openssl}
base64_bin=${BASE64:-base64}
cmp_bin=${CMP:-cmp}

runtime_dir="$work_dir/runtime"
tls_dir="$runtime_dir/tls"
policy_dir="$runtime_dir/policies"
openbao_dir="$runtime_dir/openbao"
data_dir="$runtime_dir/scrap-data"
log_dir="$runtime_dir/logs"
placement_file="$runtime_dir/shard-placement.json"
report_file="$work_dir/report.json"
scrapd_log="$log_dir/scrapd.log"
openbao_log="$log_dir/openbao.log"
scrapd_pid_file="$runtime_dir/scrapd.pid"
bootstrap_evidence_file="$runtime_dir/openbao-bootstrap-evidence.json"
drill_dir="$runtime_dir/fail-closed-drills"
redaction_scan_file="$runtime_dir/redaction-scan.json"
active_drill_pid=""

ca_key="$tls_dir/ca.key"
ca_cert="$tls_dir/ca.pem"
scrap_key="$tls_dir/scrap.key"
scrap_csr="$tls_dir/scrap.csr"
scrap_cert="$tls_dir/scrap.pem"
scrap_ext="$tls_dir/scrap.ext"
openbao_key="$tls_dir/openbao.key"
openbao_csr="$tls_dir/openbao.csr"
openbao_cert="$tls_dir/openbao.pem"
openbao_ext="$tls_dir/openbao.ext"
combined_ca="$tls_dir/combined-ca.pem"
role_policy="$policy_dir/roles.json"
peer_policy="$policy_dir/peer-identity.json"
audit_policy="$policy_dir/audit.json"
rate_policy="$policy_dir/rate-limit.json"
openbao_config="$openbao_dir/config/openbao.hcl"
openbao_addr="https://127.0.0.1:${openbao_port}"
transit_key="scrap-documents"
transit_mount="transit"

log() {
	printf '[production-rehearsal] %s\n' "$*"
}

die() {
	printf '[production-rehearsal] %s\n' "$*" >&2
	exit 1
}

require_command() {
	local path=$1
	local label=$2
	if ! command -v "$path" >/dev/null 2>&1; then
		die "missing required command for $label: $path"
	fi
}

require_file() {
	local path=$1
	local label=$2
	[ -f "$path" ] || die "missing $label: $path"
}

stop_scrapd() {
	if [ -s "$scrapd_pid_file" ]; then
		local pid
		pid=$(cat "$scrapd_pid_file")
		if kill -0 "$pid" >/dev/null 2>&1; then
			kill "$pid" >/dev/null 2>&1 || true
			wait "$pid" >/dev/null 2>&1 || true
		fi
		rm -f "$scrapd_pid_file"
	fi
}

stop_openbao() {
	"$docker_bin" rm -f "$openbao_container" >/dev/null 2>&1 || true
}

cleanup() {
	if [ "$keep_running" = "true" ]; then
		log "leaving rehearsal processes running"
		log "scrapd pid file: $scrapd_pid_file"
		log "OpenBao container: $openbao_container"
		return
	fi
	if [ -n "$active_drill_pid" ]; then
		stop_pid "$active_drill_pid"
		active_drill_pid=""
	fi
	stop_scrapd
	stop_openbao
}

prepare_workspace() {
	rm -rf "$runtime_dir"
	mkdir -p "$tls_dir" "$policy_dir" "$openbao_dir/config" "$openbao_dir/data" "$data_dir" "$log_dir" "$drill_dir"
	chmod 700 "$work_dir" "$runtime_dir" "$tls_dir" "$policy_dir" "$openbao_dir" "$data_dir" "$log_dir" "$drill_dir"
}

principal_id() {
	printf 'spiffe://scrap/cell/%s/member/%s/%s' "$cell_id" "$(hostname)" "$member_id"
}

write_tls_material() {
	local principal
	principal=$(principal_id)

	"$openssl_bin" ecparam -name prime256v1 -genkey -noout -out "$ca_key"
	"$openssl_bin" req -x509 -new -key "$ca_key" -sha256 -days 3 -out "$ca_cert" \
		-subj "/CN=scrap production rehearsal CA" \
		-addext "basicConstraints=critical,CA:TRUE" \
		-addext "keyUsage=critical,keyCertSign,cRLSign"

	"$openssl_bin" ecparam -name prime256v1 -genkey -noout -out "$scrap_key"
	"$openssl_bin" req -new -key "$scrap_key" -out "$scrap_csr" -subj "/CN=scrap production rehearsal"
	cat > "$scrap_ext" <<EOF
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth,clientAuth
subjectAltName=DNS:${server_name},IP:127.0.0.1,URI:${principal}
EOF
	"$openssl_bin" x509 -req -in "$scrap_csr" -CA "$ca_cert" -CAkey "$ca_key" -CAcreateserial \
		-out "$scrap_cert" -days 3 -sha256 -extfile "$scrap_ext"

	"$openssl_bin" ecparam -name prime256v1 -genkey -noout -out "$openbao_key"
	"$openssl_bin" req -new -key "$openbao_key" -out "$openbao_csr" -subj "/CN=openbao production rehearsal"
	cat > "$openbao_ext" <<EOF
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:localhost,IP:127.0.0.1
EOF
	"$openssl_bin" x509 -req -in "$openbao_csr" -CA "$ca_cert" -CAkey "$ca_key" -CAcreateserial \
		-out "$openbao_cert" -days 3 -sha256 -extfile "$openbao_ext"

	chmod 600 "$ca_key" "$scrap_key"
	chmod 644 "$ca_cert" "$scrap_cert" "$openbao_cert" "$openbao_key"
	write_combined_ca_bundle
}

write_combined_ca_bundle() {
	local system_bundle=""
	for candidate in \
		/etc/ssl/certs/ca-certificates.crt \
		/etc/pki/tls/certs/ca-bundle.crt \
		/etc/ssl/ca-bundle.pem
	do
		if [ -s "$candidate" ]; then
			system_bundle=$candidate
			break
		fi
	done
	if [ -n "$system_bundle" ]; then
		cat "$system_bundle" "$ca_cert" > "$combined_ca"
	else
		cp "$ca_cert" "$combined_ca"
	fi
	chmod 644 "$combined_ca"
}

write_policies() {
	local principal
	principal=$(principal_id)
	cat > "$role_policy" <<EOF
{
  "roles": [
    "document_writer",
    "document_reader",
    "peer_member",
    "admin_reader",
    "admin_operator",
    "admin_break_glass"
  ],
  "principals": [
    {
      "id": "${principal}",
      "roles": [
        "document_writer",
        "document_reader",
        "peer_member",
        "admin_reader",
        "admin_operator",
        "admin_break_glass"
      ]
    }
  ]
}
EOF
	cat > "$peer_policy" <<EOF
{
  "cell_id": "${cell_id}",
  "member_hostname": "$(hostname)",
  "member_id": "${member_id}"
}
EOF
	cat > "$audit_policy" <<EOF
{
  "sink": "stderr",
  "failure_mode": "fail_closed",
  "max_event_bytes": 1024
}
EOF
	cat > "$rate_policy" <<EOF
{
  "surfaces": [
    {"surface": "public", "limit": 1000, "window": "1m"},
    {"surface": "peer", "limit": 1000, "window": "1m"},
    {"surface": "admin", "limit": 1000, "window": "1m"}
  ]
}
EOF
	chmod 600 "$role_policy" "$peer_policy" "$audit_policy" "$rate_policy"
}

write_shard_placement() {
	cat > "$placement_file" <<EOF
{
  "slot_count": 1024,
  "shards": [7, 9],
  "local_shards": [7, 9],
  "ranges": [
    {
      "shard_id": 7,
      "start_slot": 0,
      "end_slot": 511
    },
    {
      "shard_id": 9,
      "start_slot": 512,
      "end_slot": 1023
    }
  ]
}
EOF
	chmod 600 "$placement_file"
}

write_openbao_config() {
	cat > "$openbao_config" <<EOF
ui = false
disable_mlock = true
api_addr = "${openbao_addr}"

listener "tcp" {
  address = "0.0.0.0:8200"
  tls_cert_file = "/openbao/tls/openbao.pem"
  tls_key_file = "/openbao/tls/openbao.key"
}

storage "file" {
  path = "/openbao/data"
}
EOF
}

validate_backend_config() {
	case "$backend" in
	fs)
		return
		;;
	s3)
		[ -n "${SCRAP_S3_BUCKET:-}" ] || die "SCRAP_S3_BUCKET is required for production-rehearsal"
		[ -n "${SCRAP_S3_REGION:-}" ] || die "SCRAP_S3_REGION is required for production-rehearsal"
		if [ "${SCRAP_PROD_REHEARSAL_CELL_ID:-}" = "production-rehearsal" ]; then
			die "SCRAP_PROD_REHEARSAL_CELL_ID=production-rehearsal reuses Backend object keys; choose a unique Cell ID or unset it for the per-run default"
		fi
		case "${SCRAP_S3_ENDPOINT:-}" in
		*localhost*|*127.0.0.1*|*localstack*)
			if [ "${SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3:-false}" != "true" ]; then
				die "SCRAP_S3_ENDPOINT points at a local/test endpoint; unset it or set SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3=true explicitly"
			fi
			;;
		esac
		;;
	*)
		die "SCRAP_PROD_REHEARSAL_BACKEND must be fs or s3, got: $backend"
		;;
	esac
}

validate_positive_integer() {
	local label=$1
	local value=$2
	case "$value" in
	''|*[!0-9]*)
		die "${label} must be a positive integer, got: $value"
		;;
	esac
	if [ "$((10#$value))" -le 0 ]; then
		die "${label} must be greater than zero, got: $value"
	fi
}

validate_port() {
	local label=$1
	local value=$2
	validate_positive_integer "$label" "$value"
	if [ "$((10#$value))" -gt 65535 ]; then
		die "${label} must be <= 65535, got: $value"
	fi
}

validate_rehearsal_config() {
	validate_positive_integer "SCRAP_PROD_REHEARSAL_BLOCK_SEAL_SIZE" "$block_seal_size"
	validate_port "SCRAP_PROD_REHEARSAL_OPENBAO_PORT" "$openbao_port"
	validate_port "SCRAP_PROD_REHEARSAL_CLIENT_PORT" "$client_port"
	validate_port "SCRAP_PROD_REHEARSAL_PEER_PORT" "$peer_port"
	validate_port "SCRAP_PROD_REHEARSAL_ADMIN_PORT" "$admin_port"
	validate_backend_config
}

start_openbao() {
	stop_openbao
	log "starting OpenBao $openbao_image with TLS on 127.0.0.1:$openbao_port"
	"$docker_bin" run -d --name "$openbao_container" \
		--user "$(id -u):$(id -g)" \
		-p "127.0.0.1:${openbao_port}:8200" \
		-v "$(pwd)/$openbao_dir/config:/openbao/config:ro" \
		-v "$(pwd)/$tls_dir:/openbao/tls:ro" \
		-v "$(pwd)/$openbao_dir/data:/openbao/data" \
		"$openbao_image" server -config=/openbao/config/openbao.hcl >"$openbao_log"
	wait_openbao_tls
	initialize_openbao
}

wait_openbao_tls() {
	local deadline
	deadline=$(($(date +%s) + 60))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if "$curl_bin" --silent --show-error --cacert "$ca_cert" \
			--output /dev/null "$openbao_addr/v1/sys/health" >/dev/null 2>&1; then
			return
		fi
		sleep 1
	done
	"$docker_bin" logs "$openbao_container" >>"$openbao_log" 2>&1 || true
	die "OpenBao TLS endpoint did not become reachable; see $openbao_log"
}

initialize_openbao() {
	local root_token
	log "bootstrapping OpenBao Transit with scrapctl"
	NO_PROXY="127.0.0.1,localhost" \
		no_proxy="127.0.0.1,localhost" \
		SSL_CERT_FILE="$combined_ca" \
		OPENBAO_TOKEN="" \
		"$scrapctl_bin" openbao bootstrap \
		--address="$openbao_addr" \
		--token-env=OPENBAO_TOKEN \
		--mount-path="$transit_mount" \
		--key-name="$transit_key" \
		--key-type=aes256-gcm96 \
		--key-derived=true \
		--environment=production-rehearsal \
		--init \
		--init-secrets-path="$openbao_dir/init.json" \
		--evidence-path="$bootstrap_evidence_file" >/dev/null
	root_token=$("$jq_bin" -r '.root_token' "$openbao_dir/init.json")
	[ -n "$root_token" ] && [ "$root_token" != "null" ] || die "OpenBao init did not return a root token"
	printf '%s' "$root_token" > "$openbao_dir/root-token"
	chmod 600 "$openbao_dir/init.json" "$openbao_dir/root-token"
}

create_auth_denied_token() {
	local root_token policy_name policy_text policy_payload token_payload token_json token
	root_token=$(cat "$openbao_dir/root-token")
	policy_name="scrap-production-rehearsal-auth-denied"
	policy_payload="$runtime_dir/auth-denied-policy.json"
	token_payload="$runtime_dir/auth-denied-token-request.json"
	policy_text=$(cat <<EOF
path "${transit_mount}/*" {
  capabilities = []
}
EOF
)
	"$jq_bin" -n --arg policy "$policy_text" '{policy: $policy}' > "$policy_payload"
	chmod 600 "$policy_payload"
	"$curl_bin" --silent --show-error --fail-with-body --cacert "$ca_cert" \
		--header "X-Vault-Token: ${root_token}" \
		--request PUT \
		--data @"$policy_payload" \
		"$openbao_addr/v1/sys/policies/acl/${policy_name}" >/dev/null

	"$jq_bin" -n --arg policy "$policy_name" '{
		policies: [$policy],
		no_default_policy: true,
		ttl: "5m",
		display_name: "production-rehearsal-auth-denied"
	}' > "$token_payload"
	chmod 600 "$token_payload"
	token_json=$("$curl_bin" --silent --show-error --fail-with-body --cacert "$ca_cert" \
		--header "X-Vault-Token: ${root_token}" \
		--request POST \
		--data @"$token_payload" \
		"$openbao_addr/v1/auth/token/create")
	token=$(printf '%s\n' "$token_json" | "$jq_bin" -r '.auth.client_token')
	[ -n "$token" ] && [ "$token" != "null" ] || die "OpenBao auth-denied drill token creation did not return a token"
	printf '%s' "$token"
}

scrapd_env() {
	local root_token
	root_token=$(cat "$openbao_dir/root-token")
	env \
		NO_PROXY="127.0.0.1,localhost" \
		no_proxy="127.0.0.1,localhost" \
		SSL_CERT_FILE="$combined_ca" \
		SCRAP_SECURITY_MODE="production" \
		SCRAP_CELL_ID="$cell_id" \
		SCRAP_ENVIRONMENT="production-rehearsal" \
		SCRAP_MEMBER_ID="$member_id" \
		SCRAP_BACKEND_TYPE="$backend" \
		SCRAP_SHARD_PLACEMENT_FILE="$placement_file" \
		SCRAP_TLS_PUBLIC_CERT="$scrap_cert" \
		SCRAP_TLS_PUBLIC_KEY="$scrap_key" \
		SCRAP_TLS_PUBLIC_CLIENT_CA="$ca_cert" \
		SCRAP_TLS_PUBLIC_SERVER_NAME="$server_name" \
		SCRAP_TLS_PEER_CERT="$scrap_cert" \
		SCRAP_TLS_PEER_KEY="$scrap_key" \
		SCRAP_TLS_PEER_CLIENT_CA="$ca_cert" \
		SCRAP_TLS_PEER_SERVER_NAME="$server_name" \
		SCRAP_TLS_ADMIN_CERT="$scrap_cert" \
		SCRAP_TLS_ADMIN_KEY="$scrap_key" \
		SCRAP_TLS_ADMIN_CLIENT_CA="$ca_cert" \
		SCRAP_TLS_ADMIN_SERVER_NAME="$server_name" \
		SCRAP_TLS_SCRAPCTL_CERT="$scrap_cert" \
		SCRAP_TLS_SCRAPCTL_KEY="$scrap_key" \
		SCRAP_TLS_SCRAPCTL_CLIENT_CA="$ca_cert" \
		SCRAP_TLS_SCRAPCTL_SERVER_NAME="$server_name" \
		SCRAP_ROLE_POLICY_FILE="$role_policy" \
		SCRAP_PEER_IDENTITY_POLICY_FILE="$peer_policy" \
		SCRAP_TRANSIT_ADDR="${SCRAP_TRANSIT_ADDR_OVERRIDE:-$openbao_addr}" \
		SCRAP_TRANSIT_MOUNT="$transit_mount" \
		SCRAP_TRANSIT_KEY="${SCRAP_TRANSIT_KEY_OVERRIDE:-$transit_key}" \
		SCRAP_TRANSIT_TOKEN_ENV="OPENBAO_TOKEN" \
		OPENBAO_TOKEN="${OPENBAO_TOKEN_OVERRIDE:-$root_token}" \
		SCRAP_AUDIT_POLICY_FILE="$audit_policy" \
		SCRAP_RATE_LIMIT_POLICY_FILE="$rate_policy" \
		SCRAP_PPROF_ENABLED="false" \
		SCRAP_TEST_HOOKS="false" \
		SCRAP_UPLOAD_ENABLED="true" \
		AWS_EC2_METADATA_DISABLED="${AWS_EC2_METADATA_DISABLED:-true}" \
		"$@"
}

start_scrapd() {
	log "starting scrapd in production security mode"
	scrapd_env "$scrapd_bin" \
		--data-dir="$data_dir" \
		--listen-addr="127.0.0.1:${client_port}" \
		--peer-addr="127.0.0.1:${peer_port}" \
		--admin-addr="127.0.0.1:${admin_port}" \
		--block-seal-size="$block_seal_size" \
		--peers="1=127.0.0.1:${peer_port}" >"$scrapd_log" 2>&1 &
	printf '%s\n' "$!" > "$scrapd_pid_file"
	wait_scrapd_health
	wait_scrapd_leader
}

wait_scrapd_health() {
	local deadline
	deadline=$(($(date +%s) + 60))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if "$scrapd_bin" healthcheck \
			--address="127.0.0.1:${client_port}" \
			--tls-cert="$scrap_cert" \
			--tls-key="$scrap_key" \
			--tls-ca="$ca_cert" \
			--tls-server-name="$server_name" \
			--timeout=2s >/dev/null 2>&1; then
			return
		fi
		if [ -s "$scrapd_pid_file" ] && ! kill -0 "$(cat "$scrapd_pid_file")" >/dev/null 2>&1; then
			die "scrapd exited before healthcheck passed; see $scrapd_log"
		fi
		sleep 1
	done
	die "scrapd healthcheck did not pass; see $scrapd_log"
}

wait_scrapd_leader() {
	local deadline leader_file
	leader_file="$runtime_dir/leader.json"
	deadline=$(($(date +%s) + 60))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if scrapd_env "$scrapctl_bin" leader \
			--metrics-url="https://127.0.0.1:${admin_port}/metrics" \
			--output=json > "$leader_file" 2>/dev/null &&
			"$jq_bin" -e '.is_leader == true and .leader_id == 1' "$leader_file" >/dev/null; then
			return
		fi
		if [ -s "$scrapd_pid_file" ] && ! kill -0 "$(cat "$scrapd_pid_file")" >/dev/null 2>&1; then
			die "scrapd exited before Raft leadership; see $scrapd_log"
		fi
		sleep 1
	done
	die "scrapd did not become Raft leader; see $leader_file and $scrapd_log"
}

verify_admin_health() {
	local health_file="$runtime_dir/health.json"
	scrapd_env "$scrapctl_bin" status \
		--admin-url="https://127.0.0.1:${admin_port}" \
		--output=json > "$health_file"
	"$jq_bin" -e '.security_mode == "production" and .production_readiness_status == "ready"' "$health_file" >/dev/null ||
		die "production health did not report ready; see $health_file"
}

grpcurl_common_args() {
	printf '%s\n' \
		-cacert "$ca_cert" \
		-cert "$scrap_cert" \
		-key "$scrap_key" \
		-authority "$server_name" \
		-import-path proto \
		-proto proto/scrap/v1/document.proto
}

rehearsal_payload() {
	local tx_id=$1
	local prefix filler_size target_size
	prefix="production rehearsal encrypted document payload ${tx_id} "
	target_size=$((10#$block_seal_size + 512))
	filler_size=$((target_size - ${#prefix}))
	printf '%s' "$prefix"
	if [ "$filler_size" -gt 0 ]; then
		printf '%*s' "$filler_size" '' | tr ' ' 'x'
	fi
}

write_document() {
	local tx_id=$1
	local doc_name=$2
	local idempotency_key=$3
	local payload=$4
	local write_req=$5
	local write_out=$6
	local payload_b64
	payload_b64=$(printf '%s' "$payload" | "$base64_bin" -w0)

	cat > "$write_req" <<EOF
{"init":{"transactionId":"${tx_id}","documentName":"${doc_name}","contentType":"text/plain","idempotencyKey":"${idempotency_key}"}}
{"chunkData":"${payload_b64}"}
EOF
	"$grpcurl_bin" $(grpcurl_common_args) \
		-d @ "127.0.0.1:${client_port}" scrap.v1.DocumentService/WriteDocument \
		< "$write_req" > "$write_out"
	"$jq_bin" -e '.size > 0 and .sha256Checksum != ""' "$write_out" >/dev/null ||
		die "WriteDocument response is incomplete; see $write_out"
}

write_upload_trigger_document() {
	local tx_id=$1
	local trigger_doc trigger_payload trigger_req trigger_out
	trigger_doc="seal-trigger.txt"
	trigger_payload="production rehearsal upload trigger for ${tx_id}"
	trigger_req="$runtime_dir/seal-trigger-write-request.json"
	trigger_out="$runtime_dir/seal-trigger-write-response.json"
	write_document "$tx_id" "$trigger_doc" "${tx_id}-seal-trigger" "$trigger_payload" "$trigger_req" "$trigger_out"
	if grep -R -a -F "$trigger_payload" "$data_dir" >/dev/null 2>&1; then
		die "plaintext trigger payload was found under $data_dir"
	fi
}

decode_read_payload() {
	local read_out=$1
	local payload_out=$2
	local chunk
	: > "$payload_out"
	while IFS= read -r chunk; do
		[ -n "$chunk" ] || continue
		printf '%s' "$chunk" | "$base64_bin" -d >> "$payload_out" ||
			die "ReadDocument returned invalid base64 payload; see $read_out"
	done < <("$jq_bin" -r 'select(.chunkData != null) | .chunkData' "$read_out")
}

write_read_document() {
	local tx_id doc_name payload write_req write_out head_out read_out expected_payload read_payload
	tx_id="prod-rehearsal-$(date -u +%Y%m%dT%H%M%SZ)"
	doc_name="readiness.txt"
	payload=$(rehearsal_payload "$tx_id")
	write_req="$runtime_dir/write-request.json"
	write_out="$runtime_dir/write-response.json"
	head_out="$runtime_dir/head-response.json"
	read_out="$runtime_dir/read-response.json"
	expected_payload="$runtime_dir/read-expected-payload.txt"
	read_payload="$runtime_dir/read-payload.txt"

	write_document "$tx_id" "$doc_name" "$tx_id" "$payload" "$write_req" "$write_out"

	"$grpcurl_bin" $(grpcurl_common_args) \
		-d "{\"transactionId\":\"${tx_id}\",\"documentName\":\"${doc_name}\"}" \
		"127.0.0.1:${client_port}" scrap.v1.DocumentService/HeadDocument > "$head_out"
	"$jq_bin" -e '.size > 0 and .sha256Checksum != ""' "$head_out" >/dev/null ||
		die "HeadDocument response is incomplete; see $head_out"

	"$grpcurl_bin" $(grpcurl_common_args) \
		-d "{\"transactionId\":\"${tx_id}\",\"documentName\":\"${doc_name}\"}" \
		"127.0.0.1:${client_port}" scrap.v1.DocumentService/ReadDocument > "$read_out"
	printf '%s' "$payload" > "$expected_payload"
	decode_read_payload "$read_out" "$read_payload"
	"$cmp_bin" -s "$expected_payload" "$read_payload" ||
		die "ReadDocument did not return the written payload; see $read_out"

	if grep -R -a -F "$payload" "$data_dir" >/dev/null 2>&1; then
		die "plaintext payload was found under $data_dir"
	fi
	write_upload_trigger_document "$tx_id"
	wait_backend_upload_confirmed
	wait_uploads_drained
}

confirmed_upload_count() {
	local count marker
	count=0
	while IFS= read -r marker; do
		if "$jq_bin" -e \
			'.block_id >= 0 and .confirmed_at_us >= 0 and .block_object.key != "" and .index_object.key != "" and .block_object.validation_token != "" and .index_object.validation_token != ""' \
			"$marker" >/dev/null; then
			count=$((count + 1))
		fi
	done < <(find "$data_dir" -type f -name '*.confirmed-upload.json' -print)
	printf '%s\n' "$count"
}

wait_backend_upload_confirmed() {
	local deadline count proof_file
	proof_file="$runtime_dir/upload-confirmation.json"
	deadline=$(($(date +%s) + 60))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		count=$(confirmed_upload_count)
		if [ "$count" -gt 0 ]; then
			cat > "$proof_file" <<EOF
{
  "backend_upload_confirmed": true,
  "confirmed_upload_count": ${count}
}
EOF
			return
		fi
		if [ -s "$scrapd_pid_file" ] && ! kill -0 "$(cat "$scrapd_pid_file")" >/dev/null 2>&1; then
			die "scrapd exited before a Backend upload was confirmed; see $scrapd_log"
		fi
		sleep 1
	done
	die "no committed Backend upload confirmation was observed; expected at least one *.confirmed-upload.json marker under $data_dir"
}

wait_uploads_drained() {
	local deadline health_file
	health_file="$runtime_dir/health-upload.json"
	deadline=$(($(date +%s) + 60))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		scrapd_env "$scrapctl_bin" status \
			--admin-url="https://127.0.0.1:${admin_port}" \
			--output=json > "$health_file"
		if "$jq_bin" -e '.upload_pending_blocks == 0' "$health_file" >/dev/null; then
			return
		fi
		sleep 1
	done
	die "upload pending blocks did not drain; see $health_file"
}

stop_pid() {
	local pid=$1
	if kill -0 "$pid" >/dev/null 2>&1; then
		kill "$pid" >/dev/null 2>&1 || true
		wait "$pid" >/dev/null 2>&1 || true
	fi
}

wait_drill_scrapd_health() {
	local pid=$1
	local log_file=$2
	local deadline
	deadline=$(($(date +%s) + 60))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if "$scrapd_bin" healthcheck \
			--address="127.0.0.1:${client_port}" \
			--tls-cert="$scrap_cert" \
			--tls-key="$scrap_key" \
			--tls-ca="$ca_cert" \
			--tls-server-name="$server_name" \
			--timeout=2s >/dev/null 2>&1; then
			return
		fi
		if ! kill -0 "$pid" >/dev/null 2>&1; then
			die "drill scrapd exited before healthcheck passed; see $log_file"
		fi
		sleep 1
	done
	die "drill scrapd healthcheck did not pass; see $log_file"
}

wait_drill_scrapd_leader() {
	local pid=$1
	local log_file=$2
	local leader_file=$3
	local deadline
	deadline=$(($(date +%s) + 60))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if scrapd_env "$scrapctl_bin" leader \
			--metrics-url="https://127.0.0.1:${admin_port}/metrics" \
			--output=json > "$leader_file" 2>/dev/null &&
			"$jq_bin" -e '.is_leader == true and .leader_id == 1' "$leader_file" >/dev/null; then
			return
		fi
		if ! kill -0 "$pid" >/dev/null 2>&1; then
			die "drill scrapd exited before Raft leadership; see $leader_file and $log_file"
		fi
		sleep 1
	done
	die "drill scrapd did not become Raft leader; see $leader_file and $log_file"
}

assert_expected_drill_error() {
	local name=$1
	local expected_code=$2
	local expected_marker=$3
	local write_err=$4
	if ! grep -Eq "Code:[[:space:]]+${expected_code}([[:space:]]|$)" "$write_err"; then
		die "fail-closed drill ${name} returned an unexpected gRPC code; expected ${expected_code}, see $write_err"
	fi
	if [ -n "$expected_marker" ] && ! grep -F "$expected_marker" "$write_err" >/dev/null; then
		die "fail-closed drill ${name} did not include the expected bounded error marker; see $write_err"
	fi
}

redaction_forbidden_pattern() {
	printf '%s' '(AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|[hs]v[bs]\.[A-Za-z0-9_-]{20,}|aws_access_[k]ey_id|aws_[s]ecret_access_[k]ey|root_token|client_token|keys_base64|unseal_keys|X-Vault-Token|Authorization:[[:space:]]*[Bb]earer)'
}

assert_artifact_redaction() {
	local label=$1
	shift
	local findings_file="$runtime_dir/${label}-redaction-findings.txt"
	rm -f "$findings_file"
	if grep -R -a -n -E "$(redaction_forbidden_pattern)" "$@" > "$findings_file"; then
		die "redaction scan found forbidden material in ${label}; see $findings_file"
	fi
	rm -f "$findings_file"
}

write_redaction_scan() {
	local include_report=${1:-false}
	local scan_files files_json path
	scan_files=("$bootstrap_evidence_file" "$scrapd_log" "$openbao_log")
	if [ "$include_report" = "true" ]; then
		scan_files+=("$report_file")
	fi
	while IFS= read -r path; do
		scan_files+=("$path")
	done < <(find "$drill_dir" -type f \( -name result.json -o -name write-error.txt -o -name write-response.json -o -name scrapd.log \) -print)

	assert_artifact_redaction "rehearsal-artifacts" "${scan_files[@]}"
	files_json=$(printf '%s\n' "${scan_files[@]}" | "$jq_bin" -R . | "$jq_bin" -s .)
	"$jq_bin" -n \
		--arg status "passed" \
		--argjson files "$files_json" \
		'{
			status: $status,
			forbidden_material_found: false,
			scanned_artifacts: $files
		}' > "$redaction_scan_file"
	chmod 600 "$redaction_scan_file"
}

write_document_expect_failure() {
	local name=$1
	local tx_id=$2
	local doc_name=$3
	local payload=$4
	local write_req=$5
	local write_out=$6
	local write_err=$7
	local expected_code=$8
	local expected_marker=$9
	local payload_b64
	payload_b64=$(printf '%s' "$payload" | "$base64_bin" -w0)
	cat > "$write_req" <<EOF
{"init":{"transactionId":"${tx_id}","documentName":"${doc_name}","contentType":"text/plain","idempotencyKey":"${tx_id}"}}
{"chunkData":"${payload_b64}"}
EOF
	if "$grpcurl_bin" $(grpcurl_common_args) \
		-d @ "127.0.0.1:${client_port}" scrap.v1.DocumentService/WriteDocument \
		< "$write_req" > "$write_out" 2> "$write_err"; then
		die "fail-closed drill ${tx_id} unexpectedly wrote a Document; see $write_out"
	fi
	assert_expected_drill_error "$name" "$expected_code" "$expected_marker" "$write_err"
}

run_fail_closed_write_drill() {
	local name=$1
	local transit_addr=$2
	local token=$3
	local key_name=$4
	local expected=$5
	local expected_code=$6
	local expected_marker=$7
	local drill_root drill_data drill_log drill_pid leader_file write_req write_out write_err result_file tx_id payload
	drill_root="$drill_dir/$name"
	drill_data="$drill_root/scrap-data"
	drill_log="$drill_root/scrapd.log"
	leader_file="$drill_root/leader.json"
	write_req="$drill_root/write-request.json"
	write_out="$drill_root/write-response.json"
	write_err="$drill_root/write-error.txt"
	result_file="$drill_root/result.json"
	tx_id="prod-rehearsal-${name}-$(date -u +%Y%m%dT%H%M%SZ)"
	payload="production rehearsal fail closed drill ${name} payload"
	mkdir -p "$drill_data"
	chmod 700 "$drill_root" "$drill_data"

	SCRAP_TRANSIT_ADDR_OVERRIDE="$transit_addr" \
		SCRAP_TRANSIT_KEY_OVERRIDE="$key_name" \
		OPENBAO_TOKEN_OVERRIDE="$token" \
		scrapd_env "$scrapd_bin" \
		--data-dir="$drill_data" \
		--listen-addr="127.0.0.1:${client_port}" \
		--peer-addr="127.0.0.1:${peer_port}" \
		--admin-addr="127.0.0.1:${admin_port}" \
		--block-seal-size="$block_seal_size" \
		--peers="1=127.0.0.1:${peer_port}" >"$drill_log" 2>&1 &
	drill_pid=$!
	active_drill_pid=$drill_pid
	wait_drill_scrapd_health "$drill_pid" "$drill_log"
	wait_drill_scrapd_leader "$drill_pid" "$drill_log" "$leader_file"
	write_document_expect_failure "$name" "$tx_id" "fail-closed-${name}.txt" "$payload" "$write_req" "$write_out" "$write_err" "$expected_code" "$expected_marker"
	stop_pid "$drill_pid"
	active_drill_pid=""
	if grep -R -a -F "$payload" "$drill_data" >/dev/null 2>&1; then
		die "fail-closed drill ${name} left plaintext under $drill_data"
	fi
	assert_artifact_redaction "drill-${name}" "$write_out" "$write_err" "$drill_log"
	"$jq_bin" -n \
		--arg name "$name" \
		--arg expected "$expected" \
		--arg actual "write failed closed without plaintext fallback" \
		--arg artifact "$result_file" \
		--arg log "$drill_log" \
		--arg stdout "$write_out" \
		--arg stderr "$write_err" \
		'{
			name: $name,
			status: "pass",
			expected_result: $expected,
			actual_result: $actual,
			artifact_path: $artifact,
			log_path: $log,
			stdout_path: $stdout,
			stderr_path: $stderr,
			plaintext_leak_scan_ok: true,
			secret_leak_scan_ok: true
		}' > "$result_file"
}

assert_unavailable_endpoint() {
	local addr=$1
	if "$curl_bin" --silent --show-error --cacert "$ca_cert" --max-time 2 \
		--output /dev/null "$addr/v1/sys/health" >/dev/null 2>&1; then
		die "Transit-unavailable drill endpoint unexpectedly responded: $addr"
	fi
}

run_fail_closed_drills() {
	local root_token auth_denied_token unavailable_addr unavailable_port
	root_token=$(cat "$openbao_dir/root-token")
	auth_denied_token=$(create_auth_denied_token)
	if [ "$((10#$openbao_port))" -ge 65535 ]; then
		unavailable_port=1
	else
		unavailable_port=$((10#$openbao_port + 1))
	fi
	unavailable_addr="https://127.0.0.1:${unavailable_port}"
	assert_unavailable_endpoint "$unavailable_addr"
	rm -rf "$drill_dir"
	mkdir -p "$drill_dir"
	chmod 700 "$drill_dir"
	run_fail_closed_write_drill \
		"transit_unavailable" \
		"$unavailable_addr" \
		"$root_token" \
		"$transit_key" \
		"write fails closed when OpenBao Transit is unavailable" \
		"Unavailable" \
		"ChJjcnlwdG9fdW5hdmFpbGFibGU="
	run_fail_closed_write_drill \
		"auth_denied" \
		"$openbao_addr" \
		"$auth_denied_token" \
		"$transit_key" \
		"write fails closed when OpenBao denies the configured token" \
		"Unavailable" \
		"ChJjcnlwdG9fdW5hdmFpbGFibGU="
	run_fail_closed_write_drill \
		"missing_key" \
		"$openbao_addr" \
		"$root_token" \
		"missing-scrap-documents" \
		"write fails closed when the configured Transit key is missing" \
		"DataLoss" \
		"data corruption detected"
}

json_bool() {
	if "$@"; then
		printf 'true'
	else
		printf 'false'
	fi
}

git_commit_ref() {
	if command -v git >/dev/null 2>&1 && git rev-parse HEAD >/dev/null 2>&1; then
		git rev-parse HEAD
		return
	fi
	printf 'unknown'
}

git_worktree_state() {
	if ! command -v git >/dev/null 2>&1 || ! git rev-parse HEAD >/dev/null 2>&1; then
		printf 'unknown'
		return
	fi
	if git diff --quiet HEAD --; then
		printf 'clean'
		return
	fi
	printf 'dirty'
}

git_diff_sha256() {
	if ! command -v git >/dev/null 2>&1 || ! git rev-parse HEAD >/dev/null 2>&1; then
		printf 'unknown'
		return
	fi
	if git diff --quiet HEAD --; then
		printf ''
		return
	fi
	git diff HEAD -- | "$openssl_bin" dgst -sha256 -r | awk '{print $1}'
}

rehearsal_command_name() {
	if [ -n "${SCRAP_PROD_REHEARSAL_COMMAND:-}" ]; then
		printf '%s' "$SCRAP_PROD_REHEARSAL_COMMAND"
		return
	fi
	case "$backend" in
	fs)
		printf 'make production-rehearsal-security'
		;;
	s3)
		printf 'make production-rehearsal'
		;;
	*)
		printf 'scripts/production-rehearsal.sh run'
		;;
	esac
}

evidence_tier() {
	case "$backend" in
	fs)
		printf 'local-production-security'
		;;
	s3)
		if [ "${SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3:-false}" = "true" ]; then
			printf 'local-s3-override'
			return
		fi
		printf 'real-s3-iam'
		;;
	*)
		printf 'unknown'
		;;
	esac
}

drill_results_json() {
	if find "$drill_dir" -type f -name result.json -print -quit | grep -q .; then
		find "$drill_dir" -type f -name result.json -print0 | sort -z | xargs -0 "$jq_bin" -s '.'
		return
	fi
	printf '[]\n'
}

write_report() {
	local health_file="$runtime_dir/health.json"
	local upload_confirmation_file="$runtime_dir/upload-confirmation.json"
	local status readiness confirmed_count drills_json command commit_ref worktree_state diff_sha timestamp tier
	status=$("$jq_bin" -r '.security_mode' "$health_file")
	readiness=$("$jq_bin" -r '.production_readiness_status' "$health_file")
	confirmed_count=$("$jq_bin" -r '.confirmed_upload_count' "$upload_confirmation_file")
	drills_json=$(drill_results_json)
	command=$(rehearsal_command_name)
	commit_ref=$(git_commit_ref)
	worktree_state=$(git_worktree_state)
	diff_sha=$(git_diff_sha256)
	timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	tier=$(evidence_tier)
	"$jq_bin" -n \
		--arg status "passed" \
		--arg command "$command" \
		--arg commit_ref "$commit_ref" \
		--arg worktree_state "$worktree_state" \
		--arg diff_sha "$diff_sha" \
		--arg timestamp "$timestamp" \
		--arg environment "production-rehearsal" \
		--arg tier "$tier" \
		--arg security_mode "$status" \
		--arg readiness "$readiness" \
		--arg backend "$backend" \
		--arg openbao_image "$openbao_image" \
		--arg expected "production mode with real OpenBao Transit, encrypted write/read, committed Backend upload confirmation, fail-closed drills, and redacted artifacts passes" \
		--arg actual "production security rehearsal passed" \
		--arg artifact "$report_file" \
		--arg work_dir "$work_dir" \
		--arg placement_file "$placement_file" \
		--arg log_dir "$log_dir" \
		--arg data_dir "$data_dir" \
		--arg drill_dir "$drill_dir" \
		--arg bootstrap_evidence "$bootstrap_evidence_file" \
		--arg redaction_scan "$redaction_scan_file" \
		--argjson confirmed_count "$confirmed_count" \
		--argjson drills "$drills_json" \
		--argjson filesystem_backend "$(json_bool test "$backend" = "fs")" \
		--argjson local_s3_override "$(json_bool test "${SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3:-false}" = "true")" \
		--argjson real_s3_iam "$(json_bool test "$tier" = "real-s3-iam")" \
		'{
			status: $status,
			command: $command,
			commit_ref: $commit_ref,
			git_worktree_state: $worktree_state,
			git_diff_sha256: $diff_sha,
			timestamp: $timestamp,
			environment: $environment,
			evidence_tier: $tier,
			expected_result: $expected,
			actual_result: $actual,
			artifact_path: $artifact,
			report_path: $artifact,
			work_dir: $work_dir,
			shard_placement: {
				file: $placement_file,
				slot_count: 1024,
				local_shards: [7, 9],
				route_map: "0-511:shard=7,512-1023:shard=9"
			},
			security_mode: $security_mode,
			production_readiness_status: $readiness,
			backend: $backend,
			local_overrides: {
				filesystem_backend: $filesystem_backend,
				local_s3_endpoint_allowed: $local_s3_override,
				real_s3_iam: $real_s3_iam
			},
			openbao_image: $openbao_image,
			openbao_transit: "real",
			openbao_bootstrap: {
				command: "scrapctl openbao bootstrap",
				evidence_path: $bootstrap_evidence
			},
			test_hooks_enabled: false,
			pprof_enabled: false,
			encrypted_write_read_ok: true,
			plaintext_leak_scan_ok: true,
			backend_upload_confirmed: true,
			confirmed_upload_count: $confirmed_count,
			fail_closed_drills: $drills,
			redaction_proof: {
				status: "passed",
				plaintext_leak_scan_ok: true,
				report_excludes_secret_material: true,
				tracker_ready_evidence_excludes_raw_logs: true,
				scan_artifact_path: $redaction_scan,
				scan_scope: [$data_dir, $artifact, $drill_dir]
			},
			log_dir: $log_dir
		}' > "$report_file"
	log "wrote report: $report_file"
}

assert_report_invariants() {
	"$jq_bin" -e '
		.status == "passed" and
		.command != "" and
		.commit_ref != "" and
		(.git_worktree_state == "clean" or .git_worktree_state == "dirty" or .git_worktree_state == "unknown") and
		(.git_worktree_state != "dirty" or .git_diff_sha256 != "") and
		.timestamp != "" and
		.environment == "production-rehearsal" and
		.evidence_tier != "" and
		.expected_result != "" and
		.actual_result != "" and
		.artifact_path != "" and
		.shard_placement.slot_count == 1024 and
		.shard_placement.local_shards == [7, 9] and
		.security_mode == "production" and
		.production_readiness_status == "ready" and
		.backend != "" and
		(.local_overrides.real_s3_iam | type) == "boolean" and
		.openbao_transit == "real" and
		.openbao_bootstrap.command == "scrapctl openbao bootstrap" and
		.openbao_bootstrap.evidence_path != "" and
		.test_hooks_enabled == false and
		.pprof_enabled == false and
		.encrypted_write_read_ok == true and
		.plaintext_leak_scan_ok == true and
		.backend_upload_confirmed == true and
		.confirmed_upload_count > 0 and
		(.fail_closed_drills | length) == 3 and
		all(.fail_closed_drills[]; .status == "pass" and .expected_result != "" and .actual_result != "" and .artifact_path != "" and .plaintext_leak_scan_ok == true and .secret_leak_scan_ok == true) and
		.redaction_proof.status == "passed" and
		.redaction_proof.scan_artifact_path != ""
	' "$report_file" >/dev/null || die "production rehearsal report failed invariant validation; see $report_file"
}

run_rehearsal() {
	require_command "$docker_bin" docker
	require_command "$openssl_bin" openssl
	require_command "$jq_bin" jq
	require_command "$curl_bin" curl
	require_command "$grpcurl_bin" grpcurl
	require_command "$base64_bin" base64
	require_command "$cmp_bin" cmp
	require_file "$scrapd_bin" scrapd
	require_file "$scrapctl_bin" scrapctl
	validate_rehearsal_config
	prepare_workspace
	write_tls_material
	write_policies
	write_shard_placement
	write_openbao_config
	trap cleanup EXIT INT TERM
	start_openbao
	start_scrapd
	verify_admin_health
	write_read_document
	stop_scrapd
	run_fail_closed_drills
	write_redaction_scan false
	write_report
	write_redaction_scan true
	assert_report_invariants
	log "production rehearsal passed"
}

case "$command_name" in
run)
	run_rehearsal
	;;
down)
	stop_scrapd
	stop_openbao
	;;
*)
	die "usage: scripts/production-rehearsal.sh [run|down]"
	;;
esac
