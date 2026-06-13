ARG ACT_RUNNER_BASE_IMAGE=catthehacker/ubuntu:act-24.04
FROM ${ACT_RUNNER_BASE_IMAGE}

ARG TARGETARCH
ARG KUBECTL_VERSION=v1.34.2

# Docker 29.5.1 rejects act's copy into /var/run/act when /var/run is a symlink.
RUN rm -f /var/run && mkdir -p /var/run

RUN apt-get update \
	&& apt-get install --no-install-recommends -y ca-certificates curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& arch="${TARGETARCH:-amd64}" \
	&& case "$arch" in amd64|arm64) ;; *) printf 'unsupported TARGETARCH: %s\n' "$arch" >&2; exit 1 ;; esac \
	&& curl -fsSLo /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" \
	&& chmod +x /usr/local/bin/kubectl \
	&& kubectl version --client=true
