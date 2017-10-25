FROM alpine:3.6

COPY build/kubeaware-cloudpool-proxy-alpine /usr/local/bin/kubeaware-cloudpool-proxy

# should contain a config.json to use image without --config-file flag
VOLUME [ "/etc/elastisys/" ]

ENTRYPOINT [ "/usr/local/bin/kubeaware-cloudpool-proxy" ]