FROM alpine:3.5

COPY bin/ticloud-manager /usr/local/bin/ticloud-manager
COPY pd.toml.tmpl /pd.toml.tmpl
COPY tikv.toml.tmpl /tikv.toml.tmpl

ENTRYPOINT ["/usr/local/bin/ticloud-manager"]
