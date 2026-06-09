#!/usr/bin/env bash
set -euo pipefail

command_name=${1:-run}

work_dir=${SCRAP_PROD_REHEARSAL_DIR:-artifacts/production-rehearsal}
backend=${SCRAP_PROD_REHEARSAL_BACKEND:-s3}
cell_id=${SCRAP_PROD_REHEARSAL_CELL_ID:-production-rehearsal}
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

runtime_dir="$work_dir/runtime"
tls_dir="$runtime_dir/tls"
policy_dir="$runtime_dir/policies"
openbao_dir="$runtime_dir/openbao"
data_dir="$runtime_dir/scrap-data"
log_dir="$runtime_dir/logs"
report_file="$work_dir/report.json"
scrapd_log="$log_dir/scrapd.log"
openbao_log="$log_dir/openbao.log"
scrapd_pid_file="$runtime_dir/scrapd.pid"

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
	stop_scrapd
	stop_openbao
}

prepare_workspace() {
	rm -rf "$runtime_dir"
	mkdir -p "$tls_dir" "$policy_dir" "$openbao_dir/config" "$openbao_dir/data" "$data_dir" "$log_dir"
	chmod 700 "$work_dir" "$runtime_dir" "$tls_dir" "$policy_dir" "$openbao_dir" "$data_dir" "$log_dir"
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
	local init_json unseal_key root_token
	init_json=$("$curl_bin" --silent --show-error --fail-with-body --cacert "$ca_cert" \
		--request PUT \
		--data '{"secret_shares":1,"secret_threshold":1}' \
		"$openbao_addr/v1/sys/init")
	printf '%s\n' "$init_json" > "$openbao_dir/init.json"
	unseal_key=$(printf '%s\n' "$init_json" | "$jq_bin" -r '.keys_base64[0] // .unseal_keys_b64[0]')
	root_token=$(printf '%s\n' "$init_json" | "$jq_bin" -r '.root_token')
	[ -n "$unseal_key" ] && [ "$unseal_key" != "null" ] || die "OpenBao init did not return an unseal key"
	[ -n "$root_token" ] && [ "$root_token" != "null" ] || die "OpenBao init did not return a root token"
	printf '%s' "$root_token" > "$openbao_dir/root-token"
	chmod 600 "$openbao_dir/init.json" "$openbao_dir/root-token"

	"$curl_bin" --silent --show-error --fail-with-body --cacert "$ca_cert" \
		--request PUT \
		--data "{\"key\":\"${unseal_key}\"}" \
		"$openbao_addr/v1/sys/unseal" >/dev/null

	"$curl_bin" --silent --show-error --fail-with-body --cacert "$ca_cert" \
		--header "X-Vault-Token: ${root_token}" \
		--request POST \
		--data '{"type":"transit"}' \
		"$openbao_addr/v1/sys/mounts/${transit_mount}" >/dev/null

	"$curl_bin" --silent --show-error --fail-with-body --cacert "$ca_cert" \
		--header "X-Vault-Token: ${root_token}" \
		--request POST \
		--data '{"type":"aes256-gcm96"}' \
		"$openbao_addr/v1/${transit_mount}/keys/${transit_key}" >/dev/null
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
		SCRAP_TRANSIT_ADDR="$openbao_addr" \
		SCRAP_TRANSIT_MOUNT="$transit_mount" \
		SCRAP_TRANSIT_KEY="$transit_key" \
		SCRAP_TRANSIT_TOKEN_ENV="OPENBAO_TOKEN" \
		OPENBAO_TOKEN="$root_token" \
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

write_read_document() {
	local tx_id doc_name payload payload_b64 write_req write_out head_out read_out
	tx_id="prod-rehearsal-$(date -u +%Y%m%dT%H%M%SZ)"
	doc_name="readiness.txt"
	payload="production rehearsal encrypted document payload ${tx_id}"
	payload_b64=$(printf '%s' "$payload" | base64 -w0)
	write_req="$runtime_dir/write-request.json"
	write_out="$runtime_dir/write-response.json"
	head_out="$runtime_dir/head-response.json"
	read_out="$runtime_dir/read-response.json"

	cat > "$write_req" <<EOF
{"init":{"transactionId":"${tx_id}","documentName":"${doc_name}","contentType":"text/plain","idempotencyKey":"${tx_id}"}}
{"chunkData":"${payload_b64}"}
EOF
	"$grpcurl_bin" $(grpcurl_common_args) \
		-d @ "127.0.0.1:${client_port}" scrap.v1.DocumentService/WriteDocument \
		< "$write_req" > "$write_out"
	"$jq_bin" -e '.size > 0 and .sha256Checksum != ""' "$write_out" >/dev/null ||
		die "WriteDocument response is incomplete; see $write_out"

	"$grpcurl_bin" $(grpcurl_common_args) \
		-d "{\"transactionId\":\"${tx_id}\",\"documentName\":\"${doc_name}\"}" \
		"127.0.0.1:${client_port}" scrap.v1.DocumentService/HeadDocument > "$head_out"
	"$jq_bin" -e '.size > 0 and .sha256Checksum != ""' "$head_out" >/dev/null ||
		die "HeadDocument response is incomplete; see $head_out"

	"$grpcurl_bin" $(grpcurl_common_args) \
		-d "{\"transactionId\":\"${tx_id}\",\"documentName\":\"${doc_name}\"}" \
		"127.0.0.1:${client_port}" scrap.v1.DocumentService/ReadDocument > "$read_out"
	grep -F "$payload_b64" "$read_out" >/dev/null ||
		die "ReadDocument did not return the written payload; see $read_out"

	if grep -R -a -F "$payload" "$data_dir" >/dev/null 2>&1; then
		die "plaintext payload was found under $data_dir"
	fi
	wait_uploads_drained
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

write_report() {
	local health_file="$runtime_dir/health.json"
	local status readiness
	status=$("$jq_bin" -r '.security_mode' "$health_file")
	readiness=$("$jq_bin" -r '.production_readiness_status' "$health_file")
	cat > "$report_file" <<EOF
{
  "status": "passed",
  "security_mode": "${status}",
  "production_readiness_status": "${readiness}",
  "backend": "${backend}",
  "openbao_image": "${openbao_image}",
  "openbao_transit": "real",
  "test_hooks_enabled": false,
  "pprof_enabled": false,
  "encrypted_write_read_ok": true,
  "plaintext_leak_scan_ok": true,
  "log_dir": "${log_dir}"
}
EOF
	log "wrote report: $report_file"
}

run_rehearsal() {
	require_command "$docker_bin" docker
	require_command "$openssl_bin" openssl
	require_command "$jq_bin" jq
	require_command "$curl_bin" curl
	require_command "$grpcurl_bin" grpcurl
	require_file "$scrapd_bin" scrapd
	require_file "$scrapctl_bin" scrapctl
	validate_backend_config
	prepare_workspace
	write_tls_material
	write_policies
	write_openbao_config
	trap cleanup EXIT INT TERM
	start_openbao
	start_scrapd
	verify_admin_health
	write_read_document
	write_report
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
