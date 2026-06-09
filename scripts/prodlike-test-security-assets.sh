#!/usr/bin/env bash
set -euo pipefail

asset_dir=${1:-artifacts/prodlike-security}
namespace=${SCRAP_E2E_NAMESPACE:-scrap}
server_name=${SCRAP_E2E_TLS_SERVER_NAME:-scrap.local}
principal=${SCRAP_E2E_PRINCIPAL:-spiffe://scrap/cell/kind-prodlike/member/scrapd-e2e/member-1}
kubectl_bin=${KUBECTL:-kubectl}
kube_context=${PRODLIKE_E2E_KUBE_CONTEXT:-${PRODLIKE_KUBE_CONTEXT:-}}
kubeconfig=${KUBECONFIG:-}

kubectl_args=()
if [ -n "$kube_context" ]; then
	kubectl_args+=(--context "$kube_context")
fi
if [ -n "$kubeconfig" ]; then
	kubectl_args+=(--kubeconfig "$kubeconfig")
fi

mkdir -p "$asset_dir"
chmod 700 "$asset_dir"

ca_key="$asset_dir/ca.key"
ca_cert="$asset_dir/ca.pem"
leaf_key="$asset_dir/scrap.key"
leaf_csr="$asset_dir/scrap.csr"
leaf_cert="$asset_dir/scrap.pem"
ext_file="$asset_dir/scrap.ext"
roles_file="$asset_dir/roles.json"
report_file="$asset_dir/security-evidence.json"

rm -f "$report_file"

ca_is_usable() {
	local cert_text
	if [ ! -s "$ca_cert" ]; then
		return 1
	fi
	cert_text=$(openssl x509 -in "$ca_cert" -noout -text 2>/dev/null) || return 1
	grep -q 'CA:TRUE' <<<"$cert_text" || return 1
	grep -q 'Certificate Sign' <<<"$cert_text" || return 1
}

if [ ! -s "$ca_key" ] || [ ! -s "$leaf_key" ] || [ ! -s "$leaf_cert" ] || ! ca_is_usable; then
	rm -f "$ca_key" "$ca_cert" "$leaf_key" "$leaf_csr" "$leaf_cert" "$ext_file" "$asset_dir/ca.srl"

	openssl ecparam -name prime256v1 -genkey -noout -out "$ca_key"
	openssl req -x509 -new -key "$ca_key" -sha256 -days 7 -out "$ca_cert" \
		-subj "/CN=scrap prodlike e2e CA" \
		-addext "basicConstraints=critical,CA:TRUE" \
		-addext "keyUsage=critical,keyCertSign,cRLSign"

	openssl ecparam -name prime256v1 -genkey -noout -out "$leaf_key"
	openssl req -new -key "$leaf_key" -out "$leaf_csr" -subj "/CN=scrap prodlike e2e"
	cat > "$ext_file" <<EOF
basicConstraints=CA:FALSE
keyUsage=digitalSignature
extendedKeyUsage=serverAuth,clientAuth
subjectAltName=DNS:${server_name},IP:127.0.0.1,URI:${principal}
EOF
	openssl x509 -req -in "$leaf_csr" -CA "$ca_cert" -CAkey "$ca_key" -CAcreateserial \
		-out "$leaf_cert" -days 7 -sha256 -extfile "$ext_file"
	chmod 600 "$ca_key" "$leaf_key"
fi

cat > "$roles_file" <<EOF
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

"$kubectl_bin" "${kubectl_args[@]}" create namespace "$namespace" --dry-run=client -o yaml |
	"$kubectl_bin" "${kubectl_args[@]}" apply -f -

"$kubectl_bin" "${kubectl_args[@]}" -n "$namespace" create secret generic scrap-test-security \
	--from-file=ca.pem="$ca_cert" \
	--from-file=scrap.pem="$leaf_cert" \
	--from-file=scrap.key="$leaf_key" \
	--from-file=roles.json="$roles_file" \
	--dry-run=client -o yaml |
	"$kubectl_bin" "${kubectl_args[@]}" apply -f -
